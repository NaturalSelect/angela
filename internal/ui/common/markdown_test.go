package common

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestMarkdownRenderer covers MarkdownRenderer, QuietMarkdownRenderer,
// InvalidateMarkdownRendererCache and LockMarkdownRenderer together in one
// non-parallel test. The renderer caches are single global maps shared by
// every caller; InvalidateMarkdownRendererCache wipes them outright, so
// running these identity assertions concurrently with anything else that
// resolves a renderer (in this package) would be flaky by design. None of
// the subtests call t.Parallel() either, keeping the whole sequence
// isolated.
func TestMarkdownRenderer(t *testing.T) {
	t.Run("memoizes per width", func(t *testing.T) {
		sty := styles.CharmtonePantera()

		r1 := MarkdownRenderer(&sty, 81)
		require.NotNil(t, r1)
		require.Same(t, r1, MarkdownRenderer(&sty, 81), "the same width must hit the memo")

		r2 := MarkdownRenderer(&sty, 41)
		require.NotNil(t, r2)
		require.NotSame(t, r1, r2, "a different width must build a distinct renderer")
	})

	t.Run("quiet variant memoizes per width independently", func(t *testing.T) {
		sty := styles.CharmtonePantera()

		r1 := QuietMarkdownRenderer(&sty, 82)
		require.NotNil(t, r1)
		require.Same(t, r1, QuietMarkdownRenderer(&sty, 82))
	})

	t.Run("user variant memoizes per width independently", func(t *testing.T) {
		sty := styles.CharmtonePantera()

		r1 := UserMarkdownRenderer(&sty, 87)
		require.NotNil(t, r1)
		require.Same(t, r1, UserMarkdownRenderer(&sty, 87))
	})

	t.Run("user variant preserves manual line breaks", func(t *testing.T) {
		sty := styles.CharmtonePantera()
		r := UserMarkdownRenderer(&sty, 88)
		require.NotNil(t, r)

		mu := LockMarkdownRenderer(r)
		mu.Lock()
		out, err := r.Render("first line\nsecond line\nthird line")
		mu.Unlock()

		require.NoError(t, err)
		require.Equal(t, []string{"first line", "second line", "third line"}, trimmedLines(out),
			"each typed line must stay on its own line")
	})

	t.Run("renders markdown content", func(t *testing.T) {
		sty := styles.CharmtonePantera()
		r := MarkdownRenderer(&sty, 83)
		require.NotNil(t, r)

		mu := LockMarkdownRenderer(r)
		mu.Lock()
		out, err := r.Render("# Hello\n\nSome **bold** text.")
		mu.Unlock()

		require.NoError(t, err)
		stripped := ansi.Strip(out)
		require.Contains(t, stripped, "Hello")
		require.Contains(t, stripped, "bold")
	})

	t.Run("quiet renderer strips color styling", func(t *testing.T) {
		sty := styles.CharmtonePantera()
		r := QuietMarkdownRenderer(&sty, 84)
		require.NotNil(t, r)

		mu := LockMarkdownRenderer(r)
		mu.Lock()
		out, err := r.Render("# Heading")
		mu.Unlock()

		require.NoError(t, err)
		require.Contains(t, ansi.Strip(out), "Heading")
	})

	t.Run("invalidate drops the cached renderer", func(t *testing.T) {
		sty := styles.CharmtonePantera()

		before := MarkdownRenderer(&sty, 85)
		InvalidateMarkdownRendererCache()
		after := MarkdownRenderer(&sty, 85)
		require.NotSame(t, before, after, "invalidation must drop the cached renderer")
	})

	t.Run("lock returns a stable mutex per renderer", func(t *testing.T) {
		sty := styles.CharmtonePantera()
		r := MarkdownRenderer(&sty, 86)

		mu1 := LockMarkdownRenderer(r)
		mu2 := LockMarkdownRenderer(r)
		require.Same(t, mu1, mu2, "the same renderer must always return the same mutex")
	})
}

// trimmedLines strips ANSI codes and right-padding (Glamour pads wrapped
// lines out to the render width) so line content can be compared exactly.
func trimmedLines(rendered string) []string {
	stripped := strings.TrimRight(ansi.Strip(rendered), "\n")
	lines := strings.Split(stripped, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}
