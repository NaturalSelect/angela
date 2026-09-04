package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/env"
	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	// Create a simple config for testing
	cfg := config.LSPConfig{
		Command:   "$THE_CMD", // Use echo as a dummy command that won't fail
		Args:      []string{"hello"},
		FileTypes: []string{"go"},
		Env:       map[string]string{},
	}

	// Test creating a powernap client - this will likely fail with echo
	// but we can still test the basic structure
	client, err := New("test", cfg, config.NewShellVariableResolver(env.NewFromMap(map[string]string{
		"THE_CMD": "echo",
	})), ".", false)
	if err != nil {
		// Expected to fail with echo command, skip the rest
		t.Skipf("Powernap client creation failed as expected with dummy command: %v", err)
		return
	}

	// If we get here, test basic interface methods
	if client.GetName() != "test" {
		t.Errorf("Expected name 'test', got '%s'", client.GetName())
	}

	if !client.HandlesFile("test.go") {
		t.Error("Expected client to handle .go files")
	}

	if client.HandlesFile("test.py") {
		t.Error("Expected client to not handle .py files")
	}

	// Test server state
	client.SetServerState(StateReady)
	if client.GetServerState() != StateReady {
		t.Error("Expected server state to be StateReady")
	}

	// Clean up - expect this to fail with echo command
	if err := client.Close(t.Context()); err != nil {
		// Expected to fail with echo command
		t.Logf("Close failed as expected with dummy command: %v", err)
	}
}

// TestNew_ExpansionFailure_Args pins that a failing $(cmd) in LSP
// args surfaces as a load error prefixed "invalid lsp args:" and that
// no client is returned. Mirrors the MCP contract where expansion
// failure hard-stops transport creation rather than silently running
// with an empty or literal value.
func TestNew_ExpansionFailure_Args(t *testing.T) {
	t.Parallel()

	cfg := config.LSPConfig{
		Command: "echo",
		Args:    []string{"--root", "$(false)"},
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))

	client, err := New("test-args-fail", cfg, resolver, ".", false)
	require.Error(t, err)
	require.Nil(t, client, "client must not start when args expansion fails")
	require.Contains(t, err.Error(), "invalid lsp args")
}

// TestNew_ExpansionFailure_Env pins the same contract for env values.
func TestNew_ExpansionFailure_Env(t *testing.T) {
	t.Parallel()

	cfg := config.LSPConfig{
		Command: "echo",
		Env:     map[string]string{"BAD": "$(false)"},
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))

	client, err := New("test-env-fail", cfg, resolver, ".", false)
	require.Error(t, err)
	require.Nil(t, client, "client must not start when env expansion fails")
	require.Contains(t, err.Error(), "invalid lsp env")
}

func TestNilClient(t *testing.T) {
	t.Parallel()

	var c *Client

	require.False(t, c.HandlesFile("/some/file.go"))
	require.Equal(t, DiagnosticCounts{}, c.GetDiagnosticCounts())
	require.Nil(t, c.GetDiagnostics())
	require.Nil(t, c.OpenFileOnDemand(context.Background(), "/some/file.go"))
	require.Nil(t, c.NotifyChange(context.Background(), "/some/file.go"))
	require.Nil(t, c.NotifyWorkspaceChange(context.Background()))
	c.RefreshOpenFiles(context.Background())
	c.WaitForDiagnostics(context.Background(), time.Second)
}

func newTestClient() *Client {
	c := &Client{
		name:        "test",
		diagnostics: csync.NewVersionedMap[protocol.DocumentURI, []protocol.Diagnostic](),
		openFiles:   csync.NewMap[string, *OpenFileInfo](),
	}
	c.serverState.Store(StateStopped)
	return c
}

func TestWaitForDiagnostics_NoChange(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	start := time.Now()
	c.WaitForDiagnostics(t.Context(), 5*time.Second)
	elapsed := time.Since(start)

	// Should return early via firstChangeDeadline (~1s), not the full timeout.
	require.Less(t, elapsed, 2*time.Second, "should return early when no diagnostics change")
}

