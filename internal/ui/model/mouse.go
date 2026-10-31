package model

import (
	"time"

	tea "charm.land/bubbletea/v2"
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
