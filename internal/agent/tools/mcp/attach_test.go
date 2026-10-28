package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/config"
)

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
func TestAttach_WaitsForAttemptsAndRecordsFailure(t *testing.T) {
	const name = "test-attach-fails"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	store := config.NewTestStore(&config.Config{})
	added := store.AddRuntimeMCP(map[string]config.MCPConfig{
		name: {Type: config.MCPStdio, Command: "angela-test-no-such-mcp-binary"},
	})
	require.Equal(t, []string{name}, added)

	Attach(t.Context(), store, added)

	info, ok := states.Get(name)
	require.True(t, ok, "Attach must not return before the attempt has settled")
	require.Equal(t, StateError, info.State)
	require.Error(t, info.Error)
}

// Not parallel: see above.
func TestAttach_LeavesConnectedServerWithSameConfigAlone(t *testing.T) {
	const name = "test-attach-unchanged"
	t.Cleanup(func() {
		states.Del(name)
		gens.Del(name)
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	m := config.MCPConfig{Type: config.MCPStdio, Command: "angela-test-no-such-mcp-binary"}
	store := config.NewTestStore(&config.Config{})
	added := store.AddRuntimeMCP(map[string]config.MCPConfig{name: m})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(name, sess)
	states.Set(name, ClientInfo{Name: name, State: StateConnected, Config: m})

	Attach(t.Context(), store, added)

	require.NoError(t, sessCtx.Err(), "a second editor session must not restart a healthy server")
	info, ok := states.Get(name)
	require.True(t, ok)
	require.Equal(t, StateConnected, info.State)
}

// Not parallel: see above.
func TestAttach_IgnoresServersOutsideTheNames(t *testing.T) {
	const other = "test-attach-bystander"
	t.Cleanup(func() {
		states.Del(other)
		gens.Del(other)
		if s, ok := sessions.Take(other); ok {
			_ = s.Close()
		}
	})

	sess, sessCtx := liveSession(t, "send_message")
	sessions.Set(other, sess)
	states.Set(other, ClientInfo{Name: other, State: StateConnected})

	// NOTE: The store does not list the bystander. A full reconcile would
	// remove it as gone from config; Attach must touch only what it was
	// asked to connect.
	store := config.NewTestStore(&config.Config{})
	Attach(t.Context(), store, nil)

	require.NoError(t, sessCtx.Err())
	_, ok := states.Get(other)
	require.True(t, ok)
}