func TestWaitForDiagnostics_ImmediateChange(t *testing.T) {
	t.Parallel()

	c := newTestClient()

	go func() {
		time.Sleep(100 * time.Millisecond)
		c.diagnostics.Set(protocol.DocumentURI("file:///test.go"), nil)
	}()

	start := time.Now()
	c.WaitForDiagnostics(t.Context(), 5*time.Second)
	elapsed := time.Since(start)

	// Should detect the change and then settle (~300ms settle + overhead).
	require.Less(t, elapsed, 2*time.Second, "should return after settling, not full timeout")
	require.Greater(t, elapsed, 200*time.Millisecond, "should wait for settle duration")
}

func TestWaitForDiagnostics_RepeatedChanges(t *testing.T) {
	t.Parallel()

	c := newTestClient()

	// Simulate an LSP server that publishes diagnostics in bursts.
	go func() {
		for i := range 5 {
			time.Sleep(50 * time.Millisecond)
			c.diagnostics.Set(protocol.DocumentURI("file:///test.go"), []protocol.Diagnostic{
				{Message: fmt.Sprintf("diag-%d", i)},
			})
		}
	}()

	start := time.Now()
	c.WaitForDiagnostics(t.Context(), 5*time.Second)
	elapsed := time.Since(start)

	// Should wait for diagnostics to settle after the burst finishes.
	// Burst lasts ~250ms, then 300ms settle window, so total ~550ms+.
	require.Less(t, elapsed, 2*time.Second, "should return after settling, not full timeout")
	require.Greater(t, elapsed, 400*time.Millisecond, "should wait for all changes to settle")
}

func TestWaitForDiagnostics_ContextCancellation(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	c.WaitForDiagnostics(ctx, 5*time.Second)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 1*time.Second, "should return shortly after context cancellation")
}

func TestWaitForDiagnostics_NilClient(t *testing.T) {
	t.Parallel()

	var c *Client
	// Should not panic.
	c.WaitForDiagnostics(context.Background(), time.Second)
}

// newRealClient builds a *Client wrapping a real powernap client backed by
// the "echo" process. The process exits almost immediately and the LSP
// handshake is never performed, so c.client.initialized stays false: every
// Notify* method on the underlying powernap client deterministically
// returns a "client not initialized" error without touching the network or
// blocking on process I/O. This lets tests exercise the real notify/open
// code paths in Client without depending on a genuine language server.
func newRealClient(t *testing.T, cwd string, fileTypes []string) *Client {
	t.Helper()
	cfg := config.LSPConfig{
		Command:   "echo",
		Args:      []string{"hi"},
		FileTypes: fileTypes,
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))
	c, err := New("real-test", cfg, resolver, cwd, false)
	require.NoError(t, err)
	t.Cleanup(c.Kill)
	return c
}

func TestNew_ExpansionFailure_Command(t *testing.T) {
	t.Parallel()

	cfg := config.LSPConfig{
		Command: "$(false)",
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))

	client, err := New("test-command-fail", cfg, resolver, ".", false)
	require.Error(t, err)
	require.Nil(t, client, "client must not start when command expansion fails")
	require.Contains(t, err.Error(), "invalid lsp command")
}

func TestNew_CommandNotFound(t *testing.T) {
	t.Parallel()

	cfg := config.LSPConfig{
		Command: "definitely-not-a-real-lsp-binary-xyz",
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))

	client, err := New("test-not-found", cfg, resolver, ".", false)
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "failed to create lsp client")
}

