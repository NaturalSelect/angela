package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/NaturalSelect/angela/internal/oauth"
	mcpoauth "github.com/NaturalSelect/angela/internal/oauth/mcp"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"
	"golang.org/x/oauth2"
)

// shellResolverWithPath builds a shell resolver whose env carries PATH
// plus any caller-supplied overrides. Without PATH, $(cat), $(echo),
// etc. can't find their binaries in a test process where the shell env
// is otherwise empty.
func shellResolverWithPath(t *testing.T, overrides map[string]string) config.VariableResolver {
	t.Helper()
	m := map[string]string{"PATH": os.Getenv("PATH")}
	maps.Copy(m, overrides)
	return config.NewShellVariableResolver(env.NewFromMap(m))
}

func TestMCPSession_CancelOnClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server"}, nil)
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()

	ctx, cancel := context.WithCancel(context.Background())

	client := mcp.NewClient(&mcp.Implementation{Name: "angela-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	sess := &ClientSession{ClientSession: clientSession, cancel: cancel}

	// Verify the context is not cancelled before close.
	require.NoError(t, ctx.Err())

	err = sess.Close()
	require.NoError(t, err)

	// After Close, the context must be cancelled.
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

// TestCreateTransport_URLResolution pins that m.URL goes through the
// same resolver seam as command, args, env, and headers. Covers both
// the HTTP and SSE branches, success and failure, so a regression in
// ResolvedURL wiring is caught at the transport layer rather than only
// at the config layer.
func TestCreateTransport_URLResolution(t *testing.T) {
	t.Parallel()

	shell := config.NewShellVariableResolver(env.NewFromMap(map[string]string{
		"MCP_HOST": "mcp.example.com",
	}))

	t.Run("http success expands $VAR", func(t *testing.T) {
		t.Parallel()
		m := config.MCPConfig{
			Type: config.MCPHttp,
			URL:  "https://$MCP_HOST/api",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, shell)
		require.NoError(t, err)
		require.NotNil(t, tr)
		sct, ok := tr.(*mcp.StreamableClientTransport)
		require.True(t, ok, "expected StreamableClientTransport, got %T", tr)
		require.Equal(t, "https://mcp.example.com/api", sct.Endpoint)
	})

	t.Run("sse success expands $(cmd)", func(t *testing.T) {
		t.Parallel()
		m := config.MCPConfig{
			Type: config.MCPSSE,
			URL:  "https://$(echo mcp.example.com)/events",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, shell)
		require.NoError(t, err)
		sse, ok := tr.(*mcp.SSEClientTransport)
		require.True(t, ok, "expected SSEClientTransport, got %T", tr)
		require.Equal(t, "https://mcp.example.com/events", sse.Endpoint)
	})

	t.Run("http failing $(cmd) surfaces error, no transport created", func(t *testing.T) {
		t.Parallel()
		// Under lenient nounset, unset $VAR expands to "" silently,
		// so the only way a URL resolution *errors* is a failing
		// $(cmd). Mirror the SSE subtest so both transports share
		// coverage for the url-resolve-failure path.
		m := config.MCPConfig{
			Type: config.MCPHttp,
			URL:  "https://$(false)/api",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, shellResolverWithPath(t, nil))
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "url:")
		require.Contains(t, err.Error(), "$(false)")
	})

	t.Run("http unset var expands empty", func(t *testing.T) {
		t.Parallel()
		// Pinning test for the new lenient-nounset default: an
		// unset bare $VAR in the URL is *not* an error. It
		// expands to "" and, here, leaves a syntactically weird
		// but non-empty URL that the existing non-empty guard
		// still lets through. Guards against a future regression
		// that flips strict-by-default back on.
		m := config.MCPConfig{
			Type: config.MCPHttp,
			URL:  "https://$MCP_MISSING_HOST/api",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, shell)
		require.NoError(t, err)
		sct, ok := tr.(*mcp.StreamableClientTransport)
		require.True(t, ok)
		require.Equal(t, "https:///api", sct.Endpoint)
	})

	t.Run("sse failing $(cmd) surfaces error, no transport created", func(t *testing.T) {
		t.Parallel()
		m := config.MCPConfig{
			Type: config.MCPSSE,
			URL:  "https://$(false)/events",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, shell)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "url:")
		require.Contains(t, err.Error(), "$(false)")
	})

	t.Run("http empty-after-resolve still fails the non-empty guard", func(t *testing.T) {
		t.Parallel()
		// ${MCP_EMPTY:-} resolves to the empty string (no error),
		// then the existing TrimSpace guard in createTransport must
		// reject it so we never spawn a transport against "".
		m := config.MCPConfig{
			Type: config.MCPHttp,
			URL:  "${MCP_EMPTY:-}",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, shell)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "non-empty 'url'")
	})

	t.Run("identity resolver round-trips template verbatim", func(t *testing.T) {
		t.Parallel()
		// Client mode forwards the template to the server; no local
		// expansion, no error on unset vars.
		tmpl := "https://$MCP_MISSING_HOST/api"
		m := config.MCPConfig{Type: config.MCPHttp, URL: tmpl}
		tr, _, err := createTransport(t.Context(), nil, "test", m, config.IdentityResolver())
		require.NoError(t, err)
		sct, ok := tr.(*mcp.StreamableClientTransport)
		require.True(t, ok)
		require.Equal(t, tmpl, sct.Endpoint)
	})
}

