package dialog

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestViewer(t *testing.T, spec ViewerSpec) *Viewer {
	t.Helper()
	return NewViewer(&common.Common{Styles: testStyles()}, spec)
}

func drawViewer(t *testing.T, d *Viewer, w, h int) string {
	t.Helper()
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))
	return ansi.Strip(scr.Render())
}

func longViewerContent() string {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line " + strings.Repeat("x", i%5)
	}
	return strings.Join(lines, "\n")
}

func TestViewer_ID(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t"})
	require.Equal(t, ViewerID, d.ID())
}

func TestViewer_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t"})
	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestViewer_Draw_PlainText verifies the title, its info and the content
// render as given when Markdown is off.
func TestViewer_Draw_PlainText(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "rpt_aaaaaaaa", TitleInfo: "explore", Content: "hello **not bold**"})
	view := drawViewer(t, d, 80, 24)
	require.Contains(t, view, "rpt_aaaaaaaa")
	require.Contains(t, view, "explore")
	require.Contains(t, view, "hello **not bold**")
}

// TestViewer_Draw_Markdown verifies Markdown content goes through the
// renderer, so the markup itself is gone from the screen.
func TestViewer_Draw_Markdown(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t", Content: "# Findings\n\nThe parser is **here**.", Markdown: true})
	view := drawViewer(t, d, 80, 24)
	require.Contains(t, view, "Findings")
	require.Contains(t, view, "The parser is")
	require.NotContains(t, view, "**here**")
}

// TestViewer_Scroll verifies the keys and the wheel move the viewport
// within bounds, and that top and bottom jump to the ends.
func TestViewer_Scroll(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t", Content: longViewerContent()})
	drawViewer(t, d, 80, 24)
	require.Zero(t, d.viewport.YOffset())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, d.viewport.YOffset())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Zero(t, d.viewport.YOffset())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Positive(t, d.viewport.YOffset())

	d.HandleMsg(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.True(t, d.viewport.AtBottom())

	d.HandleMsg(tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.True(t, d.viewport.AtTop())

	d.HandleMsg(common.CoalescedWheelMsg{Mouse: tea.Mouse{Button: tea.MouseWheelDown}, DeltaY: 1})
	require.Positive(t, d.viewport.YOffset())
}

// TestViewer_Draw_ShowsLaterContentAfterScrolling verifies scrolling
// changes what is on screen, not just the offset.
func TestViewer_Draw_ShowsLaterContentAfterScrolling(t *testing.T) {
	t.Parallel()

	content := "first-line\n" + strings.Repeat("filler\n", 98) + "last-line"
	d := newTestViewer(t, ViewerSpec{Title: "t", Content: content})
	view := drawViewer(t, d, 80, 24)
	require.Contains(t, view, "first-line")
	require.NotContains(t, view, "last-line")

	d.HandleMsg(tea.KeyPressMsg{Code: 'G', Text: "G"})
	view = drawViewer(t, d, 80, 24)
	require.Contains(t, view, "last-line")
	require.NotContains(t, view, "first-line")
}

// TestViewer_Copy verifies the copy key yields a command; the clipboard
// text itself is the spec's raw content, not the rendered form.
func TestViewer_Copy(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t", Content: "# raw", Markdown: true})
	action := d.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"})
	cmd, ok := action.(ActionCmd)
	require.True(t, ok, "expected ActionCmd, got %T", action)
	require.NotNil(t, cmd.Cmd)
}

// TestViewer_Draw_FitsSmallScreen verifies the rendered dialog never
// grows past the area it was given.
func TestViewer_Draw_FitsSmallScreen(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t", Content: longViewerContent(), Markdown: true})
	const w, h = 40, 12
	view := drawViewer(t, d, w, h)
	lines := strings.Split(view, "\n")
	require.LessOrEqual(t, len(lines), h)
	for _, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), w)
	}
}

func TestViewer_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	d := newTestViewer(t, ViewerSpec{Title: "t"})
	require.Len(t, d.ShortHelp(), 3)

	var total int
	for _, row := range d.FullHelp() {
		total += len(row)
	}
	require.Equal(t, 8, total)
}
