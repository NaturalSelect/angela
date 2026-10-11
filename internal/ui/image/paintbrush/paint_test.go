package paintbrush

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestPaint_ProcessesEveryCellAfterCollectionFinishes repeats Paint because
// the original defect was a scheduling race: it only showed up when the
// result collector fell behind the render workers. Run with -race to see the
// unsynchronised read directly.
func TestPaint_ProcessesEveryCellAfterCollectionFinishes(t *testing.T) {
	t.Parallel()

	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 6), G: uint8(y * 12), B: 128, A: 255})
		}
	}

	const cols, rows = 4, 2
	for range 10 {
		canvas := New()
		canvas.SetImage(img)
		canvas.SetWidth(cols)
		canvas.SetHeight(rows)
		canvas.Paint()

		lines := strings.Split(canvas.GetResult(), "\n")
		require.Len(t, lines, rows)
		for _, line := range lines {
			require.Equal(t, cols, ansi.StringWidth(line))
		}
	}
}