// TestCreateTransport_StdioResolution pins that command, args, and env
// for stdio MCPs go through the same resolver seam as the other
// transports. Covers both success (expansion produced the expected
// exec.Cmd) and failure (any one field erroring prevents transport
// creation).
func TestCreateTransport_StdioResolution(t *testing.T) {
	t.Parallel()

	t.Run("success expands command, args, and env", func(t *testing.T) {
		t.Parallel()
		r := shellResolverWithPath(t, map[string]string{
			"MY_TOKEN": "hunter2",
		})
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "forgejo-mcp",
			Args:    []string{"--token", "$MY_TOKEN", "--host", "$(echo example.com)"},
			Env: map[string]string{
				"SECRET":    "$(echo shh)",
				"PLAIN":     "literal",
				"REFERENCE": "$MY_TOKEN",
			},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.NoError(t, err)
		require.NotNil(t, tr)

		ct, ok := tr.(*mcp.CommandTransport)
		require.True(t, ok, "expected CommandTransport, got %T", tr)

		// exec.Cmd.Args[0] is the command name; the rest are positional
		// args as passed.
		require.Equal(t, []string{"forgejo-mcp", "--token", "hunter2", "--host", "example.com"}, ct.Command.Args)

		// Env is os.Environ() + resolved entries (sorted). Check the
		// resolved entries are present with their expanded values.
		require.Contains(t, ct.Command.Env, "SECRET=shh")
		require.Contains(t, ct.Command.Env, "PLAIN=literal")
		require.Contains(t, ct.Command.Env, "REFERENCE=hunter2")
	})

	t.Run("env resolution failure surfaces error, no transport created", func(t *testing.T) {
		t.Parallel()
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "forgejo-mcp",
			Env:     map[string]string{"TOKEN": "$(false)"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "env TOKEN")
	})

	t.Run("failing env command is a hard error", func(t *testing.T) {
		t.Parallel()
		// Under lenient nounset a bare $UNSET expands to ""
		// silently — see the pinning subtest below. The remaining
		// failure mode for env resolution is a $(cmd) that exits
		// non-zero, which must still error out and prevent exec so
		// we never hand a broken credential to the child process.
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "forgejo-mcp",
			Env:     map[string]string{"FORGEJO_ACCESS_TOKEN": "$(exit 5)"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "env FORGEJO_ACCESS_TOKEN")
	})

	t.Run("unset env var expands empty", func(t *testing.T) {
		t.Parallel()
		// Pinning test for the lenient-nounset default: a bare
		// $UNSET in an env value expands to "" without error, and
		// the empty entry is kept on the resulting exec.Cmd (env
		// entries, unlike headers, are not dropped — see design
		// decision #18). Guards against a regression that flips
		// strict-by-default back on and silently breaks users
		// with configs like FORGEJO_ACCESS_TOKEN=$FORGEJO_TOKEN.
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "forgejo-mcp",
			Env:     map[string]string{"FORGEJO_ACCESS_TOKEN": "$FORGEJO_TOKEN_UNSET"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.NoError(t, err)
		ct, ok := tr.(*mcp.CommandTransport)
		require.True(t, ok)
		require.Contains(t, ct.Command.Env, "FORGEJO_ACCESS_TOKEN=")
	})

	t.Run("args resolution failure surfaces error, no transport created", func(t *testing.T) {
		t.Parallel()
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "forgejo-mcp",
			Args:    []string{"--token", "$(false)"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "arg 1")
	})

	t.Run("command resolution failure surfaces error, no transport created", func(t *testing.T) {
		t.Parallel()
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "$(false)",
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "invalid mcp command")
	})

	t.Run("identity resolver round-trips templates verbatim", func(t *testing.T) {
		t.Parallel()
		// Client mode: no local expansion, no error on unset vars.
		m := config.MCPConfig{
			Type:    config.MCPStdio,
			Command: "forgejo-mcp",
			Args:    []string{"--token", "$MCP_MISSING"},
			Env:     map[string]string{"TOKEN": "$(vault read -f token)"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, config.IdentityResolver())
		require.NoError(t, err)
		ct, ok := tr.(*mcp.CommandTransport)
		require.True(t, ok)
		require.Equal(t, []string{"forgejo-mcp", "--token", "$MCP_MISSING"}, ct.Command.Args)
		require.Contains(t, ct.Command.Env, "TOKEN=$(vault read -f token)")
	})
}

// TestCreateTransport_HeadersResolution pins that a single failing
// header aborts HTTP/SSE transport creation and that the successful
// resolver passes every expanded header through to the round tripper.
func TestCreateTransport_HeadersResolution(t *testing.T) {
	t.Parallel()

	t.Run("http headers success expands $(cmd)", func(t *testing.T) {
		t.Parallel()
		r := shellResolverWithPath(t, map[string]string{
			"GITHUB_TOKEN": "gh-secret",
		})
		m := config.MCPConfig{
			Type: config.MCPHttp,
			URL:  "https://mcp.example.com/api",
			Headers: map[string]string{
				"Authorization": "$(echo Bearer $GITHUB_TOKEN)",
				"X-Static":      "kept",
			},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.NoError(t, err)

		sct, ok := tr.(*mcp.StreamableClientTransport)
		require.True(t, ok)
		rt, ok := sct.HTTPClient.Transport.(*headerRoundTripper)
		require.True(t, ok, "expected headerRoundTripper, got %T", sct.HTTPClient.Transport)
		require.Equal(t, map[string]string{
			"Authorization": "Bearer gh-secret",
			"X-Static":      "kept",
		}, rt.headers)
	})

	t.Run("http failing header surfaces error, no transport", func(t *testing.T) {
		t.Parallel()
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPHttp,
			URL:     "https://mcp.example.com/api",
			Headers: map[string]string{"Authorization": "$(false)"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "header Authorization")
	})

	t.Run("sse failing header surfaces error, no transport", func(t *testing.T) {
		t.Parallel()
		// Under lenient nounset a bare $MISSING expands to "",
		// which ResolvedHeaders drops — no error. The failing
		// $(cmd) path is the remaining way this can fail loudly;
		// cover it on the SSE branch to mirror the HTTP subtest.
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPSSE,
			URL:     "https://mcp.example.com/events",
			Headers: map[string]string{"Authorization": "$(false)"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.Error(t, err)
		require.Nil(t, tr)
		require.Contains(t, err.Error(), "header Authorization")
	})

	t.Run("sse unset var header drops silently", func(t *testing.T) {
		t.Parallel()
		// Pinning test for empty-header drop + lenient nounset:
		// a header whose value resolves to "" (here because the
		// bare $VAR is unset) is omitted from the round tripper
		// rather than sent as "X-Header:". Guards against a
		// regression that either re-introduces strict-by-default
		// or stops dropping empty headers.
		r := shellResolverWithPath(t, nil)
		m := config.MCPConfig{
			Type:    config.MCPSSE,
			URL:     "https://mcp.example.com/events",
			Headers: map[string]string{"Authorization": "$MISSING_TOKEN"},
		}
		tr, _, err := createTransport(t.Context(), nil, "test", m, r)
		require.NoError(t, err)
		sse, ok := tr.(*mcp.SSEClientTransport)
		require.True(t, ok)
		rt, ok := sse.HTTPClient.Transport.(*headerRoundTripper)
		require.True(t, ok)
		require.NotContains(t, rt.headers, "Authorization")
	})
}

// TestCreateSession_ResolutionFailureUpdatesState pins the user-visible
// half of the regression fix: when any of command/args/env/headers/url
// fails to resolve, createSession must publish StateError to the state
// map so angela_info and the TUI's MCP status card can render a real
// error instead of the MCP silently sitting in "starting" or being
// spawned with an empty credential.
//
// These subtests cannot run in parallel: `states` is a package-level
// csync.Map and each assertion reads the entry written by the call
// under test. They do use unique MCP names per subtest to keep them
// independent regardless of ordering.
func TestCreateSession_ResolutionFailureUpdatesState(t *testing.T) {
	r := shellResolverWithPath(t, nil)

	tests := []struct {
		name            string
		mcpName         string
		cfg             config.MCPConfig
		wantErrContains string
	}{
		{
			name:    "stdio env failure",
			mcpName: "test-stdio-env-fail",
			cfg: config.MCPConfig{
				Type:    config.MCPStdio,
				Command: "echo",
				Env:     map[string]string{"FORGEJO_ACCESS_TOKEN": "$(false)"},
			},
			wantErrContains: "env FORGEJO_ACCESS_TOKEN",
		},
		{
			// Args that reference an unset bare $VAR no longer
			// error out under lenient nounset; the only remaining
			// failure mode for arg resolution is a failing $(cmd).
			name:    "stdio args failure",
			mcpName: "test-stdio-args-fail",
			cfg: config.MCPConfig{
				Type:    config.MCPStdio,
				Command: "echo",
				Args:    []string{"--token", "$(false)"},
			},
			wantErrContains: "arg 1",
		},
		{
			// Likewise for URL: bare $UNSET expands to ""
			// silently, so we need a failing $(cmd) to exercise
			// the "url:" wrap from ResolvedURL.
			name:    "http url failure",
			mcpName: "test-http-url-fail",
			cfg: config.MCPConfig{
				Type: config.MCPHttp,
				URL:  "https://$(false)/api",
			},
			wantErrContains: "url:",
		},
		{
			// A URL whose shell expansion yields the empty
			// string (here via ${VAR:-}) is not a ResolvedURL
			// error, but the non-empty guard in createTransport
			// must still reject it so the state card renders an
			// error instead of spawning a transport against "".
			name:    "http empty-resolved url",
			mcpName: "test-http-url-empty",
			cfg: config.MCPConfig{
				Type: config.MCPHttp,
				URL:  "${MCP_URL_EMPTY:-}",
			},
			wantErrContains: "non-empty 'url'",
		},
		{
			name:    "http header failure",
			mcpName: "test-http-header-fail",
			cfg: config.MCPConfig{
				Type:    config.MCPHttp,
				URL:     "https://mcp.example.com/api",
				Headers: map[string]string{"Authorization": "$(false)"},
			},
			wantErrContains: "header Authorization",
		},
		{
			name:    "sse url failure",
			mcpName: "test-sse-url-fail",
			cfg: config.MCPConfig{
				Type: config.MCPSSE,
				URL:  "https://$(false)/events",
			},
			wantErrContains: "url:",
		},
		{
			// Bare $MISSING in a header resolves to "" silently
			// and is then dropped. The "header Authorization"
			// wrap only surfaces on a $(cmd) failure; that is
			// what this subtest now pins for the SSE path.
			name:    "sse header failure",
			mcpName: "test-sse-header-fail",
			cfg: config.MCPConfig{
				Type:    config.MCPSSE,
				URL:     "https://mcp.example.com/events",
				Headers: map[string]string{"Authorization": "$(false)"},
			},
			wantErrContains: "header Authorization",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Guarantee a clean slate on the shared state map so a
			// stale entry from another test can't satisfy the
			// assertion.
			states.Del(tc.mcpName)
			t.Cleanup(func() { states.Del(tc.mcpName) })

			sess, err := createSession(t.Context(), nil, tc.mcpName, tc.cfg, r, false)
			require.Error(t, err)
			require.Nil(t, sess)
			require.Contains(t, err.Error(), tc.wantErrContains)

			info, ok := GetState(tc.mcpName)
			require.True(t, ok, "state entry must be written for %q", tc.mcpName)
			require.Equal(t, StateError, info.State, "expected StateError, got %s", info.State)
			require.Error(t, info.Error, "state must carry the failure error")
			require.Contains(t, info.Error.Error(), tc.wantErrContains)
			require.Nil(t, info.Client, "no client session on failure")
		})
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	base := config.MCPConfig{
		Type: config.MCPHttp,
		URL:  "https://example.com/mcp",
	}
	changed := func() config.MCPConfig { m := base; m.URL = "https://other.com/mcp"; return m }()
	disabled := func() config.MCPConfig { m := base; m.Disabled = true; return m }()
	ptr := func(m config.MCPConfig) *config.MCPConfig { return &m }

	// server seeds the running state reconcile diffs against: a state, the
	// config the server last connected with (Config), and, for a server
	// mid-connect, the config that attempt is connecting with (PendingConfig).
	type server struct {
		state   State
		config  config.MCPConfig
		pending *config.MCPConfig
	}

	tests := []struct {
		name    string
		servers map[string]server
		current config.MCPs
		want    map[string]reinitAction
	}{
		{
			name:    "new server starts",
			current: config.MCPs{"a": base},
			want:    map[string]reinitAction{"a": reinitStart},
		},
		{
			name:    "removed server is cleaned up",
			servers: map[string]server{"gone": {state: StateConnected, config: base}},
			current: config.MCPs{},
			want:    map[string]reinitAction{"gone": reinitRemove},
		},
		{
			name:    "unchanged connected server is skipped",
			servers: map[string]server{"a": {state: StateConnected, config: base}},
			current: config.MCPs{"a": base},
			want:    map[string]reinitAction{},
		},
		{
			name:    "changed config restarts",
			servers: map[string]server{"a": {state: StateConnected, config: base}},
			current: config.MCPs{"a": changed},
			want:    map[string]reinitAction{"a": reinitStart},
		},
		{
			name:    "disabled server is disabled",
			servers: map[string]server{"a": {state: StateConnected, config: base}},
			current: config.MCPs{"a": disabled},
			want:    map[string]reinitAction{"a": reinitDisable},
		},
		{
			name:    "already disabled server is skipped",
			servers: map[string]server{"a": {state: StateDisabled}},
			current: config.MCPs{"a": disabled},
			want:    map[string]reinitAction{},
		},
		{
			// Regression: disabling clears the recorded config, so a server
			// left disabled with an unchanged config must restart on re-enable
			// rather than being skipped as "already initialized".
			name:    "re-enabled server restarts despite unchanged config",
			servers: map[string]server{"a": {state: StateDisabled}},
			current: config.MCPs{"a": base},
			want:    map[string]reinitAction{"a": reinitStart},
		},
		{
			name:    "errored server restarts",
			servers: map[string]server{"a": {state: StateError, config: base}},
			current: config.MCPs{"a": base},
			want:    map[string]reinitAction{"a": reinitStart},
		},
		{
			name:    "starting server connecting with current config is left alone",
			servers: map[string]server{"a": {state: StateStarting, pending: ptr(base)}},
			current: config.MCPs{"a": base},
			want:    map[string]reinitAction{},
		},
		{
			// Regression: a config change that lands while a server is still
			// connecting must restart it, otherwise the in-flight attempt
			// connects with the old config and the change is silently lost.
			name:    "starting server with changed config restarts",
			servers: map[string]server{"a": {state: StateStarting, pending: ptr(base)}},
			current: config.MCPs{"a": changed},
			want:    map[string]reinitAction{"a": reinitStart},
		},
		{
			name: "mixed scenario",
			servers: map[string]server{
				"keep":    {state: StateConnected, config: base},
				"remove":  {state: StateConnected, config: base},
				"restart": {state: StateConnected, config: base},
			},
			current: config.MCPs{
				"keep":    base,
				"restart": changed,
				"new":     base,
			},
			want: map[string]reinitAction{
				"remove":  reinitRemove,
				"restart": reinitStart,
				"new":     reinitStart,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			running := make(map[string]ClientInfo, len(tc.servers))
			for name, s := range tc.servers {
				running[name] = ClientInfo{
					Name:          name,
					State:         s.state,
					Config:        s.config,
					PendingConfig: s.pending,
				}
			}
			got := reconcile(tc.current, running)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMCPConfigEqual(t *testing.T) {
	t.Parallel()

	base := config.MCPConfig{
		Type:    config.MCPHttp,
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer tok"},
		Timeout: 30,
	}

	tests := []struct {
		name string
		a, b config.MCPConfig
		want bool
	}{
		{"identical", base, base, true},
		{"different URL", base, func() config.MCPConfig { m := base; m.URL = "https://other.com/mcp"; return m }(), false},
		{"different headers", base, func() config.MCPConfig {
			m := base
			m.Headers = map[string]string{"Authorization": "Bearer other"}
			return m
		}(), false},
		{"different timeout", base, func() config.MCPConfig { m := base; m.Timeout = 60; return m }(), false},
		{"different type", base, func() config.MCPConfig { m := base; m.Type = config.MCPStdio; return m }(), false},
		{
			"OAuthToken ignored",
			base,
			func() config.MCPConfig {
				m := base
				m.OAuthToken = &oauth.Token{AccessToken: "x"}
				return m
			}(),
			true,
		},
		{
			"both OAuthToken ignored",
			func() config.MCPConfig {
				m := base
				m.OAuthToken = &oauth.Token{AccessToken: "x"}
				return m
			}(),
			func() config.MCPConfig {
				m := base
				m.OAuthToken = &oauth.Token{AccessToken: "y"}
				return m
			}(),
			true,
		},
		{"disabled vs enabled", base, func() config.MCPConfig { m := base; m.Disabled = true; return m }(), false},
		{"oauth flag", base, func() config.MCPConfig { m := base; m.OAuth = true; return m }(), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, mcpConfigEqual(tc.a, tc.b))
		})
	}
}

// TestMCPConfigEqualExhaustive guards mcpConfigEqual against drift. It
// enumerates every field of config.MCPConfig via reflection and fails if a
// field is neither compared by mcpConfigEqual nor explicitly excluded here.
// Adding a field to MCPConfig now forces a conscious decision about whether
// it should trigger a server restart, rather than being silently ignored.
func TestMCPConfigEqualExhaustive(t *testing.T) {
	t.Parallel()

	// Fields intentionally excluded from the comparison.
	excluded := map[string]bool{
		"OAuthToken": true, // internally managed, refreshed out-of-band.
	}

	typ := reflect.TypeOf(config.MCPConfig{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if excluded[name] {
			continue
		}
		// Build two configs that differ only in this field and assert the
		// difference is detected.
		a := config.MCPConfig{}
		b := config.MCPConfig{}
		setDistinct(typ.Field(i).Type, reflect.ValueOf(&a).Elem().Field(i))
		require.False(t, mcpConfigEqual(a, b),
			"mcpConfigEqual ignores field %q; add it to the comparison or to the excluded set", name)
	}
}

// setDistinct assigns a non-zero value of the given type so two structs
// differ in exactly one field.
func setDistinct(typ reflect.Type, field reflect.Value) {
	switch typ.Kind() {
	case reflect.String:
		field.SetString("x")
	case reflect.Bool:
		field.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(1)
	case reflect.Slice:
		field.Set(reflect.MakeSlice(typ, 1, 1))
	case reflect.Map:
		m := reflect.MakeMap(typ)
		m.SetMapIndex(reflect.Zero(typ.Key()), reflect.Zero(typ.Elem()))
		field.Set(m)
	case reflect.Pointer:
		field.Set(reflect.New(typ.Elem()))
	default:
		panic("setDistinct: unhandled kind " + typ.Kind().String())
	}
}

// TestRemoveServer proves removeServer fully tears down a server: the live
// session is closed and removed, and unlike DisableSingle both the state and
// generation entries are deleted rather than left behind as StateDisabled.
func TestRemoveServer(t *testing.T) {
	t.Parallel()

	const name = "test-remove-server"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	states.Set(name, ClientInfo{Name: name, State: StateConnected})
	gens.Set(name, 2)

	removeServer(name)

	require.ErrorIs(t, sessCtx.Err(), context.Canceled, "removeServer must close the live session")
	_, ok := sessions.Get(name)
	require.False(t, ok)
	_, ok = states.Get(name)
	require.False(t, ok, "removeServer must delete the state entry entirely")
	_, ok = gens.Get(name)
	require.False(t, ok, "removeServer must delete the generation entry entirely")
}

// TestReconcileOnce_RemovesServerGoneFromConfig exercises reconcileOnce's
// reinitRemove branch: a server tracked in states but absent from the
// current config must be fully torn down via removeServer.
//
// Not parallel: reconcileOnce reconciles the whole package-global states
// map against cfg, so a concurrently-running test's own entry (absent from
// this test's cfg) would be torn down as if it were removed from config.
func TestReconcileOnce_RemovesServerGoneFromConfig(t *testing.T) {
	const name = "test-reconcile-once-remove"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	states.Set(name, ClientInfo{Name: name, State: StateConnected})

	cfg := config.NewTestStore(&config.Config{}) // no MCP servers configured

	reconcileOnce(context.Background(), cfg)

	require.ErrorIs(t, sessCtx.Err(), context.Canceled)
	_, ok := states.Get(name)
	require.False(t, ok, "a server removed from config must have its state entry deleted")
}

// TestReconcileOnce_DisablesServerMarkedDisabledInConfig exercises
// reconcileOnce's reinitDisable branch: a connected server whose config now
// sets Disabled must transition to StateDisabled via DisableSingle.
//
// Not parallel: reconcileOnce reconciles the whole package-global states
// map against cfg, so a concurrently-running test's own entry (absent from
// this test's cfg) would be torn down as if it were removed from config.
func TestReconcileOnce_DisablesServerMarkedDisabledInConfig(t *testing.T) {
	const name = "test-reconcile-once-disable"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
	})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	states.Set(name, ClientInfo{Name: name, State: StateConnected, Config: config.MCPConfig{Type: config.MCPStdio}})

	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{
		name: {Type: config.MCPStdio, Disabled: true},
	}})

	reconcileOnce(context.Background(), cfg)

	require.ErrorIs(t, sessCtx.Err(), context.Canceled, "disabling must close the live session")
	info, ok := states.Get(name)
	require.True(t, ok, "a disabled server keeps its state entry, unlike a removed one")
	require.Equal(t, StateDisabled, info.State)
}

// TestReinitialize_RemovesServerGoneFromConfig exercises the public entry
// point for the deterministic (non-goroutine-spawning) remove path, proving
// Reinitialize actually invokes reconcileOnce rather than only managing the
// single-flight bookkeeping. Not parallel: Reinitialize coordinates through
// process-global single-flight state shared by every caller.
func TestReinitialize_RemovesServerGoneFromConfig(t *testing.T) {
	const name = "test-reinitialize-remove"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	states.Set(name, ClientInfo{Name: name, State: StateConnected})

	cfg := config.NewTestStore(&config.Config{})

	Reinitialize(context.Background(), cfg)

	require.ErrorIs(t, sessCtx.Err(), context.Canceled)
	_, ok := states.Get(name)
	require.False(t, ok)
}

// TestBeginAuth_UnknownServer proves BeginAuth rejects a server that is not
// present in the configuration.
func TestBeginAuth_UnknownServer(t *testing.T) {
	cfg := config.NewTestStore(&config.Config{})
	_, _, err := BeginAuth(cfg, "missing")
	require.ErrorContains(t, err, "not found")
}

// TestBeginAuth_NonOAuth proves BeginAuth rejects a server that does not use
// OAuth over HTTP.
func TestBeginAuth_NonOAuth(t *testing.T) {
	cfg := config.NewTestStore(&config.Config{
		MCP: config.MCPs{
			"stdio": {Type: config.MCPStdio},
			"plain": {Type: config.MCPHttp, URL: "https://example.com/mcp"},
		},
	})
	for _, name := range []string{"stdio", "plain"} {
		_, _, err := BeginAuth(cfg, name)
		require.ErrorContains(t, err, "does not use OAuth", "name %q", name)
	}
}

// TestBeginAuth_Concurrent proves only one browser-suppressed flow per
// server may be in progress at a time; a second BeginAuth fails fast while
// the first is outstanding, and succeeds once the first has finished.
func TestBeginAuth_Concurrent(t *testing.T) {
	const name = "oauth-http"
	cfg := config.NewTestStore(&config.Config{
		MCP: config.MCPs{name: {Type: config.MCPHttp, URL: "https://example.com/mcp", OAuth: true}},
	})

	finish, cancel, err := BeginAuth(cfg, name)
	require.NoError(t, err)
	t.Cleanup(cancel)

	// A second flow for the same server must fail fast while the first is
	// still outstanding.
	_, _, err = BeginAuth(cfg, name)
	require.ErrorContains(t, err, "already has an authentication in progress")

	// Finishing the first flow frees the slot for the next caller. Cancel
	// the request context so finish returns promptly without dialing.
	ctx, cancelCtx := context.WithCancel(context.Background())
	cancelCtx()
	_ = finish(ctx)

	_, cancel2, err := BeginAuth(cfg, name)
	require.NoError(t, err)
	cancel2()
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level string
		want  slog.Level
	}{
		{"info", slog.LevelInfo},
		{"notice", slog.LevelInfo},
		{"warning", slog.LevelWarn},
		{"debug", slog.LevelDebug},
		{"unknown-level", slog.LevelDebug},
		{"", slog.LevelDebug},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, parseLevel(tt.level))
		})
	}
}

