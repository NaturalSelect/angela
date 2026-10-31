package model

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseRelease handles tea.MouseReleaseMsg. skipTail reports that
// Update must return right away instead of running its shared tail.
func (m *UI) handleMouseRelease(msg tea.MouseReleaseMsg) (cmds []tea.Cmd, skipTail bool) {
	// Pass mouse events to dialogs first if any are open.
	if m.dialog.HasDialogs() {
		if cmd := m.handleDialogMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return cmds, true
	}

	switch m.state {
	case uiChat:
		x, y := msg.X, msg.Y
		// Adjust for chat area position
		x -= m.layout.main.Min.X
		y -= m.layout.main.Min.Y
		if m.chat.HandleMouseUp(x, y) && m.chat.HasHighlight() {
			cmds = append(cmds, tea.Tick(doubleClickThreshold, func(t time.Time) tea.Msg {
				if time.Since(m.lastClickTime) >= doubleClickThreshold {
					return copyChatHighlightMsg{}
				}
				return nil
			}))
		}
	}
	return cmds, false
}

// handleCoalescedWheel handles common.CoalescedWheelMsg. skipTail reports
// that Update must return right away instead of running its shared tail.
func (m *UI) handleCoalescedWheel(msg common.CoalescedWheelMsg) (cmds []tea.Cmd, skipTail bool) {
	// Route wheel events to active inline editor only when the
	// mouse is over the editor area, so scrolling over the chat
	// still scrolls the chat.
	if m.activeInline != nil && image.Pt(msg.Mouse.X, msg.Mouse.Y).In(m.layout.editor) {
		if we, ok := m.activeInline.(common.WheelScrollable); ok {
			we.HandleWheel(msg.DeltaX, msg.DeltaY)
			return cmds, true
		}
	}

	// Pass mouse events to dialogs first if any are open.
	if m.dialog.HasDialogs() {
		if cmd := m.handleDialogMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return cmds, true
	}

	// Otherwise handle mouse wheel for chat. Use the coalesced delta
	// directly as the line count. Terminals like Ghostty send DeltaY=3
	// per physical wheel tick (matching their native scrollback), while
	// others send DeltaY=1.
	switch m.state {
	case uiChat:
		if msg.DeltaX != 0 {
			m.chat.ScrollSelectedShellHorizontal(int(msg.DeltaX))
		}
		lines := int(msg.DeltaY)
		if lines == 0 {
			break
		}
		if cmd := m.chat.ScrollByAndAnimate(lines); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !m.chat.SelectedItemInView() {
			if lines > 0 && m.chat.AtBottom() {
				m.chat.SelectLast()
			} else {
				m.chat.SelectClosestInView()
			}
		}
	}
	return cmds, false
}

