package model

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
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