func TestState_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state State
		want  string
	}{
		{StateDisabled, "disabled"},
		{StateStarting, "starting"},
		{StateConnected, "connected"},
		{StateError, "error"},
		{StateNeedsAuth, "needs auth"},
		{State(999), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.state.String())
		})
	}
}

func TestGetStates(t *testing.T) {
	t.Parallel()

	const name = "test-getstates"
	t.Cleanup(func() { states.Del(name) })

	updateState(name, StateConnected, nil, nil, Counts{Tools: 2})

	all := GetStates()
	info, ok := all[name]
	require.True(t, ok)
	require.Equal(t, StateConnected, info.State)
	require.Equal(t, Counts{Tools: 2}, info.Counts)
}

// TestArmInitDisarmInit pins ArmInit/DisarmInit's effect on WaitForInit
// directly, rather than only through the initStarted/initMu internals other
// tests in this package poke at.
func TestArmInitDisarmInit(t *testing.T) {
	// Not parallel: mutates the package-global init gate.
	origDone := initDone
	initDone = make(chan struct{})
	t.Cleanup(func() { initDone = origDone })

	DisarmInit()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.NoError(t, WaitForInit(ctx), "disarmed WaitForInit must return immediately")

	ArmInit()
	t.Cleanup(DisarmInit)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	require.ErrorIs(t, WaitForInit(ctx2), context.DeadlineExceeded, "armed WaitForInit must block until initDone closes")

	DisarmInit()
	require.NoError(t, WaitForInit(context.Background()), "disarming again must unblock WaitForInit immediately")
}

