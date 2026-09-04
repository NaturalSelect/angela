package common

import (
	"image/color"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestChromaStyle_MemoizesPerThemeAndBackground exercises the fresh-base,
// cache-hit, and per-background paths of the memo. It intentionally does
// not call t.Parallel(): the memo is a single global slot keyed by theme
// pointer, so interleaving with any other test that also resolves a
// ChromaStyle concurrently would thrash the slot and make the identity
// assertions flaky.
func TestChromaStyle_MemoizesPerThemeAndBackground(t *testing.T) {
	sty := styles.CharmtonePantera()

	base := ChromaStyle(&sty, nil)
	require.NotNil(t, base)
	require.Same(t, base, ChromaStyle(&sty, nil), "repeated calls for the same theme must hit the memo")

	bg := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	withBg := ChromaStyle(&sty, bg)
	require.NotNil(t, withBg)
	require.NotSame(t, base, withBg, "a background override must build a distinct style")
	require.Same(t, withBg, ChromaStyle(&sty, bg), "the same background must hit the per-background memo")

	other := styles.CharmtonePantera()
	require.NotSame(t, base, ChromaStyle(&other, nil), "switching the theme pointer must reset the memo")
}
