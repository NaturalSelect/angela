package model

import (
	"image"
	"math/rand"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/home"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/screen"
	"github.com/charmbracelet/x/ansi"
)

// drawHeader draws the header section of the UI.
func (m *UI) drawHeader(scr uv.Screen, area uv.Rectangle) {
	m.header.drawHeader(scr, area, m.sessionTrail(), area.Dx(), m.lspErrorCount())
}

// drawEditor draws whatever currently owns the editor rect: an inline dialog
// (question form, etc.) when one is active, otherwise the prompt textarea.
func (m *UI) drawEditor(scr uv.Screen, layout uiLayout) {
	if m.activeInline == nil {
		m.drawPromptBox(scr, layout.editor)
		m.inlineCursor = nil
		return
	}

	m.activeInline.SetFocused(m.focus == uiFocusEditor)
	if qf, ok := m.activeInline.(*dialog.QuestionForm); ok &&
		m.focus != uiFocusEditor && m.shouldCollapseQuestion(qf) {
		qf.DrawCollapsed(scr, layout.editor)
		m.inlineCursor = nil
		return
	}
	m.inlineCursor = m.activeInline.Draw(scr, layout.editor)
}

// Draw implements [uv.Drawable] and draws the UI model.
func (m *UI) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	layout := m.generateLayout(area.Dx(), area.Dy())

	if m.layout != layout {
		m.layout = layout
		m.updateSize()
	}

	// Clear the screen first
	screen.Clear(scr)

	switch m.state {
	case uiOnboarding:
		m.drawHeader(scr, layout.header)

		// NOTE: Onboarding flow will be rendered as dialogs below, but
		// positioned at the bottom left of the screen.

	case uiInitialize:
		m.drawHeader(scr, layout.header)

		main := uv.NewStyledString(m.initializeView())
		main.Draw(scr, layout.main)

	case uiLanding:
		m.drawHeader(scr, layout.header)
		main := uv.NewStyledString(m.landingView())
		main.Draw(scr, layout.main)

		m.drawEditor(scr, layout)

	case uiChat:
		m.drawHeader(scr, layout.header)
		m.chat.Draw(scr, layout.main)
		m.drawEditor(scr, layout)

		uv.NewStyledString(m.renderTurnStatus(layout.turnStatus.Dx())).
			Draw(scr, layout.turnStatus)

		// Draw details overlay when open
		if m.detailsOpen {
			m.drawSessionDetails(scr, layout.sessionDetails)
		}
	}

	isOnboarding := m.state == uiOnboarding

	// Add status and help layer
	m.status.SetHideHelp(isOnboarding)
	m.status.Draw(scr, layout.status)

	// Draw completions popup if open
	if !isOnboarding && m.completionsOpen && m.completions.HasItems() {
		w, h := m.completions.Size()
		x := m.completionsPositionStart.X
		y := m.completionsPositionStart.Y - h

		screenW := area.Dx()
		if x+w > screenW {
			x = screenW - w
		}
		x = max(0, x)
		y = max(0, y+1) // Offset for attachments row

		rect := image.Rectangle{
			Min: image.Pt(x, y),
			Max: image.Pt(x+w, y+h),
		}
		m.completionsRect = rect
		completionsView := uv.NewStyledString(m.completions.Render())
		completionsView.Draw(scr, rect)
	} else {
		m.completionsRect = image.Rectangle{}
	}

	// Debugging rendering (visually see when the tui rerenders)
	if os.Getenv("ANGELA_UI_DEBUG") == "true" {
		debugView := lipgloss.NewStyle().Background(lipgloss.ANSIColor(rand.Intn(256))).Width(4).Height(2)
		debug := uv.NewStyledString(debugView.String())
		debug.Draw(scr, image.Rectangle{
			Min: image.Pt(4, 1),
			Max: image.Pt(8, 3),
		})
	}

	// This needs to come last to overlay on top of everything. We always pass
	// the full screen bounds because the dialogs will position themselves
	// accordingly.
	if m.dialog.HasDialogs() {
		return m.dialog.Draw(scr, scr.Bounds())
	}

	switch m.focus {
	case uiFocusEditor:
		if m.layout.editor.Dy() <= 0 {
			// Don't show cursor if editor is not visible
			return nil
		}
		if m.detailsOpen {
			// Don't show cursor if details overlay is open
			return nil
		}

		if m.activeInline != nil {
			if cur := m.inlineCursor; cur != nil {
				cur.X += m.layout.editor.Min.X // Adjust for app margins
				cur.Y += m.layout.editor.Min.Y // Inline editor draws from area top
				return cur
			}
			return nil
		}

		if m.textarea.Focused() {
			cur := m.textarea.Cursor()
			// App margin plus the box's left border.
			cur.X += m.layout.editor.Min.X + 1
			// Attachments row plus the box's top border.
			cur.Y += m.layout.editor.Min.Y + 2
			return cur
		}
	}
	return nil
}

