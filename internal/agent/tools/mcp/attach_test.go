package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/config"
)

// requireSettled fails unless ch closes promptly.
func requireSettled(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("attempt did not settle")
	}
}

// requirePending fails if ch is already closed.
func requirePending(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal(msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func cleanupServer(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
		attemptsMu.Lock()
		delete(attempts, name)
		attemptsMu.Unlock()
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})
}

// TestCreateTransport_RuntimeMCPIsLiteral pins that a server an editor
// passed is never shell-expanded, even though the caller hands in the full
// shell resolver. A config-file server of the same shape is expanded.
func TestCreateTransport_RuntimeMCPIsLiteral(t *testing.T) {
	t.Parallel()

	shell := shellResolverWithPath(t, nil)
	const arg = "$(echo expanded)"

	t.Run("stdio arguments stay literal", func(t *testing.T) {
		t.Parallel()
		store := config.NewTestStore(&config.Config{})
		m := config.MCPConfig{Type: config.MCPStdio, Command: "server", Args: []string{arg}}
		store.AddRuntimeMCP(map[string]config.MCPConfig{"editor-stdio": m})

		tr, _, err := createTransport(t.Context(), store, "editor-stdio", m, shell)
		require.NoError(t, err)
		ct, ok := tr.(*mcp.CommandTransport)
		require.True(t, ok, "expected CommandTransport, got %T", tr)
		require.Equal(t, []string{"server", arg}, ct.Command.Args)
	})

	t.Run("a command that would fail to expand is not run", func(t *testing.T) {
		t.Parallel()
		store := config.NewTestStore(&config.Config{})
		m := config.MCPConfig{Type: config.MCPHttp, URL: "https://example.com/$(false)"}
		store.AddRuntimeMCP(map[string]config.MCPConfig{"editor-http": m})

		tr, _, err := createTransport(t.Context(), store, "editor-http", m, shell)
		require.NoError(t, err, "the literal URL is used as sent")
		sct, ok := tr.(*mcp.StreamableClientTransport)
		require.True(t, ok, "expected StreamableClientTransport, got %T", tr)
		require.Equal(t, "https://example.com/$(false)", sct.Endpoint)
	})

	t.Run("the same server from a config file is expanded", func(t *testing.T) {
		t.Parallel()
		m := config.MCPConfig{Type: config.MCPStdio, Command: "server", Args: []string{arg}}
		store := config.NewTestStore(&config.Config{MCP: config.MCPs{"from-file": m}})

		tr, _, err := createTransport(t.Context(), store, "from-file", m, shell)
		require.NoError(t, err)
		ct, ok := tr.(*mcp.CommandTransport)
		require.True(t, ok, "expected CommandTransport, got %T", tr)
		require.Equal(t, []string{"server", "expanded"}, ct.Command.Args)
	})
}

// Not parallel: Attach reads and writes the package-global state maps.
func TestAttach_ReturnsAtOnceAndSettlesWhenTheAttemptFails(t *testing.T) {
	const name = "test-attach-fails"
	cleanupServer(t, name)

	store := config.NewTestStore(&config.Config{})
	added := store.AddRuntimeMCP(map[string]config.MCPConfig{
		name: {Type: config.MCPStdio, Command: "angela-test-no-such-mcp-binary"},
	})
	require.Equal(t, []string{name}, added)

	settled := Attach(t.Context(), store, added)
	requireSettled(t, settled)

	info, ok := states.Get(name)
	require.True(t, ok)
	require.Equal(t, StateError, info.State)
	require.Error(t, info.Error)
}

// Not parallel: see above.
func TestAttach_WaitsForAnAttemptAlreadyInFlight(t *testing.T) {
	const name = "test-attach-in-flight"
	cleanupServer(t, name)

	m := config.MCPConfig{Type: config.MCPStdio, Command: "angela-test-no-such-mcp-binary"}
	store := config.NewTestStore(&config.Config{})
	added := store.AddRuntimeMCP(map[string]config.MCPConfig{name: m})

	// NOTE: This is the state the first of two back-to-back session
	// requests leaves behind: connecting with the same config. The second
	// must not restart it, and must still wait for it.
	states.Set(name, ClientInfo{Name: name, State: StateStarting, PendingConfig: &m})
	settle := trackAttempt(name)

	settled := Attach(t.Context(), store, added)
	requirePending(t, settled, "Attach must wait for the attempt that is already running")

	info, _ := states.Get(name)
	require.Equal(t, StateStarting, info.State, "the running attempt must not be restarted")

	settle()
	requireSettled(t, settled)
}