func TestHasUsableToken(t *testing.T) {
	t.Parallel()

	require.False(t, hasUsableToken(nil))
	require.False(t, hasUsableToken(&oauth.Token{}))
	require.True(t, hasUsableToken(&oauth.Token{AccessToken: "tok"}))
}

func TestIsOAuthInitErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"interactive auth required", mcpoauth.ErrInteractiveAuthRequired, true},
		{"wrapped interactive auth required", fmt.Errorf("wrap: %w", mcpoauth.ErrInteractiveAuthRequired), true},
		{"retrieve error invalid_grant", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"retrieve error invalid_client", &oauth2.RetrieveError{ErrorCode: "invalid_client"}, true},
		{"retrieve error other code", &oauth2.RetrieveError{ErrorCode: "server_error"}, false},
		{"message contains invalid_grant", errors.New("token refresh failed: invalid_grant"), true},
		{"message contains invalid_client", errors.New("register failed: invalid_client"), true},
		{"message contains no token available", errors.New("no token available"), true},
		{"unrelated error", errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isOAuthInitErr(tt.err))
		})
	}
}

func TestMcpTimeout(t *testing.T) {
	t.Parallel()

	require.Equal(t, 10*time.Second, mcpTimeout(config.MCPConfig{}), "default with no OAuth is 10s")
	require.Equal(t, 30*time.Second, mcpTimeout(config.MCPConfig{OAuth: true}), "default with OAuth is 30s")
	require.Equal(t, 5*time.Second, mcpTimeout(config.MCPConfig{Timeout: 5}), "explicit timeout wins")
	require.Equal(t, 5*time.Second, mcpTimeout(config.MCPConfig{Timeout: 5, OAuth: true}), "explicit timeout wins over OAuth default")
}

