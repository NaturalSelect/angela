package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPruneSessionScratch pins the retention rule that replaces
// per-turn deletion for top-level sessions: whole session directories go
// once they have sat untouched past the retention, and nothing else does.
func TestPruneSessionScratch(t *testing.T) {
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

		require.NoError(t, PruneSessionScratch(root, time.Hour))

		require.NoDirExists(t, stale)
		require.DirExists(t, fresh)
	})

	t.Run("a missing root is not an error", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, PruneSessionScratch(filepath.Join(t.TempDir(), "absent"), time.Hour))
	})

	t.Run("leaves loose files alone", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		loose := filepath.Join(root, "notes.txt")
		require.NoError(t, os.WriteFile(loose, []byte("x"), 0o600))
		ageDir(t, loose, 48*time.Hour)

		require.NoError(t, PruneSessionScratch(root, time.Hour))

		require.FileExists(t, loose)
	})
}

// TestSessionScratchDirRejectsUnsafeSessionIDs pins the guard that
// keeps a session ID confined to a single path component. Delegated
// sessions build their ID from a provider-supplied tool-call ID
// (coordinator.CreateAgentToolSessionID), which this package cannot
// trust, and filepath.Join normalizes "../" straight through rather
// than sandboxing to root.
func TestSessionScratchDirRejectsUnsafeSessionIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, id := range []string{"session-a", "msg-1$$call-2", "abc123", "a.b.c"} {
		dir, err := SessionScratchDir(root, id)
		require.NoError(t, err, id)
		require.Equal(t, filepath.Join(root, id), dir)
	}

	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "../escape", "a/../b", "msg-1$$../../escape"} {
		_, err := SessionScratchDir(root, id)
		require.Error(t, err, id)
	}
}
