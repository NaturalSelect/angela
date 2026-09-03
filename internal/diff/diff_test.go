package diff

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateDiff(t *testing.T) {
	t.Parallel()

	t.Run("counts additions and removals", func(t *testing.T) {
		t.Parallel()
		before := "line1\nline2\nline3\n"
		after := "line1\nchanged\nline3\nline4\n"

		unified, additions, removals := GenerateDiff(before, after, "file.txt")

		require.Contains(t, unified, "a/file.txt")
		require.Contains(t, unified, "b/file.txt")
		require.Equal(t, 2, additions) // "changed" and "line4"
		require.Equal(t, 1, removals)  // "line2"
	})

	t.Run("strips leading slash from filename", func(t *testing.T) {
		t.Parallel()
		unified, _, _ := GenerateDiff("a\n", "b\n", "/abs/path.txt")
		require.Contains(t, unified, "a/abs/path.txt")
		require.Contains(t, unified, "b/abs/path.txt")
		require.NotContains(t, unified, "a//abs/path.txt")
	})

	t.Run("identical content has no changes", func(t *testing.T) {
		t.Parallel()
		unified, additions, removals := GenerateDiff("same\n", "same\n", "f.txt")
		require.Equal(t, 0, additions)
		require.Equal(t, 0, removals)
		require.Empty(t, strings.TrimSpace(unified))
	})

	t.Run("pure addition", func(t *testing.T) {
		t.Parallel()
		_, additions, removals := GenerateDiff("", "new content\n", "f.txt")
		require.Equal(t, 1, additions)
		require.Equal(t, 0, removals)
	})

	t.Run("pure removal", func(t *testing.T) {
		t.Parallel()
		_, additions, removals := GenerateDiff("old content\n", "", "f.txt")
		require.Equal(t, 0, additions)
		require.Equal(t, 1, removals)
	})
}
