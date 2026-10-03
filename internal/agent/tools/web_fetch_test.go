package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// TestWebFetchToolScopesLargePagesToSession pins the scratch directory
// layout: a large page must land under a subdirectory named after the
// calling session, not directly under the shared scratch root, so that
// removing one session's cache cannot touch a concurrent session's.
func TestWebFetchToolScopesLargePagesToSession(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", LargeContentThreshold+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	scratchRoot := t.TempDir()
	tool := NewWebFetchTool(scratchRoot, srv.Client())

	fetchAs := func(t *testing.T, sessionID string) string {
		t.Helper()
		ctx := context.WithValue(context.Background(), SessionIDContextKey, sessionID)
		input, err := json.Marshal(WebFetchParams{URL: srv.URL})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-" + sessionID, Name: toolnames.WebFetch, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		return resp.Content
	}

	respA := fetchAs(t, "session-a")
	respB := fetchAs(t, "session-b")

	entries, err := os.ReadDir(scratchRoot)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.ElementsMatch(t, []string{"session-a", "session-b"}, names,
		"large pages must be nested under a per-session directory")

	dirA := filepath.Join(scratchRoot, "session-a")
	dirB := filepath.Join(scratchRoot, "session-b")
	require.Contains(t, respA, dirA)
	require.Contains(t, respB, dirB)

	filesA, err := os.ReadDir(dirA)
	require.NoError(t, err)
	require.Len(t, filesA, 1)

	// Cleaning up one session's cache must not disturb another's, the
	// way coordinator.removeWebFetchScratch does at the end of a delegated run.
	require.NoError(t, os.RemoveAll(dirA))
	_, err = os.Stat(dirA)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(dirB)
	require.NoError(t, err, "session-b's page must survive session-a's cleanup")
}

// TestPruneWebFetchScratch pins the retention rule that replaces
// per-turn deletion for top-level sessions: whole session directories go
// once they have sat untouched past the retention, and nothing else does.
func TestPruneWebFetchScratch(t *testing.T) {
	t.Parallel()

	ageDir := func(t *testing.T, path string, age time.Duration) {
		t.Helper()
		when := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(path, when, when))
	}
	makeSession := func(t *testing.T, root, id string) string {
		t.Helper()
		dir := filepath.Join(root, id)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "page-1.md"), []byte("x"), 0o600))
		return dir
	}

	t.Run("removes only session directories older than the retention", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		stale := makeSession(t, root, "stale")
		fresh := makeSession(t, root, "fresh")
		ageDir(t, stale, 2*time.Hour)
		ageDir(t, fresh, 30*time.Minute)

		require.NoError(t, pruneWebFetchScratch(root, time.Hour))

		require.NoDirExists(t, stale)
		require.DirExists(t, fresh)
	})

	t.Run("a missing root is not an error", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, pruneWebFetchScratch(filepath.Join(t.TempDir(), "absent"), time.Hour))
	})

	t.Run("leaves loose files alone", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		loose := filepath.Join(root, "notes.txt")
		require.NoError(t, os.WriteFile(loose, []byte("x"), 0o600))
		ageDir(t, loose, 48*time.Hour)

		require.NoError(t, pruneWebFetchScratch(root, time.Hour))

		require.FileExists(t, loose)
	})
}

// TestWebFetchToolPrunesStaleSessionsOnLargeSave checks the prune is
// wired into the tool: a large save clears a stale session's pages but
// keeps the caller's own.
func TestWebFetchToolPrunesStaleSessionsOnLargeSave(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", LargeContentThreshold+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	scratchRoot := t.TempDir()
	stale := filepath.Join(scratchRoot, "stale-session")
	require.NoError(t, os.MkdirAll(stale, 0o700))
	old := time.Now().Add(-2 * webFetchScratchRetention)
	require.NoError(t, os.Chtimes(stale, old, old))

	tool := NewWebFetchTool(scratchRoot, srv.Client())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "live-session")
	input, err := json.Marshal(WebFetchParams{URL: srv.URL})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: toolnames.WebFetch, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	require.NoDirExists(t, stale)
	require.DirExists(t, filepath.Join(scratchRoot, "live-session"))
}

// TestWebFetchScratchDirRejectsUnsafeSessionIDs pins the guard that
// keeps a session ID confined to a single path component. Delegated
// sessions build their ID from a provider-supplied tool-call ID
// (coordinator.CreateAgentToolSessionID), which this package cannot
// trust, and filepath.Join normalizes "../" straight through rather
// than sandboxing to root.
func TestWebFetchScratchDirRejectsUnsafeSessionIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, id := range []string{"session-a", "msg-1$$call-2", "abc123", "a.b.c"} {
		dir, err := WebFetchScratchDir(root, id)
		require.NoError(t, err, id)
		require.Equal(t, filepath.Join(root, id), dir)
	}

	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "../escape", "a/../b", "msg-1$$../../escape"} {
		_, err := WebFetchScratchDir(root, id)
		require.Error(t, err, id)
	}
}

// TestWebFetchToolRejectsPathTraversalSessionID exercises the guard
// through the actual tool call, not just the helper: a large fetch
// under a malicious session ID must fail outright rather than writing
// its cache somewhere outside scratchRoot.
func TestWebFetchToolRejectsPathTraversalSessionID(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", LargeContentThreshold+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	scratchRoot := t.TempDir()
	tool := NewWebFetchTool(scratchRoot, srv.Client())
	unsafeTarget := filepath.Join(scratchRoot, "..", "evil-marker")

	for _, maliciousID := range []string{
		"../evil-marker",
		"msg-1$$../../evil-marker",
		"a/b",
		`a\b`,
		"..",
		".",
	} {
		t.Run(maliciousID, func(t *testing.T) {
			t.Parallel()

			ctx := context.WithValue(context.Background(), SessionIDContextKey, maliciousID)
			input, err := json.Marshal(WebFetchParams{URL: srv.URL})
			require.NoError(t, err)

			resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: toolnames.WebFetch, Input: string(input)})
			require.NoError(t, err)
			require.True(t, resp.IsError, "an unsafe session id must fail the call instead of writing somewhere unexpected")

			_, statErr := os.Stat(unsafeTarget)
			require.True(t, os.IsNotExist(statErr), "must not have escaped scratchRoot")
		})
	}
}