// View renders the UI model's view.
func (m *UI) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if !m.isTransparent {
		v.BackgroundColor = m.com.Styles.Background
	}
	if m.activeInline != nil {
		v.MouseMode = tea.MouseModeAllMotion
	} else {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.ReportFocus = m.caps.ReportFocusEvents
	v.WindowTitle = "angela " + home.Short(m.com.Workspace.WorkingDir())

	canvas := uv.NewScreenBuffer(m.width, m.height)
	v.Cursor = m.Draw(canvas, canvas.Bounds())

	content := strings.ReplaceAll(canvas.Render(), "\r\n", "\n") // normalize newlines
	contentLines := strings.Split(content, "\n")
	for i, line := range contentLines {
		// Trim trailing spaces for concise rendering
		contentLines[i] = strings.TrimRight(line, " ")
	}

	content = strings.Join(contentLines, "\n")

	v.Content = content
	if m.progressBarEnabled && m.sendProgressBar && m.isSessionWorking() {
		// HACK: use a random percentage to prevent ghostty from hiding it
		// after a timeout.
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, rand.Intn(100))
	}

	return v
}

// cancelHint returns the esc binding with the wording the current turn state
// calls for: popping the queue, confirming a second press, or plain cancel.
func (m *UI) cancelHint() key.Binding {
	b := m.keyMap.Chat.Cancel
	switch {
	case m.isCanceling:
		b.SetHelp("esc", "press again to cancel")
	case m.promptQueue > 0:
		b.SetHelp("esc", "pop queue")
	}
	return b
}

// commandsHint returns the command-palette binding, advertising the bare "/"
// shortcut only where typing it would actually open the palette.
func (m *UI) commandsHint() key.Binding {
	b := m.keyMap.Commands
	if m.focus == uiFocusEditor && m.textarea.Value() == "" {
		b.SetHelp("/ or ctrl+p", "commands")
	}
	return b
}

// tabHint returns the focus-switch binding labeled with where it goes.
func (m *UI) tabHint() key.Binding {
	b := m.keyMap.Tab
	if m.focus == uiFocusEditor {
		b.SetHelp("tab", "focus chat")
	} else {
		b.SetHelp("tab", "focus editor")
	}
	return b
}

// shortHelpLimit caps the help line so it stays one row on a narrow terminal.
// Everything cut from here is still reachable through ctrl+p and ctrl+g.
const shortHelpLimit = 4

// shortHelpWidth measures the line the way [help.Model] renders it: each hint
// is "key desc", hints joined by a three-cell separator.
func shortHelpWidth(binds []key.Binding) int {
	var w, n int
	for _, b := range binds {
		if !b.Enabled() {
			continue
		}
		if n > 0 {
			w += 3
		}
		h := b.Help()
		w += ansi.StringWidth(h.Key) + 1 + ansi.StringWidth(h.Desc)
		n++
	}
	return w
}

