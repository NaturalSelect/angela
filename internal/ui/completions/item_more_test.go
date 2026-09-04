package completions

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

func TestRenderItem_CacheHitReturnsStoredValue(t *testing.T) {
	t.Parallel()

	cache := map[int]string{10: "sentinel"}
	match := fuzzy.Match{}
	got := renderItem(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle(), "hello", false, 10, cache, &match)
	require.Equal(t, "sentinel", got, "a cache hit must short-circuit rendering entirely")
}

func TestRenderItem_TruncatesWhenTextExceedsWidth(t *testing.T) {
	t.Parallel()

	match := fuzzy.Match{}
	got := renderItem(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle(), "a-very-long-filename.go", false, 8, nil, &match)
	require.Contains(t, got, "…")
}

func TestMatchedRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []int
		want [][2]int
	}{
		{"empty", nil, [][2]int{}},
		{"single", []int{2}, [][2]int{{2, 2}}},
		{"contiguous run", []int{0, 1, 2}, [][2]int{{0, 2}}},
		{"two runs", []int{0, 1, 3, 4}, [][2]int{{0, 1}, {3, 4}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, matchedRanges(tt.in))
		})
	}
}

// TestBytePosToVisibleCharPos_HandlesOutOfRangeGracefully covers the
// grapheme-exhaustion guards in both loops: a requested byte range that
// runs past the end of the string must stop at the string's end instead
// of looping forever or panicking.
func TestBytePosToVisibleCharPos_HandlesOutOfRangeGracefully(t *testing.T) {
	t.Parallel()

	t.Run("start within range, stop past the end", func(t *testing.T) {
		t.Parallel()
		start, stop := bytePosToVisibleCharPos("ab", [2]int{0, 100})
		require.Equal(t, 0, start)
		require.Equal(t, 2, stop)
	})

	t.Run("start also past the end", func(t *testing.T) {
		t.Parallel()
		start, stop := bytePosToVisibleCharPos("ab", [2]int{100, 200})
		require.Equal(t, 2, start)
		require.Equal(t, 2, stop)
	})
}
