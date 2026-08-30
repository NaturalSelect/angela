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
	// way coordinator.removeWebFetchScratch does at the end of a turn.
	require.NoError(t, os.RemoveAll(dirA))
	_, err = os.Stat(dirA)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(dirB)
	require.NoError(t, err, "session-b's page must survive session-a's cleanup")
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

			_, err = tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: toolnames.WebFetch, Input: string(input)})
			require.Error(t, err, "an unsafe session id must fail the call instead of writing somewhere unexpected")

			_, statErr := os.Stat(unsafeTarget)
			require.True(t, os.IsNotExist(statErr), "must not have escaped scratchRoot")
		})
	}
}