// helpWidth is the room the status bar actually gives the help line.
func (m *UI) helpWidth() int {
	w := m.layout.status.Dx()
	if w <= 0 {
		w = m.width
	}
	st := m.com.Styles.Status.Help
	return w - st.GetPaddingLeft() - st.GetPaddingRight()
}

// fitShortHelp drops optional hints until the line fits one row. The help
// component truncates unpredictably once the budget runs out — at 80 columns
// it can emit a 106-cell line — so the line has to arrive already short
// enough. The trailing two hints go last: quit and more are what a stuck user
// reaches for.
func fitShortHelp(binds []key.Binding, width int) []key.Binding {
	if width <= 0 {
		return binds
	}
	for len(binds) > 2 && shortHelpWidth(binds) > width {
		binds = slices.Delete(binds, len(binds)-3, len(binds)-2)
	}
	return binds
}

// ShortHelp implements [help.KeyMap].
func (m *UI) ShortHelp() []key.Binding {
	// When an inline editor is active, show its help.
	if m.activeInline != nil {
		return m.activeInline.ShortHelp()
	}

	k := &m.keyMap
	if m.state == uiInitialize {
		return []key.Binding{k.Quit}
	}

	var binds []key.Binding
	if m.state == uiChat && m.isSessionBusy() {
		binds = append(binds, m.cancelHint())
	}

	binds = append(binds, m.tabHint(), m.commandsHint())

	switch m.focus {
	case uiFocusEditor:
		binds = append(binds, k.Editor.Newline)
	case uiFocusMain:
		binds = append(binds, k.Chat.UpDown, k.Chat.Copy)
		if _, ok := m.selectedAgentTool(); ok {
			binds = append(binds, k.Chat.OpenSubSession)
		}
	}

	if m.hasSession() {
		details := k.Chat.Details
		details.SetHelp("ctrl+d", "details")
		binds = append(binds, details)
	}
	binds = append(binds, k.Quit, k.Help)

	if len(binds) > shortHelpLimit {
		binds = append(binds[:shortHelpLimit-2:shortHelpLimit-2], k.Quit, k.Help)
	}
	return fitShortHelp(binds, m.helpWidth())
}

// FullHelp implements [help.KeyMap]. Four fixed groups so the layout does not
// reshuffle as session state changes.
func (m *UI) FullHelp() [][]key.Binding {
	// When an inline editor is active, show its help.
	if m.activeInline != nil {
		return [][]key.Binding{m.activeInline.ShortHelp()}
	}

	k := &m.keyMap
	if m.state == uiInitialize {
		return [][]key.Binding{{k.Quit}}
	}

	less := k.Help
	less.SetHelp("ctrl+g", "less")

	navigation := []key.Binding{
		k.Chat.UpDown,
		k.Chat.UpDownOneItem,
		k.Chat.PageUp,
		k.Chat.PageDown,
		k.Chat.Copy,
		k.Chat.Expand,
	}

	editor := []key.Binding{
		m.tabHint(),
		k.Editor.Newline,
		k.Editor.MentionAgent,
		k.Editor.MentionFile,
		k.Editor.MentionSkill,
		k.Editor.OpenEditor,
	}
	if m.currentModelSupportsImages() {
		editor = append(editor, k.Editor.AddImage, k.Editor.PasteImage)
	}
	if len(m.attachments.List()) > 0 {
		editor = append(editor, k.Editor.AttachmentDeleteMode)
	}

	session := []key.Binding{
		m.commandsHint(),
		k.Chat.NewSession,
		k.Chat.Details,
		k.Sessions,
		k.Models,
		k.CycleVariant,
	}

	app := []key.Binding{k.CyclePermissionMode, k.Suspend, less, k.Quit}
	if m.state == uiChat && m.isSessionBusy() {
		app = append([]key.Binding{m.cancelHint()}, app...)
	}

	return [][]key.Binding{navigation, editor, session, app}
}
