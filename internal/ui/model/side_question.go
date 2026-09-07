package model

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// sideQuestionAnsweredMsg carries the result of a /btw side question,
// fetched off the Update goroutine.
type sideQuestionAnsweredMsg struct {
	question string
	answer   string
	err      error
}

// askSideQuestion answers a one-off question from sessionID's existing
// context, concurrently with any turn already running on it: the
// question and its answer are never written to the session history.
// It uses context.Background(), matching ActionSummarize, so it keeps
// running even if the main turn is later cancelled.
func (m *UI) askSideQuestion(sessionID, question string) tea.Cmd {
	return func() tea.Msg {
		answer, err := m.com.Workspace.AgentAskSideQuestion(context.Background(), sessionID, question)
		return sideQuestionAnsweredMsg{question: question, answer: answer, err: err}
	}
}