func TestHeaderRoundTripper(t *testing.T) {
	t.Parallel()

	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	rt := headerRoundTripper{headers: map[string]string{"X-Test": "value", "Authorization": "Bearer tok"}}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := (&http.Client{Transport: rt}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "value", gotHeaders.Get("X-Test"))
	require.Equal(t, "Bearer tok", gotHeaders.Get("Authorization"))
}

// countingHandler returns 401 for the first n requests, then 200. It lets
// oauthRoundTripper tests exercise the retry-after-Authorize path over a
// real local HTTP server instead of a hand-rolled RoundTripper.
func countingHandler(unauthorizedCount int) (http.HandlerFunc, *int) {
	seen := 0
	return func(w http.ResponseWriter, r *http.Request) {
		seen++
		if seen <= unauthorizedCount {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}, &seen
}

func TestOAuthRoundTripper(t *testing.T) {
	t.Parallel()

	t.Run("valid token attaches Authorization header", func(t *testing.T) {
		t.Parallel()
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		ctrl := gomock.NewController(t)
		h := NewMockOAuthHandler(ctrl)
		h.EXPECT().TokenSource(gomock.Any()).Return(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "secret-tok"}), nil)

		rt := newOAuthRoundTripper(h, http.DefaultTransport)
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "Bearer secret-tok", gotAuth)
	})

	t.Run("nil token source sends request without Authorization header", func(t *testing.T) {
		t.Parallel()
		var gotAuth string
		var sawHeader bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth, sawHeader = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		ctrl := gomock.NewController(t)
		h := NewMockOAuthHandler(ctrl)
		h.EXPECT().TokenSource(gomock.Any()).Return(nil, nil)

		rt := newOAuthRoundTripper(h, http.DefaultTransport)
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.False(t, sawHeader, "no Authorization header should be set, got %q", gotAuth)
	})

	t.Run("token source error aborts before the request is sent", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		h := NewMockOAuthHandler(ctrl)
		h.EXPECT().TokenSource(gomock.Any()).Return(nil, errors.New("token source boom"))

		base := roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("base transport must not be called when TokenSource errors")
			return nil, nil
		})
		rt := newOAuthRoundTripper(h, base)
		req, err := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "oauth token source")
	})

	t.Run("401 then failed Authorize returns the original response", func(t *testing.T) {
		t.Parallel()
		handler, seen := countingHandler(100) // always 401
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		ctrl := gomock.NewController(t)
		h := NewMockOAuthHandler(ctrl)
		h.EXPECT().TokenSource(gomock.Any()).Return(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), nil).Times(1)
		h.EXPECT().Authorize(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("authorize failed"))

		rt := newOAuthRoundTripper(h, http.DefaultTransport)
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err, "a failed Authorize is swallowed; the original response is returned")
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.Equal(t, 1, *seen, "no retry should be attempted when Authorize fails")
	})

	t.Run("401 then successful Authorize retries and succeeds", func(t *testing.T) {
		t.Parallel()
		handler, seen := countingHandler(1) // first call 401, second 200
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		ctrl := gomock.NewController(t)
		h := NewMockOAuthHandler(ctrl)
		h.EXPECT().TokenSource(gomock.Any()).Return(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}), nil).Times(2)
		h.EXPECT().Authorize(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		rt := newOAuthRoundTripper(h, http.DefaultTransport)
		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, 2, *seen, "the request must be retried exactly once after a successful Authorize")
	})
}

