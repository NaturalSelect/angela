package model

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
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
	cmds = append(cmds, m.showPending(undoPreviewingInfo), func() tea.Msg {
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
	cmds = append(cmds, m.showPending(undoingInfo), func() tea.Msg {
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

// handleUndoResult handles undoResultMsg.
func (m *UI) handleUndoResult(msg undoResultMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.sessionID != m.currentSessionID() {
		// NOTE: The messages are already deleted, so hold the text and
		// files until that session is shown again.
		if msg.result.PoppedText != "" || len(msg.result.PoppedAttachments) > 0 {
			m.stashRestoredPrompts(msg.sessionID, []message.QueuedPrompt{{
				Prompt:      msg.result.PoppedText,
				Attachments: msg.result.PoppedAttachments,
			}})
		}
	} else {
		m.prependAttachments(msg.result.PoppedAttachments)
		if msg.result.PoppedText != "" {
			if cmd := m.prependToEditor(msg.result.PoppedText); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	switch {
	case len(msg.result.Skipped) > 0:
		cmds = append(cmds, util.ReportWarn(fmt.Sprintf("Undid last turn, but skipped %d file(s) modified outside the session", len(msg.result.Skipped))))
	case len(msg.result.Reverted) > 0 || len(msg.result.Deleted) > 0:
		cmds = append(cmds, util.ReportInfo(fmt.Sprintf("Undid last turn: %d file(s) reverted, %d deleted", len(msg.result.Reverted), len(msg.result.Deleted))))
	default:
		cmds = append(cmds, util.ReportInfo("Undid last turn"))
	}
	return cmds
}
