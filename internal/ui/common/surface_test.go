package common_test

import (
	"image/color"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

var (
	bandBg = color.RGBA{0x24, 0x24, 0x24, 0xff}
	textFg = color.RGBA{0xff, 0x00, 0x00, 0xff}
)

func sameColor(t *testing.T, want, got color.Color, msg string, args ...any) {
	t.Helper()
	if want == nil {
		require.Nilf(t, got, msg, args...)
		return
	}
	require.NotNilf(t, got, msg, args...)
	wr, wg, wb, wa := want.RGBA()
	gr, gg, gb, ga := got.RGBA()
	require.Equalf(t, [4]uint32{wr, wg, wb, wa}, [4]uint32{gr, gg, gb, ga}, msg, args...)
}

// A style reset inside the content is exactly what broke the lipgloss
// background approach; on a cell surface it must leave the fill alone.
func TestDrawOnSurfaceKeepsFillUnderStyledText(t *testing.T) {
	t.Parallel()

	base := uv.Style{Bg: bandBg}
	buf := uv.NewScreenBuffer(12, 2)
	// Red "hi" followed by an explicit reset, then plain text after it.
	common.DrawOnSurface(&buf, buf.Bounds(), base, "\x1b[38;2;255;0;0mhi\x1b[mok")

	for x := range 12 {
		for y := range 2 {
			cell := buf.CellAt(x, y)
			require.NotNil(t, cell, "cell (%d,%d) is nil", x, y)
			sameColor(t, bandBg, cell.Style.Bg,
				"cell (%d,%d) lost the surface fill", x, y)
		}
	}

	sameColor(t, textFg, buf.CellAt(0, 0).Style.Fg, "styled text lost its own foreground")
	require.Equal(t, "o", buf.CellAt(2, 0).Content, "text after the reset went missing")
}

func TestDrawOnSurfaceFillsBlankArea(t *testing.T) {
	t.Parallel()

	base := uv.Style{Bg: bandBg}
	buf := uv.NewScreenBuffer(6, 3)
	common.DrawOnSurface(&buf, buf.Bounds(), base, "x")

	for x := range 6 {
		for y := range 3 {
			cell := buf.CellAt(x, y)
			require.NotNil(t, cell)
			require.NotEmpty(t, cell.Content, "cell (%d,%d) has no content", x, y)
			sameColor(t, bandBg, cell.Style.Bg, "blank cell (%d,%d) was not filled", x, y)
		}
	}
}

// A wide rune occupies two columns, and the second one is a zero-width
// placeholder. Writing to it makes the buffer erase the rune it belongs to,
// which is how CJK text vanished from the user band.
func TestDrawOnSurfaceKeepsWideRunes(t *testing.T) {
	t.Parallel()

	base := uv.Style{Bg: bandBg}
	buf := uv.NewScreenBuffer(12, 1)
	common.DrawOnSurface(&buf, buf.Bounds(), base, "中文ok")

	require.Equal(t, "中", buf.CellAt(0, 0).Content)
	require.Equal(t, "文", buf.CellAt(2, 0).Content)
	require.Equal(t, "o", buf.CellAt(4, 0).Content)
	require.Equal(t, "k", buf.CellAt(5, 0).Content)

	for x := range 12 {
		cell := buf.CellAt(x, 0)
		// The continuation column of a wide rune carries no style; the
		// rune's own cell paints both columns.
		if cell.Width == 0 {
			continue
		}
		sameColor(t, bandBg, cell.Style.Bg, "cell (%d,0) lost the surface fill", x)
	}
}

func TestSetSpanKeepsWideRunes(t *testing.T) {
	t.Parallel()

	buf := uv.NewScreenBuffer(10, 1)
	common.FillRect(&buf, buf.Bounds(), uv.Style{Bg: bandBg})
	common.SetSpan(&buf, 1, 0, uv.Style{Fg: textFg}, "中文")

	require.Equal(t, "中", buf.CellAt(1, 0).Content)
	require.Equal(t, "文", buf.CellAt(3, 0).Content)
	sameColor(t, textFg, buf.CellAt(1, 0).Style.Fg, "span lost its foreground")
	sameColor(t, bandBg, buf.CellAt(1, 0).Style.Bg, "span erased the fill")
	sameColor(t, bandBg, buf.CellAt(5, 0).Style.Bg, "span bled past its width")
}

// The chat path renders items to a string and parses that string back into
// cells. If the background does not survive that round-trip, every surface in
// the redesign is unreachable through the list.Item contract.
func TestRenderSurfaceSurvivesTheStringRoundTrip(t *testing.T) {
	t.Parallel()

	base := uv.Style{Bg: bandBg}
	out := common.RenderSurface(10, 2, base, "\x1b[38;2;255;0;0mhello\x1b[m")
	require.NotEmpty(t, out)

	buf := uv.NewScreenBuffer(10, 2)
	uv.NewStyledString(out).Draw(&buf, buf.Bounds())

	for x := range 10 {
		for y := range 2 {
			cell := buf.CellAt(x, y)
			require.NotNil(t, cell, "cell (%d,%d) is nil after round-trip", x, y)
			sameColor(t, bandBg, cell.Style.Bg,
				"cell (%d,%d) lost the fill through the string round-trip", x, y)
		}
	}
}

func TestRenderSurfaceRejectsEmptySize(t *testing.T) {
	t.Parallel()

	base := uv.Style{Bg: bandBg}
	require.Empty(t, common.RenderSurface(0, 3, base, "x"))
	require.Empty(t, common.RenderSurface(3, 0, base, "x"))
	require.Empty(t, common.RenderSurface(-1, -1, base, "x"))
}

// A label pinned onto a filled row must not cut a notch out of the fill.
func TestSetSpanWithoutBackgroundKeepsTheSurface(t *testing.T) {
	t.Parallel()

	base := uv.Style{Bg: bandBg}
	buf := uv.NewScreenBuffer(10, 1)
	common.FillRect(&buf, buf.Bounds(), base)
	common.SetSpan(&buf, 2, 0, uv.Style{Fg: textFg}, "ab")

	sameColor(t, bandBg, buf.CellAt(2, 0).Style.Bg, "span erased the fill")
	sameColor(t, textFg, buf.CellAt(2, 0).Style.Fg, "span lost its foreground")
	require.Equal(t, "a", buf.CellAt(2, 0).Content)
	require.Equal(t, "b", buf.CellAt(3, 0).Content)
	sameColor(t, bandBg, buf.CellAt(4, 0).Style.Bg, "span bled past its width")
}

// The bottom-border label works the other way round: its own background is
// what overwrites the border glyphs.
func TestSetSpanWithBackgroundOverwritesTheSurface(t *testing.T) {
	t.Parallel()

	panelBg := color.RGBA{0x14, 0x14, 0x14, 0xff}
	buf := uv.NewScreenBuffer(6, 1)
	common.FillRect(&buf, buf.Bounds(), uv.Style{Bg: bandBg})
	common.SetSpan(&buf, 1, 0, uv.Style{Bg: panelBg}, "──")

	sameColor(t, panelBg, buf.CellAt(1, 0).Style.Bg, "span did not paint its own background")
	sameColor(t, bandBg, buf.CellAt(3, 0).Style.Bg, "span bled past its width")
}

func TestSetSpanIgnoresEmptyContent(t *testing.T) {
	t.Parallel()

	buf := uv.NewScreenBuffer(4, 1)
	common.FillRect(&buf, buf.Bounds(), uv.Style{Bg: bandBg})
	require.NotPanics(t, func() {
		common.SetSpan(&buf, 0, 0, uv.Style{Fg: textFg}, "")
	})
	sameColor(t, bandBg, buf.CellAt(0, 0).Style.Bg, "empty content disturbed the fill")
}
