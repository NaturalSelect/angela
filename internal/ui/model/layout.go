package model

import (
	"image"
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/charmbracelet/x/editor"
)

// currentModelSupportsImages reports whether the model the session is
// actually running accepts image attachments. It reads the memoized
// active agent rather than the global config: another session may be on
// a different model, and the file picker must offer what this one can
// take.
func (m *UI) currentModelSupportsImages() bool {
	active := m.activeAgent()
	return active != nil && active.CatwalkCfg.SupportsImages
}

// toggleCompactMode toggles compact mode between uiChat and uiChatCompact states.
func (m *UI) toggleCompactMode() tea.Cmd {
	m.forceCompactMode = !m.forceCompactMode

	err := m.com.Workspace.SetCompactMode(config.ScopeGlobal, m.forceCompactMode)
	if err != nil {
		return util.ReportError(err)
	}

	m.updateLayoutAndSize()

	return nil
}

// updateLayoutAndSize updates the layout and sizes of UI components.
func (m *UI) updateLayoutAndSize() {
	// Compact mode is now purely vertical: the layout no longer has a
	// horizontal branch, so only short terminals (or an explicit toggle)
	// need to drop the gaps between bands.
	if m.state == uiChat {
		m.isCompact = m.forceCompactMode || m.height < compactModeHeightBreakpoint
	}

	// First pass sizes components from the current textarea height.
	m.layout = m.generateLayout(m.width, m.height)
	prevHeight := m.textarea.Height()
	m.updateSize()

	// SetWidth can change textarea height due to soft-wrap recalculation.
	// If that happens, run one reconciliation pass with the new height.
	if m.textarea.Height() != prevHeight {
		m.layout = m.generateLayout(m.width, m.height)
		m.updateSize()
	}
}

// handleTextareaHeightChange checks whether the textarea height changed and,
// if so, recalculates the layout. When the chat is in follow mode it keeps
// the view scrolled to the bottom. The returned command, if non-nil, must be
// batched by the caller.
func (m *UI) handleTextareaHeightChange(prevHeight int) tea.Cmd {
	if m.textarea.Height() == prevHeight {
		return nil
	}
	m.updateLayoutAndSize()
	if m.state == uiChat && m.chat.Follow() {
		return m.chat.ScrollToBottomAndAnimate()
	}
	return nil
}

// updateTextarea updates the textarea for msg and then reconciles layout if
// the textarea height changed as a result.
func (m *UI) updateTextarea(msg tea.Msg) tea.Cmd {
	return m.updateTextareaWithPrevHeight(msg, m.textarea.Height())
}

// updateTextareaWithPrevHeight is for cases when the height of the layout may
// have changed.
//
// Particularly, it's for cases where the textarea changes before
// textarea.Update is called (for example, SetValue, Reset, and InsertRune). We
// pass the height from before those changes took place so we can compare
// "before" vs "after" sizing and recalculate the layout if the textarea grew
// or shrank.
func (m *UI) updateTextareaWithPrevHeight(msg tea.Msg, prevHeight int) tea.Cmd {
	ta, cmd := m.textarea.Update(msg)
	m.textarea = ta
	return tea.Batch(cmd, m.handleTextareaHeightChange(prevHeight))
}

// updateSize updates the sizes of UI components based on the current layout.
func (m *UI) updateSize() {
	// Set status width
	m.status.SetWidth(m.layout.status.Dx())

	m.chat.SetSize(m.layout.main.Dx(), m.layout.main.Dy())
	m.textarea.MaxHeight = m.maxTextareaContentHeight()
	m.textarea.SetWidth(m.layout.editor.Dx() - editorBoxBorders)
}

// Layout constants for the single unified skeleton.
const (
	headerHeight     = 1
	headerGapHeight  = 1
	mainBottomGap    = 1
	turnStatusHeight = 1
	// editorGapHeight separates the editor from the turn-status row.
	// Without it the bottom bands read as one crammed block.
	editorGapHeight = 1
	// appMarginX insets the content from each terminal edge so it sits on a
	// page instead of running into the frame.
	appMarginX = 2
	// logoWallHeight is the row count of the letterform wall: three rows of
	// letterforms plus the version line.
	logoWallHeight = 4
)