// roundTripFunc adapts a function to http.RoundTripper, mirroring the
// standard library's http.HandlerFunc pattern.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreateTransport_UnsupportedType(t *testing.T) {
	t.Parallel()

	tr, oh, err := createTransport(t.Context(), nil, "test", config.MCPConfig{Type: "bogus"}, config.IdentityResolver())
	require.Error(t, err)
	require.Nil(t, tr)
	require.Nil(t, oh)
	require.Contains(t, err.Error(), "unsupported mcp type")
}

// TestStdioCheck uses true/false rather than a command like `go version`:
// stdioCheck re-execs via old.Path plus old.Args, and old.Args conventionally
// includes argv[0] as its own first element, so the recheck always runs with
// one duplicated leading argument. true/false ignore all arguments and exit
// deterministically either way; a subcommand-sensitive binary would not.
func TestStdioCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true binary not found on PATH")
	}

	t.Run("success returns nil", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, stdioCheck(exec.Command("true")))
	})

	t.Run("failure returns error with captured output", func(t *testing.T) {
		t.Parallel()
		err := stdioCheck(exec.Command("false"))
		require.Error(t, err)
	})
}

func TestMaybeStdioErr(t *testing.T) {
	t.Parallel()

	t.Run("non-EOF error returned unchanged", func(t *testing.T) {
		t.Parallel()
		orig := errors.New("boom")
		got := maybeStdioErr(orig, &mcp.CommandTransport{Command: exec.Command("true")})
		require.Equal(t, orig, got)
	})

	t.Run("EOF with non-command transport returned unchanged", func(t *testing.T) {
		t.Parallel()
		got := maybeStdioErr(io.EOF, &mcp.StreamableClientTransport{Endpoint: "http://example.com"})
		require.ErrorIs(t, got, io.EOF)
		require.Equal(t, io.EOF, got)
	})

	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true/false binaries not found on PATH")
	}

	t.Run("EOF with command transport that now succeeds stays plain EOF", func(t *testing.T) {
		t.Parallel()
		got := maybeStdioErr(io.EOF, &mcp.CommandTransport{Command: exec.Command("true")})
		require.Equal(t, io.EOF, got, "a successful recheck must not join extra output onto the error")
	})

	t.Run("EOF with command transport that still fails is joined with recheck output", func(t *testing.T) {
		t.Parallel()
		got := maybeStdioErr(io.EOF, &mcp.CommandTransport{Command: exec.Command("false")})
		require.ErrorIs(t, got, io.EOF)
		require.NotEqual(t, io.EOF.Error(), got.Error(), "a failing recheck must join its output onto the error")
	})
}

