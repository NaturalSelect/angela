package dialog

import (
	"cmp"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
)

// FrameSpec declares how big a dialog wants to be. It is intent, not
// arithmetic: the dialog states its bounds and [Frame.Measure] resolves them
// against the screen. Nothing outside this file should subtract a border or a
// frame size to arrive at a dialog dimension.
//
// Width is driven by MaxWidth, or by WidthRatio when the dialog should track
// the terminal (a diff needs room a fixed cap would waste). Height works the
// same way, plus [Frame.FitHeight] for dialogs that shrink to their content.
type FrameSpec struct {
	Title     string
	TitleInfo string

	// MinWidth raises the floor; zero means no floor.
	MinWidth int
	// MaxWidth caps the width; zero means defaultDialogMaxWidth.
	MaxWidth int
	// WidthRatio, when positive, takes that share of the screen width,
	// still capped by MaxWidth.
	WidthRatio float64

	// MinHeight raises the floor; zero means no floor.
	MinHeight int
	// MaxHeight caps the height; zero means defaultDialogHeight.
	MaxHeight int
	// HeightRatio, when positive, takes that share of the screen height,
	// still capped by MaxHeight.
	HeightRatio float64

	// Gap is the blank line count between rendered parts.
	Gap int
	// Onboarding renders unframed at the bottom left instead of centered.
	Onboarding bool
	// Fullscreen takes the whole area, ignoring every bound above.
	Fullscreen bool
}

// FrameMetrics is a resolved size. ContentWidth and ContentHeight are the room
// left inside the border and padding, and are what every piece of dialog
// content must be laid out against.
type FrameMetrics struct {
	Width         int
	Height        int
	ContentWidth  int
	ContentHeight int
}

// Frame owns dialog sizing, the shared chrome (title, gap, help footer), and
// placement on screen.
type Frame struct {
	t    *styles.Styles
	spec FrameSpec
}

// NewFrame creates a frame for a dialog.
func NewFrame(t *styles.Styles, spec FrameSpec) *Frame {
	return &Frame{t: t, spec: spec}
}

// SetTitle updates the title and the info shown beside it.
func (f *Frame) SetTitle(title, info string) {
	f.spec.Title = title
	f.spec.TitleInfo = info
}

// SetFullscreen switches the frame to or from filling the whole area. Dialogs
// use it when the terminal is too small for their normal bounds.
func (f *Frame) SetFullscreen(v bool) { f.spec.Fullscreen = v }

// Spec returns the frame's current spec.
func (f *Frame) Spec() FrameSpec { return f.spec }

// Measure resolves the spec against the drawable area.
func (f *Frame) Measure(area uv.Rectangle) FrameMetrics {
	if f.spec.Fullscreen {
		return f.metrics(area.Dx(), area.Dy())
	}
	view := f.t.Dialog.View

	width := f.resolveSpan(f.spec.MaxWidth, f.spec.WidthRatio, area.Dx(), defaultDialogMaxWidth)
	width = clampSpan(width, f.spec.MinWidth, area.Dx()-view.GetHorizontalBorderSize())

	height := f.resolveSpan(f.spec.MaxHeight, f.spec.HeightRatio, area.Dy(), defaultDialogHeight)
	height = clampSpan(height, f.spec.MinHeight, area.Dy()-view.GetVerticalBorderSize())

	return f.metrics(width, height)
}

// resolveSpan turns a cap and a ratio into a target span. A ratio without a
// cap tracks the screen unbounded, which is what a diff wants; a cap without a
// ratio is a fixed size; both together mean track the screen up to the cap.
func (f *Frame) resolveSpan(maxSpan int, ratio float64, available, fallback int) int {
	if ratio <= 0 {
		return cmp.Or(maxSpan, fallback)
	}
	span := int(float64(available) * ratio)
	if maxSpan > 0 {
		span = min(span, maxSpan)
	}
	return span
}