// Not parallel: see above.
func TestAttach_StopsWaitingWhenItsContextEnds(t *testing.T) {
	const name = "test-attach-ctx"
	cleanupServer(t, name)

	m := config.MCPConfig{Type: config.MCPStdio, Command: "angela-test-no-such-mcp-binary"}
	store := config.NewTestStore(&config.Config{})
	added := store.AddRuntimeMCP(map[string]config.MCPConfig{name: m})
	states.Set(name, ClientInfo{Name: name, State: StateStarting, PendingConfig: &m})
	settle := trackAttempt(name)
	t.Cleanup(settle)

	ctx, cancel := context.WithCancel(t.Context())
	settled := Attach(ctx, store, added)
	requirePending(t, settled, "the attempt is still running")

	cancel()
	requireSettled(t, settled)
}

// Not parallel: see above.
func TestAttach_LeavesConnectedServerWithSameConfigAlone(t *testing.T) {
	const name = "test-attach-unchanged"
	cleanupServer(t, name)

	m := config.MCPConfig{Type: config.MCPStdio, Command: "angela-test-no-such-mcp-binary"}
	store := config.NewTestStore(&config.Config{})
	added := store.AddRuntimeMCP(map[string]config.MCPConfig{name: m})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	states.Set(name, ClientInfo{Name: name, State: StateConnected, Config: m})

	requireSettled(t, Attach(t.Context(), store, added))

	require.NoError(t, sessCtx.Err(), "a second editor session must not restart a healthy server")
	info, ok := states.Get(name)
	require.True(t, ok)
	require.Equal(t, StateConnected, info.State)
}

// Not parallel: see above.
func TestAttach_IgnoresServersOutsideTheNames(t *testing.T) {
	const other = "test-attach-bystander"
	cleanupServer(t, other)

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(other, sess)
	states.Set(other, ClientInfo{Name: other, State: StateConnected})

	// NOTE: The store does not list the bystander. A full reconcile would
	// remove it as gone from config; Attach must touch only what it was
	// asked to connect.
	store := config.NewTestStore(&config.Config{})
	requireSettled(t, Attach(t.Context(), store, nil))

	require.NoError(t, sessCtx.Err())
	_, ok := states.Get(other)
	require.True(t, ok)
}

// Not parallel: see above.
func TestRunningWithDifferentConfig(t *testing.T) {
	const name = "test-running-different"
	cleanupServer(t, name)

	a := config.MCPConfig{Type: config.MCPHttp, URL: "https://one.example"}
	b := config.MCPConfig{Type: config.MCPHttp, URL: "https://two.example"}

	require.False(t, RunningWithDifferentConfig(name, a), "an unknown server is free to start")

	states.Set(name, ClientInfo{Name: name, State: StateConnected, Config: a})
	require.False(t, RunningWithDifferentConfig(name, a))
	require.True(t, RunningWithDifferentConfig(name, b))

	states.Set(name, ClientInfo{Name: name, State: StateStarting, PendingConfig: &a})
	require.False(t, RunningWithDifferentConfig(name, a))
	require.True(t, RunningWithDifferentConfig(name, b))

	states.Set(name, ClientInfo{Name: name, State: StateError})
	require.False(t, RunningWithDifferentConfig(name, b), "a failed server holds nothing worth keeping")
}

func TestListBounded(t *testing.T) {
	t.Parallel()

	t.Run("returns what the call returns", func(t *testing.T) {
		t.Parallel()
		got, err := listBounded(t.Context(), time.Second, "tools", func(context.Context) (int, error) { return 7, nil })
		require.NoError(t, err)
		require.Equal(t, 7, got)
	})

	t.Run("a call that never answers fails at the timeout", func(t *testing.T) {
		t.Parallel()
		start := time.Now()
		_, err := listBounded(t.Context(), 50*time.Millisecond, "tools", func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		})
		require.ErrorContains(t, err, "listing tools timed out")
		require.Less(t, time.Since(start), 2*time.Second)
	})

	t.Run("a cancelled parent keeps its own error", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := listBounded(ctx, time.Second, "tools", func(ctx context.Context) (int, error) {
			return 0, ctx.Err()
		})
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a server that stalls tools/list is cut off", func(t *testing.T) {
		t.Parallel()
		release := make(chan struct{})
		sess := liveSessionIntercept(t, "send_message", "p", "file:///r", func(ctx context.Context, method string) error {
			if method == "tools/list" {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil
		})
		t.Cleanup(func() { close(release) })

		_, err := listBounded(t.Context(), 100*time.Millisecond, "tools", func(ctx context.Context) ([]*Tool, error) {
			return getTools(ctx, sess)
		})
		require.ErrorContains(t, err, "listing tools timed out")
	})
}