func TestMCPAuthURL_NoHandler(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", MCPAuthURL("test-mcpauthurl-does-not-exist"))
}

// TestPendingAuthMCPs is not parallel: it asserts an exact slice length over
// the whole package-global states map, which would be flaky if another test
// concurrently registered a StateNeedsAuth entry of its own.
func TestPendingAuthMCPs(t *testing.T) {
	const nameA = "test-pending-a"
	const nameB = "test-pending-b"
	t.Cleanup(func() {
		states.Del(nameA)
		states.Del(nameB)
	})

	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{
		nameA: {Type: config.MCPHttp, URL: "https://a.example.com/mcp"},
		nameB: {Type: config.MCPHttp, URL: "https://b.example.com/mcp"},
	}})
	updateState(nameB, StateNeedsAuth, nil, nil, Counts{})
	updateState(nameA, StateNeedsAuth, nil, nil, Counts{})

	got := PendingAuthMCPs(cfg)
	require.Equal(t, []PendingAuthServer{
		{Name: nameA, URL: "https://a.example.com/mcp"},
		{Name: nameB, URL: "https://b.example.com/mcp"},
	}, got, "results must be sorted by name regardless of registration order")
}

func TestAuthenticateMCP_GuardClauses(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{
		MCP: config.MCPs{
			"stdio": {Type: config.MCPStdio},
			"plain": {Type: config.MCPHttp, URL: "https://example.com/mcp"},
		},
	})

	err := AuthenticateMCP(context.Background(), cfg, "missing")
	require.ErrorContains(t, err, "not found")

	for _, name := range []string{"stdio", "plain"} {
		err := AuthenticateMCP(context.Background(), cfg, name)
		require.ErrorContains(t, err, "does not use OAuth", "name %q", name)
	}
}

// TestClose_ClosesSessionsAndShutsDownBroker swaps the package-global broker
// for a private one so shutting it down (a documented, one-way effect of
// Close) does not break every other test in this package that subscribes to
// the real broker.
func TestClose_ClosesSessionsAndShutsDownBroker(t *testing.T) {
	small := pubsub.NewBroker[Event]()
	prevBroker := broker
	broker = small
	t.Cleanup(func() { broker = prevBroker })

	const name = "test-close-session"
	sess, sessCtx := liveSession(t, "do_thing")
	sessions.Set(name, sess)
	t.Cleanup(func() { sessions.Del(name) })

	require.NoError(t, sessCtx.Err())
	require.NoError(t, Close(context.Background()))
	require.ErrorIs(t, sessCtx.Err(), context.Canceled, "Close must actually close registered sessions, not just drop them")
}