func TestClient_Initialize_FailsFastWithNonLSPProcess(t *testing.T) {
	t.Parallel()

	c := newRealClient(t, t.TempDir(), []string{"go"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := c.Initialize(ctx, t.TempDir())
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "failed to initialize the lsp client")
}

func TestClient_GetOffsetEncoding_Default(t *testing.T) {
	t.Parallel()

	c := newRealClient(t, t.TempDir(), nil)
	require.Equal(t, powernap.UTF16, c.GetOffsetEncoding())
}

func TestClient_ServerStateTransitions(t *testing.T) {
	t.Parallel()

	states := []ServerState{StateUnstarted, StateStarting, StateReady, StateError, StateStopped, StateDisabled}
	for _, state := range states {
		c := newTestClient()
		c.SetServerState(state)
		require.Equal(t, state, c.GetServerState())
	}
}

func TestClient_ServerState_ZeroValueDefaultsToStarting(t *testing.T) {
	t.Parallel()

	c := &Client{}
	require.Equal(t, StateStarting, c.GetServerState())
}

func TestClient_GetName(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	require.Equal(t, "test", c.GetName())
}

func TestClient_FileTypes_Clone(t *testing.T) {
	t.Parallel()

	c := &Client{fileTypes: []string{"go", "mod"}}
	got := c.FileTypes()
	require.Equal(t, []string{"go", "mod"}, got)

	got[0] = "mutated"
	require.Equal(t, []string{"go", "mod"}, c.fileTypes, "FileTypes must return a clone, not the backing slice")
}

func TestHandlesFile_TableDriven(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tests := []struct {
		name      string
		cwd       string
		fileTypes []string
		path      string
		want      bool
	}{
		{
			name:      "matching extension within cwd",
			cwd:       dir,
			fileTypes: []string{"go"},
			path:      filepath.Join(dir, "main.go"),
			want:      true,
		},
		{
			name:      "non-matching extension within cwd",
			cwd:       dir,
			fileTypes: []string{"go"},
			path:      filepath.Join(dir, "main.py"),
			want:      false,
		},
		{
			name:      "outside cwd",
			cwd:       dir,
			fileTypes: []string{"go"},
			path:      filepath.Join(t.TempDir(), "main.go"),
			want:      false,
		},
		{
			name:      "empty file types handles everything within cwd",
			cwd:       dir,
			fileTypes: nil,
			path:      filepath.Join(dir, "whatever.xyz"),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &Client{name: "test", cwd: tt.cwd, fileTypes: tt.fileTypes}
			require.Equal(t, tt.want, c.HandlesFile(tt.path))
		})
	}
}

func TestClient_IsFileOpen(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	file := "/tmp/whatever.go"
	require.False(t, c.IsFileOpen(file))

	uri := string(protocol.URIFromPath(file))
	c.openFiles.Set(uri, &OpenFileInfo{Version: 1, URI: protocol.DocumentURI(uri)})
	require.True(t, c.IsFileOpen(file))
}

func TestClient_DiagnosticsAccessors(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	uriA := protocol.DocumentURI("file:///a.go")
	uriB := protocol.DocumentURI("file:///b.go")

	require.Empty(t, c.GetFileDiagnostics(uriA))
	require.Empty(t, c.GetDiagnostics())
	require.Equal(t, DiagnosticCounts{}, c.GetDiagnosticCounts())

	c.diagnostics.Set(uriA, []protocol.Diagnostic{
		{Severity: protocol.SeverityError},
		{Severity: protocol.SeverityWarning},
		{Severity: protocol.SeverityWarning},
	})
	c.diagnostics.Set(uriB, []protocol.Diagnostic{
		{Severity: protocol.SeverityInformation},
		{Severity: protocol.SeverityHint},
	})

	require.Len(t, c.GetFileDiagnostics(uriA), 3)
	require.Len(t, c.GetDiagnostics(), 2)

	counts := c.GetDiagnosticCounts()
	require.Equal(t, DiagnosticCounts{Error: 1, Warning: 2, Information: 1, Hint: 1}, counts)

	// Calling again without changes must hit the cached path and return
	// the identical result.
	cached := c.GetDiagnosticCounts()
	require.Equal(t, counts, cached)

	// A further change bumps the version and forces a recompute.
	c.diagnostics.Set(uriA, []protocol.Diagnostic{{Severity: protocol.SeverityError}})
	updated := c.GetDiagnosticCounts()
	require.Equal(t, DiagnosticCounts{Error: 1, Information: 1, Hint: 1}, updated)
}

func TestClient_OpenFile_HandlesFileFalse(t *testing.T) {
	t.Parallel()

	c := &Client{
		name:      "test",
		cwd:       t.TempDir(),
		fileTypes: []string{"go"},
		openFiles: csync.NewMap[string, *OpenFileInfo](),
	}

	// Path outside the client's working directory never reaches disk I/O
	// or the (nil) underlying LSP client.
	err := c.OpenFile(context.Background(), "/definitely/not/in/cwd/file.go")
	require.NoError(t, err)
	require.False(t, c.IsFileOpen("/definitely/not/in/cwd/file.go"))
}

