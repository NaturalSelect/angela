package logo

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testOpts() Opts {
	return Opts{
		TitleColorA:  color.RGBA{R: 0xff, A: 0xff},
		TitleColorB:  color.RGBA{B: 0xff, A: 0xff},
		VersionColor: color.RGBA{G: 0xff, A: 0xff},
	}
}

func TestRenderDelegatesToCompactRender(t *testing.T) {
	t.Parallel()

	o := testOpts()
	require.Equal(t,
		compactRender(lipgloss.NewStyle(), "v1", o),
		Render(lipgloss.NewStyle(), "v1", true, o))
}

func TestRenderFullWordmarkHasVersionCaption(t *testing.T) {
	t.Parallel()

	out := Render(lipgloss.NewStyle(), "v9.9.9", false, testOpts())
	lines := strings.Split(ansi.Strip(out), "\n")
	require.Len(t, lines, 4, "3 letterform rows plus a version caption row")
	require.Contains(t, lines[3], "v9.9.9")
}

func TestRenderFullWordmarkNoVersion(t *testing.T) {
	t.Parallel()

	out := Render(lipgloss.NewStyle(), "", false, testOpts())
	lines := strings.Split(ansi.Strip(out), "\n")
	require.Len(t, lines, 3, "no version means no caption row")
}

func TestRenderFullWordmarkTruncatesToWidth(t *testing.T) {
	t.Parallel()

	o := testOpts()
	o.Width = 10
	out := Render(lipgloss.NewStyle(), "a-much-longer-version-string", false, o)
	for i, line := range strings.Split(out, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 10, "line %d exceeds requested width", i)
	}
}

func TestRenderUnstableDoesNotPanic(t *testing.T) {
	t.Parallel()

	o := testOpts()
	o.Unstable = true
	require.NotPanics(t, func() {
		Render(lipgloss.NewStyle(), "", false, o)
	})
}

func TestCompactRenderWithVersion(t *testing.T) {
	t.Parallel()

	o := testOpts()
	out := compactRender(lipgloss.NewStyle(), "v1.0", o)
	stripped := ansi.Strip(out)
	require.Contains(t, stripped, "v1.0")
	require.Contains(t, stripped, WordmarkText)
}

func TestCompactRenderWithoutVersion(t *testing.T) {
	t.Parallel()

	o := testOpts()
	out := compactRender(lipgloss.NewStyle(), "", o)
	require.Equal(t,
		styles.ApplyBoldForegroundGrad(lipgloss.NewStyle(), WordmarkText, o.TitleColorA, o.TitleColorB),
		out)
}

func TestWordmarkTruncatesToWidth(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	full := Wordmark(&sty, 0)
	require.Greater(t, ansi.StringWidth(full), 5, "sanity: wordmark is wider than the truncation width used below")

	truncated := Wordmark(&sty, 5)
	require.LessOrEqual(t, ansi.StringWidth(truncated), 5)
}

func TestAppendVersionEmptyReturnsLogoUnchanged(t *testing.T) {
	t.Parallel()

	require.Equal(t, "logo", appendVersion("logo", "", color.RGBA{}))
}

func TestAppendVersionAddsRightAlignedCaption(t *testing.T) {
	t.Parallel()

	logo := "XXXXX\nXXXXX"
	out := appendVersion(logo, "v1", color.RGBA{R: 0xff, A: 0xff})
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 3)
	require.Equal(t, "   v1", ansi.Strip(lines[2]))
}

func TestAppendVersionTruncatesLongVersion(t *testing.T) {
	t.Parallel()

	out := appendVersion("XXX", "way-too-long-version-string", color.RGBA{R: 0xff, A: 0xff})
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2)
	require.LessOrEqual(t, ansi.StringWidth(lines[1]), 3)
	require.Contains(t, lines[1], "…")
}

func TestTintColumnsPreservesSpacesAndTintsInk(t *testing.T) {
	t.Parallel()

	wall := "X X\nXXX"
	out := tintColumns(wall, color.RGBA{R: 0xff, A: 0xff}, color.RGBA{B: 0xff, A: 0xff})
	require.Equal(t, wall, ansi.Strip(out), "stripping color must round-trip to the original glyphs")
	require.Contains(t, out, "\x1b[", "ink cells must carry a color escape")
}
