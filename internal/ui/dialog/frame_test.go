package dialog

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testArea(w, h int) uv.Rectangle {
	return uv.Rect(0, 0, w, h)
}

func testStyles() *styles.Styles {
	t := styles.CharmtonePantera()
	return &t
}

// A dialog must never be wider than the screen, however large its declared
// bounds: an overflowing frame corrupts every row it touches.
func TestMeasureNeverExceedsArea(t *testing.T) {
	t.Parallel()
	sty := testStyles()

	specs := map[string]FrameSpec{
		"default":     {},
		"wide cap":    {MaxWidth: 180, MaxHeight: 60},
		"ratio":       {WidthRatio: 0.8, MaxWidth: 180, HeightRatio: 0.8},
		"with floors": {MinWidth: 70, MinHeight: 20, MaxWidth: 100},
	}

	for name, spec := range specs {
		for _, size := range [][2]int{{20, 6}, {40, 10}, {80, 24}, {200, 60}} {
			f := NewFrame(sty, spec)
			m := f.Measure(testArea(size[0], size[1]))

			require.LessOrEqual(t, m.Width, size[0], "%s at %dx%d", name, size[0], size[1])
			require.LessOrEqual(t, m.Height, size[1], "%s at %dx%d", name, size[0], size[1])
			require.GreaterOrEqual(t, m.Width, 0, name)
			require.GreaterOrEqual(t, m.ContentWidth, 0, name)
			require.GreaterOrEqual(t, m.ContentHeight, 0, name)
		}
	}
}

// A declared floor must not win over a screen that cannot hold it. This is the
// case that produces a dialog wider than the terminal.
func TestMinBoundsYieldToASmallScreen(t *testing.T) {
	t.Parallel()

	f := NewFrame(testStyles(), FrameSpec{MinWidth: 70, MinHeight: 20})
	m := f.Measure(testArea(30, 8))
	require.LessOrEqual(t, m.Width, 30)
	require.LessOrEqual(t, m.Height, 8)
}

func TestMeasureRespectsCapsAndRatios(t *testing.T) {
	t.Parallel()
	sty := testStyles()
	border := sty.Dialog.View.GetHorizontalBorderSize()

	// A cap holds on a screen with room to spare.
	capped := NewFrame(sty, FrameSpec{MaxWidth: 50}).Measure(testArea(200, 60))
	require.Equal(t, 50, capped.Width)

	// A ratio tracks the screen until it hits the cap.
	f := NewFrame(sty, FrameSpec{WidthRatio: 0.5, MaxWidth: 180})
	require.Equal(t, 100, f.Measure(testArea(200, 60)).Width)
	require.Equal(t, 180, f.Measure(testArea(400, 60)).Width)

	// Absent a cap the default applies.
	require.Equal(t, defaultDialogMaxWidth, NewFrame(sty, FrameSpec{}).Measure(testArea(200, 60)).Width)

	// A ratio with no cap tracks the screen: a diff must be allowed to grow
	// past the default, which is a bound for fixed-size dialogs only.
	uncapped := NewFrame(sty, FrameSpec{WidthRatio: 0.8, HeightRatio: 0.8})
	big := uncapped.Measure(testArea(200, 60))
	require.Equal(t, 160, big.Width)
	require.Equal(t, 48, big.Height)

	// The screen is the last word.
	tight := NewFrame(sty, FrameSpec{MaxWidth: 200}).Measure(testArea(40, 10))
	require.Equal(t, 40-border, tight.Width)
}

func TestFullscreenTakesTheArea(t *testing.T) {
	t.Parallel()

	f := NewFrame(testStyles(), FrameSpec{MaxWidth: 50, MaxHeight: 10, Fullscreen: true})
	m := f.Measure(testArea(120, 40))
	require.Equal(t, 120, m.Width)
	require.Equal(t, 40, m.Height)
}

// FitHeight is what keeps a short list from rendering as a half-empty box.
func TestFitHeightFollowsContentWithinBounds(t *testing.T) {
	t.Parallel()
	sty := testStyles()
	f := NewFrame(sty, FrameSpec{MinHeight: 8, MaxHeight: 16})
	area := testArea(100, 40)

	require.Equal(t, 12, f.FitHeight(area, 12).Height, "content within bounds is honored")
	require.Equal(t, 8, f.FitHeight(area, 3).Height, "floor holds")
	require.Equal(t, 16, f.FitHeight(area, 100).Height, "cap holds")
	require.LessOrEqual(t, f.FitHeight(testArea(100, 6), 100).Height, 6, "screen wins")
}

// ContentWidth is what dialogs lay their content out against; if it does not
// match the frame the border re-wraps the content.
func TestContentDimensionsMatchTheFrame(t *testing.T) {
	t.Parallel()
	sty := testStyles()
	m := NewFrame(sty, FrameSpec{MaxWidth: 70, MaxHeight: 20}).Measure(testArea(200, 60))

	require.Equal(t, 70-sty.Dialog.View.GetHorizontalFrameSize(), m.ContentWidth)
	require.Equal(t, 20-sty.Dialog.View.GetVerticalFrameSize(), m.ContentHeight)
}

// The rendered box must be exactly the measured width on every row, including
// rows whose content is far shorter or far longer than the frame.
func TestRenderedViewMatchesMeasuredWidth(t *testing.T) {
	t.Parallel()
	sty := testStyles()

	for _, area := range []uv.Rectangle{testArea(80, 24), testArea(120, 40), testArea(40, 12)} {
		f := NewFrame(sty, FrameSpec{Title: "Test Dialog", MaxWidth: 70, Gap: 1})
		m := f.Measure(area)

		view := f.Render(m, []string{
			"short",
			strings.Repeat("wide content ", 40),
		}, "")

		require.Equal(t, m.Width, lipgloss.Width(view),
			"rendered width must equal measured width at area %v", area)
		for i, line := range strings.Split(view, "\n") {
			require.Equal(t, m.Width, ansi.StringWidth(line),
				"row %d ragged at area %v: %q", i, area, ansi.Strip(line))
		}
	}
}

// Onboarding renders unframed; the surrounding border would otherwise be drawn
// twice once the onboarding flow places it itself.
func TestOnboardingRendersUnframed(t *testing.T) {
	t.Parallel()
	sty := testStyles()
	area := testArea(80, 24)

	framed := NewFrame(sty, FrameSpec{Title: "T", MaxWidth: 60})
	onboarding := NewFrame(sty, FrameSpec{Title: "T", MaxWidth: 60, Onboarding: true})

	f := framed.Render(framed.Measure(area), []string{"body"}, "")
	o := onboarding.Render(onboarding.Measure(area), []string{"body"}, "")

	require.Greater(t, lipgloss.Height(f), lipgloss.Height(o),
		"the framed view carries a border the onboarding one does not")
}

// The list viewport is what is left after the chrome; if the offset is wrong
// the list overflows the border or leaves a dead band.
func TestListHeightOffsetLeavesRoomForChrome(t *testing.T) {
	t.Parallel()
	sty := testStyles()
	f := NewFrame(sty, FrameSpec{MaxWidth: 70, MaxHeight: 20})
	m := f.Measure(testArea(200, 60))

	offset := f.ListHeightOffset()
	require.Greater(t, offset, 0)
	require.Less(t, offset, m.Height, "chrome must not consume the whole dialog")
}