// generateLayout calculates the layout rectangles for all UI components. Every
// state shares one vertical stack; states differ only in which bands they use.
//
//	header
//	(gap)
//	main
//	(gap)
//	editor
//	turn status
//	help
func (m *UI) generateLayout(w, h int) uiLayout {
	area := image.Rect(0, 0, w, h)

	helpHeight := 1
	var helpKeyMap help.KeyMap = m
	if m.status != nil && m.status.ShowingAll() {
		for _, row := range helpKeyMap.FullHelp() {
			helpHeight = max(helpHeight, len(row))
		}
	}

	// App margins: one row top and bottom, one column each side.
	var appRect, helpRect image.Rectangle
	layout.Vertical(
		layout.Len(area.Dy()-helpHeight),
		layout.Fill(1),
	).Split(area).Assign(&appRect, &helpRect)
	appRect.Min.Y += 1
	appRect.Max.Y -= 1
	helpRect.Min.Y -= 1
	appRect.Min.X += appMarginX
	appRect.Max.X -= appMarginX

	l := uiLayout{area: area, status: helpRect}

	// Header band. Every state gets the one-line instrument bar. The
	// letterform wall belongs to the landing body, where it can be
	// composed with the menu and dropped on short terminals.
	header := headerHeight
	gap := headerGapHeight
	if m.isCompact {
		gap = 0
	}

	var headerRect, bodyRect image.Rectangle
	layout.Vertical(
		layout.Len(header),
		layout.Fill(1),
	).Split(appRect).Assign(&headerRect, &bodyRect)
	l.header = headerRect
	bodyRect.Min.Y += gap

	// Onboarding draws its flow as dialogs and owns no body bands.
	if m.state == uiOnboarding {
		return l
	}

	// Initialize has a main pane but no editor or turn status.
	if m.state == uiInitialize {
		l.main = bodyRect
		return l
	}

	editorHeight := m.editorHeight()

	// Chat reserves a turn-status row directly under the editor; landing
	// has no turn to report on.
	statusRows := 0
	if m.state == uiChat {
		statusRows = turnStatusHeight
	}

	// A blank row between the editor and the turn status, unless the
	// terminal is too short to spend one.
	editorGap := editorGapHeight
	if m.isCompact || statusRows == 0 {
		editorGap = 0
	}

	var mainRect, editorRect, turnStatusRect image.Rectangle
	layout.Vertical(
		layout.Len(max(0, bodyRect.Dy()-editorHeight-statusRows-editorGap)),
		layout.Len(editorHeight),
		layout.Fill(1),
	).Split(bodyRect).Assign(&mainRect, &editorRect, &turnStatusRect)
	turnStatusRect.Min.Y += editorGap

	// Main keeps a blank row above the editor so text never touches it.
	// Clamp rather than subtract unconditionally: main can already be at
	// its zero-height floor when the editor claims everything else, and an
	// unclamped subtraction would invert the rect (Max.Y < Min.Y).
	mainRect.Max.Y = max(mainRect.Max.Y-mainBottomGap, mainRect.Min.Y)

	l.main = mainRect
	l.editor = editorRect
	if statusRows > 0 {
		l.turnStatus = turnStatusRect
		l.sessionDetails = common.CenterRect(
			appRect,
			min(appRect.Dx(), sessionDetailsMaxWidth),
			min(sessionDetailsMaxHeight, appRect.Dy()),
		)
	}

	return l
}

// maxTextareaContentHeight returns the tallest the textarea's own content
// may grow to before the editor band would no longer fit the terminal.
// generateLayout lets main shrink to nothing but still hands the editor
// whatever editorHeight() asks for; without this cap DynamicHeight keeps
// growing the textarea past that point, so the box the terminal actually
// draws ends up shorter than the textarea's own reported height instead of
// the textarea switching to internal scrolling.
func (m *UI) maxTextareaContentHeight() int {
	if m.state != uiChat && m.state != uiLanding {
		return TextareaMaxHeight
	}

	helpHeight := 1
	var helpKeyMap help.KeyMap = m
	if m.status != nil && m.status.ShowingAll() {
		for _, row := range helpKeyMap.FullHelp() {
			helpHeight = max(helpHeight, len(row))
		}
	}

	header := headerHeight
	headerGap := headerGapHeight
	if m.isCompact {
		headerGap = 0
	}

	statusRows := 0
	if m.state == uiChat {
		statusRows = turnStatusHeight
	}
	editorGap := editorGapHeight
	if m.isCompact || statusRows == 0 {
		editorGap = 0
	}

	// Two app margin rows (top and bottom) plus everything else that
	// shares the body band with the editor: the header, its gap, and the
	// turn-status row with its gap. This mirrors generateLayout's bodyRect
	// and split math; main is the one allowed to shrink to nothing there.
	maxEditorHeight := m.height - helpHeight - 2 - header - headerGap - statusRows - editorGap

	return max(TextareaMinHeight, min(maxEditorHeight-editorHeightMargin, TextareaMaxHeight))
}