// handleMouseMotion handles tea.MouseMotionMsg. skipTail reports that
// Update must return right away instead of running its shared tail.
func (m *UI) handleMouseMotion(msg tea.MouseMotionMsg) (cmds []tea.Cmd, skipTail bool) {
	// Pass mouse events to dialogs first if any are open.
	if m.dialog.HasDialogs() {
		if cmd := m.handleDialogMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return cmds, true
	}

	// Track hover position for inline editors.
	if m.activeInline != nil {
		if m.hoverX != msg.X || m.hoverY != msg.Y {
			m.hoverX = msg.X
			m.hoverY = msg.Y
			if clickable, ok := m.activeInline.(dialog.MouseClickableEditor); ok {
				clickable.SetHover(msg.X, msg.Y)
			}
		}
	}

	switch m.state {
	case uiChat:
		// Skip chat edge-scrolling when an inline editor is
		// active to prevent accidental scrolling while hovering
		// over question forms or other inline components.
		if m.activeInline != nil && m.focus == uiFocusEditor {
			break
		}
		if msg.Y <= 0 {
			if cmd := m.chat.ScrollByAndAnimate(-1); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if !m.chat.SelectedItemInView() {
				m.chat.SelectPrev()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		} else if msg.Y >= m.chat.Height()-1 {
			if cmd := m.chat.ScrollByAndAnimate(1); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if !m.chat.SelectedItemInView() {
				m.chat.SelectNext()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}

		x, y := msg.X, msg.Y
		// Adjust for chat area position
		x -= m.layout.main.Min.X
		y -= m.layout.main.Min.Y
		m.chat.HandleMouseDrag(x, y)
	}

	return cmds, false
}

// handleMouseClick handles tea.MouseClickMsg. skipTail reports that
// Update must return right away instead of running its shared tail.
func (m *UI) handleMouseClick(msg tea.MouseClickMsg) (cmds []tea.Cmd, skipTail bool) {
	// Pass mouse events to dialogs first if any are open.
	if m.dialog.HasDialogs() {
		if cmd := m.handleDialogMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return cmds, true
	}

	// Route clicks to inline editors that support mouse interaction.
	// A collapsed question form (Tab'd away to the chat) only renders
	// a one-line summary; its component layout is stale, last built
	// by a full Draw. Skip hit-testing it and fall through instead:
	// handleClickFocus below re-expands it if the click landed in
	// the editor area, and the chat pane handles it otherwise.
	// Hit-testing it anyway risked matching stale coordinates and
	// swallowing clicks meant for the chat pane underneath, such as
	// the one that re-enters a parked branch session.
	qf, isQuestionForm := m.activeInline.(*dialog.QuestionForm)
	collapsed := isQuestionForm && m.shouldCollapseQuestion(qf)
	if m.activeInline != nil && !collapsed {
		if clickable, ok := m.activeInline.(dialog.MouseClickableEditor); ok {
			if done, handled := clickable.HandleMouseClick(msg.X, msg.Y); handled {
				if done {
					m.activeInline = nil
					m.textarea.Focus()
					m.updateLayoutAndSize()
				}
				return cmds, true
			}
		}
	}

	// Route a click on the mention/file completions popup to the
	// same insert path Enter uses. The popup renders bottom-to-top
	// (completions.New sets SetReverse(true)), but
	// list.ItemIndexAtPosition walks items in un-reversed order, so
	// the clicked screen row has to be flipped before resolving
	// which item it landed on.
	if m.completionsOpen && m.completions.HasItems() && msg.Button == uv.MouseLeft {
		rect := m.completionsRect
		if msg.X >= rect.Min.X && msg.X < rect.Max.X && msg.Y >= rect.Min.Y && msg.Y < rect.Max.Y {
			yInList := (rect.Dy() - 1) - (msg.Y - rect.Min.Y)
			if cmd, keepOpen, handled := m.applyCompletionSelection(m.completions.SelectAt(yInList)); handled {
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !keepOpen {
					m.closeCompletions()
				}
			}
			return cmds, true
		}
	}

	if cmd := m.handleClickFocus(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Check if the click landed on an attachment's remove button.
	// The attachment chips are rendered on the first row of the
	// editor layout area, above the textarea.
	if m.activeInline == nil && msg.Button == uv.MouseLeft && len(m.attachments.List()) > 0 && msg.Y == m.layout.editor.Min.Y {
		relX := msg.X - m.layout.editor.Min.X
		if m.attachments.HandleClick(relX) {
			return cmds, true
		}
	}

	// Check if the click landed on an ancestor crumb in the header
	// breadcrumb. The level in view is never a click target — there
	// is nowhere to jump to.
	if msg.Button == uv.MouseLeft && msg.Y == m.layout.header.Min.Y {
		relX := msg.X - m.layout.header.Min.X
		if idx, ok := m.header.HitTestBreadcrumb(relX); ok {
			if cmd := m.goToBreadcrumbLevel(idx); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return cmds, true
		}
	}

	switch m.state {
	case uiChat:
		x, y := msg.X, msg.Y
		// Adjust for chat area position
		x -= m.layout.main.Min.X
		y -= m.layout.main.Min.Y
		{
			if handled, cmd := m.chat.HandleMouseDown(x, y); handled {
				m.lastClickTime = time.Now()
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	}

	return cmds, false
}
