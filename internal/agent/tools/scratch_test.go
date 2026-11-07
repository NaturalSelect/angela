package tools

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionScratchDirRejectsUnsafeSessionIDs pins the guard that
// keeps a session ID confined to a single path component. Delegated
// sessions build their ID from a provider-supplied tool-call ID
// (coordinator.CreateAgentToolSessionID), which this package cannot
// trust, and filepath.Join normalizes "../" straight through rather
// than sandboxing to root.
func TestSessionScratchDirRejectsUnsafeSessionIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, id := range []string{"session-a", "abc123", "a.b.c", "4f1c9a52-7d1e-4a63-9f0b-2d8c5e7a1b34"} {
		dir, err := SessionScratchDir(root, id)
		require.NoError(t, err, id)
		require.Equal(t, filepath.Join(root, id), dir, "a plain ID is kept so the directory stays recognizable")
	}

	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "../escape", "a/../b", "msg-1$$../../escape"} {
		_, err := SessionScratchDir(root, id)
		require.Error(t, err, id)
	}
}

// TestSessionScratchDirKeepsShellMetacharactersOutOfTheName pins that the
// directory name is safe to paste into a shell command unquoted. A
// delegated session's ID is messageID$$toolCallID, and the model is told to
// pass paths in this directory to Bash, where an unquoted $$ expands to the
// shell's process ID and names a file that does not exist.
func TestSessionScratchDirKeepsShellMetacharactersOutOfTheName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	plain := regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

	ids := []string{"msg-1$$call-2", "msg-1$$call_3", "msg 1", "a;b", "a`b`", "a'b", "$(x)", "functions.bash:0", "naïve"}
	seen := map[string]string{}
	for _, id := range ids {
		dir, err := SessionScratchDir(root, id)
		require.NoError(t, err, id)
		require.Equal(t, root, filepath.Dir(dir), "%q must stay a single component of root", id)
		require.Regexp(t, plain, filepath.Base(dir), id)

		again, err := SessionScratchDir(root, id)
		require.NoError(t, err)
		require.Equal(t, dir, again, "the writer and the later cleanup must agree on %q", id)

		other, clash := seen[dir]
		require.False(t, clash, "%q and %q must not share a directory", id, other)
		seen[dir] = id
	}
}