// TestMCPAuthURL_HandlerPresent covers the branch TestMCPAuthURL_NoHandler
// does not: a server with a registered (but not yet resolved) auth handler
// reports whatever AuthURL the handler currently holds, here empty since no
// authorization flow has produced a URL yet.
func TestMCPAuthURL_HandlerPresent(t *testing.T) {
	t.Parallel()

	const name = "test-mcpauthurl-handler-present"
	handler, err := mcpoauth.NewHandler(name, "https://example.com/mcp", nil, nil, func(*oauth.Token) {}, false, 0)
	require.NoError(t, err)
	t.Cleanup(func() {
		handler.Close()
		authURLs.Del(name)
	})
	authURLs.Set(name, handler)

	require.Equal(t, "", MCPAuthURL(name))
}

// TestClearMCPData proves clearMCPData drops every registry entry for a
// server, including closing and removing its pending OAuth handler, so a
// torn-down server stops advertising capabilities it can no longer serve.
func TestClearMCPData(t *testing.T) {
	t.Parallel()

	const name = "test-clear-mcp-data"
	t.Cleanup(func() {
		allTools.Del(name)
		allPrompts.Del(name)
		allResources.Del(name)
		authURLs.Del(name)
	})

	allTools.Set(name, []*Tool{{Name: "t"}})
	allPrompts.Set(name, []*Prompt{{Name: "p"}})
	allResources.Set(name, []*Resource{{Name: "r", URI: "res://r"}})

	handler, err := mcpoauth.NewHandler(name, "https://example.com/mcp", nil, nil, func(*oauth.Token) {}, false, 0)
	require.NoError(t, err)
	authURLs.Set(name, handler)

	clearMCPData(name)

	_, ok := allTools.Get(name)
	require.False(t, ok, "tools must be cleared")
	_, ok = allPrompts.Get(name)
	require.False(t, ok, "prompts must be cleared")
	_, ok = allResources.Get(name)
	require.False(t, ok, "resources must be cleared")
	_, ok = authURLs.Get(name)
	require.False(t, ok, "the pending OAuth handler must be closed and removed")
}

// TestTeardown covers both of teardown's paths: closing and removing a live
// session when one is registered, and being a no-op on the session map
// (while still clearing registry data and bumping the generation) when none
// is.
func TestTeardown(t *testing.T) {
	t.Parallel()

	t.Run("closes session, clears data, bumps generation", func(t *testing.T) {
		t.Parallel()

		const name = "test-teardown-with-session"
		t.Cleanup(func() {
			gens.Del(name)
			allTools.Del(name)
		})

		sess, sessCtx := liveSession(t, "send_message")
		sessions.Set(name, sess)
		allTools.Set(name, []*Tool{{Name: "send_message"}})
		gens.Set(name, 5)

		teardown(name)

		_, ok := sessions.Get(name)
		require.False(t, ok, "session must be removed")
		require.ErrorIs(t, sessCtx.Err(), context.Canceled, "session must be closed")
		_, ok = allTools.Get(name)
		require.False(t, ok, "tools must be cleared")
		gen, ok := gens.Get(name)
		require.True(t, ok)
		require.Equal(t, uint64(6), gen, "generation must be bumped")
	})

	t.Run("without a session still clears data and bumps generation", func(t *testing.T) {
		t.Parallel()

		const name = "test-teardown-no-session"
		t.Cleanup(func() { gens.Del(name) })

		allPrompts.Set(name, []*Prompt{{Name: "p"}})

		teardown(name)

		gen, ok := gens.Get(name)
		require.True(t, ok)
		require.Equal(t, uint64(1), gen)
		_, ok = allPrompts.Get(name)
		require.False(t, ok)
	})
}

// TestDisableSingle proves DisableSingle both tears down the live session
// (via teardown) and records the server as StateDisabled so a later
// reconcile with an unchanged config still restarts it.
func TestDisableSingle(t *testing.T) {
	t.Parallel()

	const name = "test-disable-single"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
	})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	allTools.Set(name, []*Tool{{Name: "send_message"}})
	states.Set(name, ClientInfo{Name: name, State: StateConnected})

	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})
	require.NoError(t, DisableSingle(cfg, name))

	require.ErrorIs(t, sessCtx.Err(), context.Canceled, "disabling must close the live session")
	_, ok := sessions.Get(name)
	require.False(t, ok)

	info, ok := GetState(name)
	require.True(t, ok)
	require.Equal(t, StateDisabled, info.State)
}

// TestInitialize_DisabledServerSkipsConnectionAttempt exercises Initialize's
// arm/dispatch/close bookkeeping without spawning a real connection: a
// disabled server takes the updateState(StateDisabled) branch instead of
// goInitClient, so the whole run completes synchronously. Not parallel: it
// swaps the process-wide, one-shot initOnce/initDone pair that Initialize and
// WaitForInit's tests also depend on.
func TestInitialize_DisabledServerSkipsConnectionAttempt(t *testing.T) {
	origDone := initDone
	initDone = make(chan struct{})
	// A fresh zero value, not a copy of the existing initOnce: Initialize's
	// initOnce.Do must fire again for this test's swapped initDone rather
	// than being a no-op left over from an earlier call.
	initOnce = sync.Once{}
	t.Cleanup(func() { initDone = origDone })
	t.Cleanup(DisarmInit)

	const name = "test-initialize-disabled"
	t.Cleanup(func() { states.Del(name) })

	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{
		name: {Type: config.MCPStdio, Disabled: true},
	}})

	Initialize(context.Background(), cfg)

	info, ok := GetState(name)
	require.True(t, ok)
	require.Equal(t, StateDisabled, info.State)

	select {
	case <-initDone:
	default:
		t.Fatal("Initialize must close initDone once every configured server has been dispatched")
	}
}

// TestInitializeSingle covers InitializeSingle's two guard branches that
// don't require a real connection: an unknown server errors, and a disabled
// one records StateDisabled and returns without attempting to connect.
func TestInitializeSingle(t *testing.T) {
	t.Run("unknown server", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestStore(&config.Config{})
		err := InitializeSingle(context.Background(), "missing", cfg)
		require.ErrorContains(t, err, "not found")
	})

	t.Run("disabled server records state without connecting", func(t *testing.T) {
		t.Parallel()
		const name = "test-init-single-disabled"
		t.Cleanup(func() { states.Del(name) })
		cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio, Disabled: true}}})

		require.NoError(t, InitializeSingle(context.Background(), name, cfg))

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateDisabled, info.State)
	})
}
