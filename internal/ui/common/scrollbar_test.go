package common

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestScrollbar(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("non-positive height returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, Scrollbar(&sty, 0, 100, 10, 0))
		require.Empty(t, Scrollbar(&sty, -3, 100, 10, 0))
	})

	t.Run("content fitting the viewport returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, Scrollbar(&sty, 10, 10, 10, 0))
		require.Empty(t, Scrollbar(&sty, 10, 5, 10, 0))
	})

	t.Run("the thumb tracks the scroll offset", func(t *testing.T) {
		t.Parallel()

		out := Scrollbar(&sty, 10, 100, 10, 0)
		lines := strings.Split(out, "\n")
		require.Len(t, lines, 10)
		require.Equal(t, styles.ScrollbarThumb, ansi.Strip(lines[0]))
		for _, l := range lines[1:] {
			require.Equal(t, styles.ScrollbarTrack, ansi.Strip(l))
		}

		outEnd := Scrollbar(&sty, 10, 100, 10, 90)
		linesEnd := strings.Split(outEnd, "\n")
		require.Equal(t, styles.ScrollbarThumb, ansi.Strip(linesEnd[9]), "the thumb must be at the bottom for the max offset")
	})

	t.Run("a larger viewport share grows the thumb", func(t *testing.T) {
		t.Parallel()

		out := Scrollbar(&sty, 10, 20, 10, 0)
		lines := strings.Split(out, "\n")
		thumbCount := 0
		for _, l := range lines {
			if ansi.Strip(l) == styles.ScrollbarThumb {
				thumbCount++
			}
		}
		require.Equal(t, 5, thumbCount, "height*viewport/content = 10*10/20 = 5")
	})
}
