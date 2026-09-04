package list

import (
	"image"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestHighlightBuffer_NegativeStartReturnsNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, HighlightBuffer("abc", image.Rect(0, 0, 3, 1), -1, 0, 0, 0, nil))
	require.Nil(t, HighlightBuffer("abc", image.Rect(0, 0, 3, 1), 0, -1, 0, 0, nil))
}

func TestHighlightBuffer_AppliesHighlightWithinRange(t *testing.T) {
	t.Parallel()

	content := "hello\nworld"
	area := image.Rect(0, 0, 5, 2)

	buf := HighlightBuffer(content, area, 0, 1, 0, 4, nil)
	require.NotNil(t, buf)

	line0 := buf.Line(0)
	for x := 1; x < 4; x++ {
		cell := line0.At(x)
		require.NotNil(t, cell)
		require.NotZero(t, cell.Style.Attrs&uv.AttrReverse, "cell %d must carry the default highlight", x)
	}
	require.Zero(t, line0.At(0).Style.Attrs&uv.AttrReverse, "cell before the range must not be highlighted")

	line1 := buf.Line(1)
	for x := range 5 {
		cell := line1.At(x)
		require.NotNil(t, cell)
		require.Zero(t, cell.Style.Attrs&uv.AttrReverse, "a line outside [startLine,endLine] must be untouched")
	}
}

func TestHighlightBuffer_CustomHighlighterIsUsed(t *testing.T) {
	t.Parallel()

	var calls int
	buf := HighlightBuffer("hi", image.Rect(0, 0, 2, 1), 0, 0, 0, -1, func(x, y int, c *uv.Cell) *uv.Cell {
		calls++
		return c
	})
	require.NotNil(t, buf)
	require.Equal(t, 2, calls, "the highlighter must run once per highlighted cell")
}

func TestHighlightContent_NegativeStartReturnsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, HighlightContent("abc", image.Rect(0, 0, 3, 1), -1, 0, 0, 0))
}

func TestHighlightContent_ExtractsHighlightedText(t *testing.T) {
	t.Parallel()

	got := HighlightContent("hello\nworld", image.Rect(0, 0, 5, 2), 0, 0, -1, -1)
	require.Equal(t, "hello\nworld\n", got)
}

func TestHighlightContent_SingleLineRange(t *testing.T) {
	t.Parallel()

	got := HighlightContent("hello", image.Rect(0, 0, 5, 1), 0, 1, 0, 4)
	require.Equal(t, "ell\n", got)
}

func TestHighlight_NegativeStartReturnsOriginalContent(t *testing.T) {
	t.Parallel()

	original := "  abc  \r\n"
	require.Equal(t, original, Highlight(original, image.Rect(0, 0, 3, 1), -1, 0, 0, 0, nil))
}

func TestHighlight_AppliesHighlighterAndPreservesText(t *testing.T) {
	t.Parallel()

	content := "hello"
	area := image.Rect(0, 0, 5, 1)
	out := Highlight(content, area, 0, 0, 0, -1, DefaultHighlighter)
	require.Contains(t, out, "\x1b", "highlighted output must carry style escapes")
	require.Equal(t, "hello", ansi.Strip(out))
}

func TestToHighlighter_AppliesLipglossStyle(t *testing.T) {
	t.Parallel()

	h := ToHighlighter(lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")))
	cell := &uv.Cell{Content: "x"}
	got := h(0, 0, cell)
	require.NotNil(t, got)
	require.NotNil(t, got.Style.Fg)
}

func TestToHighlighter_NilCellPassesThrough(t *testing.T) {
	t.Parallel()

	h := ToHighlighter(lipgloss.NewStyle())
	require.Nil(t, h(0, 0, nil))
}

func TestAdjustArea(t *testing.T) {
	t.Parallel()

	t.Run("no style is a no-op", func(t *testing.T) {
		t.Parallel()

		area := image.Rect(2, 3, 10, 12)
		require.Equal(t, area, AdjustArea(area, lipgloss.NewStyle()))
	})

	t.Run("margin border and padding are subtracted from every side", func(t *testing.T) {
		t.Parallel()

		style := lipgloss.NewStyle().Margin(1).Border(lipgloss.NormalBorder()).Padding(2)
		area := image.Rect(0, 0, 20, 20)
		require.Equal(t, image.Rect(4, 4, 16, 16), AdjustArea(area, style))
	})
}
