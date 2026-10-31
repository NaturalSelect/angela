package model

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// sideQuestionAnsweredMsg carries the result of a /btw side question,
// fetched off the Update goroutine.
type sideQuestionAnsweredMsg struct {
	pendingID string
	sessionID string
	question  string
	answer    string
	err       error
}

// askSideQuestion answers a one-off question from sessionID's existing
// context, concurrently with any turn already running on it: the question
// and its answer are appended to the chat as an in-memory item only, never
// written to the session history. It does not touch m.bangCancel or
// m.agentBusyCache, so it never makes isAgentBusy() true and is never
// cancelled by Esc-Esc: a side question runs alongside the main turn
// without blocking or being blocked by it. It uses context.Background(),
// matching ActionSummarize, so it keeps running even if the main turn is
// later cancelled.
func (m *UI) askSideQuestion(sessionID, question string) tea.Cmd {
	var cmds []tea.Cmd

	pendingItem := chat.NewPendingSideQuestionItem(m.com.Styles, question)
	m.chat.AppendMessages(pendingItem)
	if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := pendingItem.StartAnimation(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	pendingID := pendingItem.ID()
	cmds = append(cmds, func() tea.Msg {
		answer, err := m.com.Workspace.AgentAskSideQuestion(context.Background(), sessionID, question)
		return sideQuestionAnsweredMsg{pendingID: pendingID, sessionID: sessionID, question: question, answer: answer, err: err}
	})

	return tea.Batch(cmds...)
}

// handleAskSideQuestion handles dialog.ActionAskSideQuestion.
func (m *UI) handleAskSideQuestion(msg dialog.ActionAskSideQuestion) tea.Cmd {
	var cmds []tea.Cmd
	if strings.TrimSpace(msg.Question) == "" {
		m.dialog.CloseFrontDialog()
		argsDialog := dialog.NewArguments(
			m.com,
			"Side Question",
			"Answered from the current conversation only — no tools, and it never enters the session history.",
			[]commands.Argument{{ID: "QUESTION", Title: "Question", Required: true}},
			msg, // Pass the action as the result
		)
		m.dialog.OpenDialog(argsDialog)
		return tea.Batch(cmds...)
	}
	cmds = append(cmds, m.askSideQuestion(msg.SessionID, msg.Question))
	m.dialog.CloseFrontDialog()
	return tea.Batch(cmds...)
}

// handleSideQuestionAnswered handles sideQuestionAnsweredMsg.
func (m *UI) handleSideQuestionAnswered(msg sideQuestionAnsweredMsg) []tea.Cmd {
	var cmds []tea.Cmd
	completed := false
	if item := m.chat.MessageItem(msg.pendingID); item != nil {
		if sq, ok := item.(*chat.SideQuestionItem); ok {
			if msg.err != nil {
				sq.Fail(msg.err)
			} else {
				sq.Complete(msg.answer)
			}
			if m.chat.Follow() {
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			completed = true
		}
	}
	if !completed {
		if msg.err != nil {
			cmds = append(cmds, util.ReportError(msg.err))
			return cmds
		}
		if m.hasSession() && m.session.ID == msg.sessionID {
			item := chat.NewPendingSideQuestionItem(m.com.Styles, msg.question)
			item.Complete(msg.answer)
			m.chat.AppendMessages(item)
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}