// FitHeight resolves the width as [Frame.Measure] does but lets the height
// follow the content, bounded by the spec and the area. Dialogs whose list is
// usually short use it so they do not render a half-empty box.
func (f *Frame) FitHeight(area uv.Rectangle, desiredHeight int) FrameMetrics {
	m := f.Measure(area)
	if f.spec.Fullscreen {
		return m
	}
	avail := area.Dy() - f.t.Dialog.View.GetVerticalBorderSize()
	height := clampSpan(min(m.Height, desiredHeight), f.spec.MinHeight, avail)
	return f.metrics(m.Width, height)
}

// MeasureWidth resolves a width the caller measured from its own content,
// clamping it to the spec and the area. It serves dialogs sized by their
// widest row rather than by a bound.
func (f *Frame) MeasureWidth(area uv.Rectangle, contentWidth int) int {
	view := f.t.Dialog.View
	maxW := cmp.Or(f.spec.MaxWidth, defaultDialogMaxWidth)
	return clampSpan(min(contentWidth, maxW), f.spec.MinWidth,
		area.Dx()-view.GetHorizontalBorderSize())
}

// metrics derives the inner dimensions from an outer size.
func (f *Frame) metrics(width, height int) FrameMetrics {
	view := f.t.Dialog.View
	return FrameMetrics{
		Width:         width,
		Height:        height,
		ContentWidth:  max(0, width-view.GetHorizontalFrameSize()),
		ContentHeight: max(0, height-view.GetVerticalFrameSize()),
	}
}

// clampSpan bounds v to [lo, hi], never returning a negative span. hi wins
// over lo when the area cannot even satisfy the floor.
func clampSpan(v, lo, hi int) int {
	hi = max(0, hi)
	return max(0, min(max(v, lo), hi))
}

// ListHeightOffset is the vertical room the chrome takes from a dialog whose
// body is a filterable list: title, input, help, and the view frame. The
// remainder is the list viewport.
func (f *Frame) ListHeightOffset() int {
	t := f.t
	return t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
}

// SizeList sizes a list to the frame and reports the dimensions the caller
// needs for the scrollbar and for hiding info columns.
func (f *Frame) SizeList(l sizer, m FrameMetrics) (listHeight, listTotalHeight, listWidth int) {
	return sizeDialogList(f.t, l, m.ContentWidth, m.Height)
}

// JoinScrollbar puts a scrollbar beside view when the content overflows.
func (f *Frame) JoinScrollbar(view string, height, contentSize, viewportSize, offset int) string {
	return joinScrollbar(f.t, view, height, contentSize, viewportSize, offset)
}

// RenderHelp renders the keybind footer at the frame's content width.
func (f *Frame) RenderHelp(h *help.Model, km help.KeyMap, contentWidth int) string {
	return renderDialogHelp(f.t, h, km, contentWidth)
}

// InputTextWidth is the text width for an input sitting in this frame.
func (f *Frame) InputTextWidth(input textinput.Model, contentWidth int) int {
	return dialogInputTextWidth(f.t, input, contentWidth)
}

// Render builds the dialog view: title, parts separated by the spec's gap, and
// the help footer.
func (f *Frame) Render(m FrameMetrics, parts []string, helpLine string) string {
	rc := f.Context(m)
	for _, p := range parts {
		rc.AddPart(p)
	}
	rc.Help = helpLine
	return rc.Render()
}

// Context returns a [RenderContext] preloaded from the frame, for dialogs that
// need to override a style before rendering.
func (f *Frame) Context(m FrameMetrics) *RenderContext {
	rc := NewRenderContext(f.t, m.Width)
	rc.Title = f.spec.Title
	rc.TitleInfo = f.spec.TitleInfo
	rc.Gap = f.spec.Gap
	rc.IsOnboarding = f.spec.Onboarding
	return rc
}

// Draw places a rendered view on screen and returns the adjusted cursor.
// Onboarding dialogs sit at the bottom left; every other dialog is centered.
func (f *Frame) Draw(scr uv.Screen, area uv.Rectangle, view string, cur *tea.Cursor) *tea.Cursor {
	if f.spec.Onboarding {
		cur = adjustOnboardingInputCursor(f.t, cur)
		DrawOnboardingCursor(scr, area, view, cur)
		return cur
	}
	DrawCenterCursor(scr, area, view, cur)
	return cur
}