// editorHeight is the number of rows the editor band needs: the textarea plus
// its margin, or the active inline dialog's own height.
func (m *UI) editorHeight() int {
	if m.activeInline == nil {
		return m.textarea.Height() + editorHeightMargin
	}

	// The editor content width depends only on terminal width and layout
	// (not on editor height), so passing the current frame's width keeps
	// layout in sync with the width Draw will use, preventing flicker
	// during fast resize.
	width := m.editorContentWidth()
	if qf, ok := m.activeInline.(*dialog.QuestionForm); ok &&
		m.focus != uiFocusEditor && m.shouldCollapseQuestion(qf) {
		return qf.CollapsedHeight() + 1
	}
	return m.activeInline.Height(width)
}

// uiLayout defines the positioning of UI elements.
type uiLayout struct {
	// area is the overall available area.
	area uv.Rectangle

	// header is the wordmark band: the full letterform wall on landing
	// and setup, a single line in chat.
	header uv.Rectangle

	// main is the area for the main pane. (e.x chat, configure, landing)
	main uv.Rectangle

	// editor is the area for the editor pane.
	editor uv.Rectangle

	// turnStatus is the single row under the editor reporting what the
	// current turn is doing. Empty outside chat.
	turnStatus uv.Rectangle

	// status is the area for the status view.
	status uv.Rectangle

	// sessionDetails is the area for the session details overlay.
	sessionDetails uv.Rectangle
}

func (m *UI) openEditor(value string) tea.Cmd {
	tmpfile, err := os.CreateTemp("", "msg_*.md")
	if err != nil {
		return util.ReportError(err)
	}
	tmpPath := tmpfile.Name()
	defer tmpfile.Close() //nolint:errcheck
	if _, err := tmpfile.WriteString(value); err != nil {
		return util.ReportError(err)
	}
	cmd, err := editor.Command(
		"angela",
		tmpPath,
		editor.AtPosition(
			m.textarea.Line()+1,
			m.textarea.Column()+1,
		),
	)
	if err != nil {
		return util.ReportError(err)
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer func() {
			_ = os.Remove(tmpPath)
		}()

		if err != nil {
			return util.ReportError(err)
		}
		content, err := os.ReadFile(tmpPath)
		if err != nil {
			return util.ReportError(err)
		}
		if len(content) == 0 {
			return util.ReportWarn("Message is empty")
		}
		return openEditorMsg{
			Text: strings.TrimSpace(string(content)),
		}
	})
}

// setEditorPrompt configures the textarea prompt function. The prompt is a
// two-column gutter: marker, gap.
func (m *UI) setEditorPrompt(mode permission.PermissionMode) {
	m.textarea.SetPromptFunc(editorPromptWidth, m.editorPromptFunc(mode))
}

// editorPromptFunc draws the editor gutter for one visual line. The marker
// only appears on the first line, and carries the input mode in its color —
// as does the box border, so no separate accent rail is needed.
func (m *UI) editorPromptFunc(mode permission.PermissionMode) func(textarea.PromptInfo) string {
	t := m.com.Styles
	railStyle := t.Editor.Rail
	switch {
	case m.bangMode:
		railStyle = t.Editor.RailBang
	case mode == permission.ModeYolo:
		railStyle = t.Editor.RailYolo
		if m.sandboxActive {
			railStyle = t.Editor.RailYoloSandbox
		}
	case mode == permission.ModeAutoAcceptEdits:
		railStyle = t.Editor.RailAutoAcceptEdits
	}

	return func(info textarea.PromptInfo) string {
		if info.LineNumber != 0 {
			return "  "
		}
		if !info.Focused {
			return t.Editor.PromptMarkerBlurred.Render(editorPromptGlyph)
		}
		return railStyle.Render(editorPromptGlyph)
	}
}

// handleWindowSize handles tea.WindowSizeMsg.
func (m *UI) handleWindowSize(msg tea.WindowSizeMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.width, m.height = msg.Width, msg.Height
	// Suppress the chat's full-height scan during the resize so a drag
	// only reflows visible items; it settles (and recomputes) shortly
	// after the last resize event.
	if m.state == uiChat {
		cmds = append(cmds, m.chat.BeginResize())
	}
	m.updateLayoutAndSize()
	if m.state == uiChat && m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := m.applyImageCaps(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return cmds
}