func TestClient_OpenFile_AlreadyOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := &Client{
		name:      "test",
		cwd:       dir,
		fileTypes: []string{"go"},
		openFiles: csync.NewMap[string, *OpenFileInfo](),
	}
	file := filepath.Join(dir, "main.go") // never created on disk
	uri := string(protocol.URIFromPath(file))
	c.openFiles.Set(uri, &OpenFileInfo{Version: 1, URI: protocol.DocumentURI(uri)})

	// Already open, so OpenFile returns immediately without touching disk
	// or the (nil) underlying LSP client.
	err := c.OpenFile(context.Background(), file)
	require.NoError(t, err)
}

func TestClient_OpenFile_FileNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := &Client{
		name:      "test",
		cwd:       dir,
		fileTypes: []string{"go"},
		openFiles: csync.NewMap[string, *OpenFileInfo](),
	}
	file := filepath.Join(dir, "missing.go")

	err := c.OpenFile(context.Background(), file)
	require.Error(t, err)
	require.Contains(t, err.Error(), "error reading file")
}

func TestClient_OpenFile_NotifiesUninitializedClient(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(file, []byte("package main\n"), 0o644))

	c := newRealClient(t, dir, []string{"go"})

	err := c.OpenFile(context.Background(), file)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client not initialized")
	require.False(t, c.IsFileOpen(file), "a failed notify must not mark the file as open")
}

func TestClient_NotifyChange_FileNotFound(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	err := c.NotifyChange(context.Background(), filepath.Join(t.TempDir(), "missing.go"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "error reading file")
}

func TestClient_NotifyChange_NotOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(file, []byte("package main\n"), 0o644))

	c := newTestClient()
	err := c.NotifyChange(context.Background(), file)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot notify change for unopened file")
}

func TestClient_NotifyChange_UninitializedClient(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(file, []byte("package main\n"), 0o644))

	c := newRealClient(t, dir, []string{"go"})
	uri := string(protocol.URIFromPath(file))
	c.openFiles.Set(uri, &OpenFileInfo{Version: 1, URI: protocol.DocumentURI(uri)})

	err := c.NotifyChange(context.Background(), file)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client not initialized")

	info, ok := c.openFiles.Get(uri)
	require.True(t, ok)
	require.EqualValues(t, 2, info.Version, "version increments even when the notify itself fails")
}

func TestClient_CloseAllFiles_Empty(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	c.CloseAllFiles(context.Background()) // must not touch the nil c.client
	require.Equal(t, 0, c.openFiles.Len())
}

func TestClient_CloseAllFiles_NotifyFailureKeepsEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := newRealClient(t, dir, []string{"go"})

	uri1 := string(protocol.URIFromPath(filepath.Join(dir, "a.go")))
	uri2 := string(protocol.URIFromPath(filepath.Join(dir, "b.go")))
	c.openFiles.Set(uri1, &OpenFileInfo{Version: 1, URI: protocol.DocumentURI(uri1)})
	c.openFiles.Set(uri2, &OpenFileInfo{Version: 1, URI: protocol.DocumentURI(uri2)})

	c.CloseAllFiles(context.Background())

	// The notify fails because the client was never initialized, so the
	// entries are kept rather than deleted.
	_, ok1 := c.openFiles.Get(uri1)
	_, ok2 := c.openFiles.Get(uri2)
	require.True(t, ok1)
	require.True(t, ok2)
}

func TestClient_RefreshOpenFiles_Empty(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	c.RefreshOpenFiles(context.Background()) // must not touch the nil c.client
}

func TestClient_RefreshOpenFiles_IncrementsVersionOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(file, []byte("package main\n"), 0o644))

	c := newRealClient(t, dir, []string{"go"})
	uri := string(protocol.URIFromPath(file))
	c.openFiles.Set(uri, &OpenFileInfo{Version: 1, URI: protocol.DocumentURI(uri)})

	c.RefreshOpenFiles(context.Background())

	info, ok := c.openFiles.Get(uri)
	require.True(t, ok)
	require.EqualValues(t, 2, info.Version)
}

func TestClient_NotifyWorkspaceChange_Uninitialized(t *testing.T) {
	t.Parallel()

	c := newRealClient(t, t.TempDir(), []string{"go"})

	err := c.NotifyWorkspaceChange(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "client not initialized")
}
