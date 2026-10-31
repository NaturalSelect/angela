package model

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/undo"
)

// handleUndo handles dialog.ActionUndo.
func (m *UI) handleUndo(msg dialog.ActionUndo) tea.Cmd {
	var cmds []tea.Cmd
	// Session-scoped, matching ActionSummarize: the busy cache
	// answers for the whole process and would block this session's
	// undo even while it sits idle.
	if m.com.Workspace.AgentIsSessionBusy(msg.SessionID) {
		cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before undoing..."))
		return tea.Batch(cmds...)
	}
	m.dialog.CloseDialog(dialog.CommandsID)
	sessionID := msg.SessionID
	cmds = append(cmds, func() tea.Msg {
		preview, err := m.com.Workspace.PreviewUndo(context.Background(), sessionID)
		if err != nil {
			if errors.Is(err, undo.ErrNothingToUndo) {
				return util.ReportInfo("Nothing to undo")()
			}
			if errors.Is(err, undo.ErrSessionBusy) {
				return util.ReportWarn("Agent is busy, please wait before undoing...")()
			}
			return util.ReportError(err)()
		}
		return undoPreviewMsg{sessionID: sessionID, preview: preview}
	})
	return tea.Batch(cmds...)
}

// handleUndoConfirmed handles dialog.ActionUndoConfirmed.
func (m *UI) handleUndoConfirmed(msg dialog.ActionUndoConfirmed) tea.Cmd {
	var cmds []tea.Cmd
	m.dialog.CloseDialog(dialog.UndoID)
	if m.com.Workspace.AgentIsSessionBusy(msg.SessionID) {
		cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before undoing..."))
		return tea.Batch(cmds...)
	}
	sessionID := msg.SessionID
	cutMessageID := msg.CutMessageID
	cmds = append(cmds, func() tea.Msg {
		result, err := m.com.Workspace.Undo(context.Background(), sessionID, cutMessageID)
		if err != nil {
			if errors.Is(err, undo.ErrSessionBusy) {
				return util.ReportWarn("Agent is busy, please wait before undoing...")()
			}
			return util.ReportError(err)()
		}
		return undoResultMsg{sessionID: sessionID, result: result}
	})
	return tea.Batch(cmds...)
}
