package model

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/ui/chat"
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
