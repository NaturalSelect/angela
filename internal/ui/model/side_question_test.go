package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// drainForSideQuestionAnswered runs cmd and everything it batches (the
// scroll/animation start cmds askSideQuestion also returns), returning the
// sideQuestionAnsweredMsg produced by the async fetch.
func drainForSideQuestionAnswered(t *testing.T, cmd tea.Cmd) sideQuestionAnsweredMsg {
	t.Helper()

	var found *sideQuestionAnsweredMsg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case sideQuestionAnsweredMsg:
			found = &m
		case tea.BatchMsg:
			for _, sub := range m {
				walk(sub)
			}
		}
	}
	walk(cmd)
	require.NotNil(t, found, "expected a sideQuestionAnsweredMsg somewhere in the batch")
	return *found
}

// TestAskSideQuestion_CompletesChatItemOnSuccess verifies that
// askSideQuestion appends a pending chat item without touching the
// main-turn busy state, and that feeding its answer back through Update
// completes that same item in place.
func TestAskSideQuestion_CompletesChatItemOnSuccess(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentAskSideQuestion(gomock.Any(), "s1", "what now?").Return("the answer", nil)

	m := newBusyUIWithWorkspace(ws)
	busyBefore := m.isAgentBusy()

	cmd := m.askSideQuestion("s1", "what now?")
	require.False(t, m.dialog.HasDialogs(), "askSideQuestion must not open any dialog")
	require.Equal(t, busyBefore, m.isAgentBusy(), "a side question must not affect the main-turn busy state")

	answered := drainForSideQuestionAnswered(t, cmd)
	require.NoError(t, answered.err)
	require.Equal(t, "the answer", answered.answer)

	m.Update(answered)

	item, ok := m.chat.MessageItem(answered.pendingID).(*chat.SideQuestionItem)
	require.True(t, ok, "expected the pending item to still be a *chat.SideQuestionItem")
	require.True(t, item.Finished())
	require.Contains(t, ansi.Strip(item.Render(80)), "the answer")
}

// TestAskSideQuestion_FailsChatItemOnError verifies a failed fetch is
// shown on the same chat item instead of a toast, since the item is
// still there to show it on.
func TestAskSideQuestion_FailsChatItemOnError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentAskSideQuestion(gomock.Any(), "s1", "what now?").Return("", errors.New("boom"))

	m := newBusyUIWithWorkspace(ws)

	cmd := m.askSideQuestion("s1", "what now?")
	answered := drainForSideQuestionAnswered(t, cmd)
	require.Error(t, answered.err)

	m.Update(answered)

	item, ok := m.chat.MessageItem(answered.pendingID).(*chat.SideQuestionItem)
	require.True(t, ok)
	require.True(t, item.Finished())
	require.Contains(t, ansi.Strip(item.Render(80)), "boom")
}

// TestUpdate_SideQuestionAnswered_MissingItemReportsError verifies that
// when the pending chat item is gone by the time the answer arrives (for
// example the session was switched while the fetch was in flight) and the
// fetch failed, the error is still surfaced as a toast instead of being
// silently dropped.
func TestUpdate_SideQuestionAnswered_MissingItemReportsError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	_, cmd := m.Update(sideQuestionAnsweredMsg{pendingID: "does-not-exist", sessionID: "s1", err: errors.New("boom")})

	require.NotNil(t, cmd)
	require.True(t, containsErrorInfoMsg(cmd()), "the error must be reported as a toast")
}

// containsErrorInfoMsg reports whether msg is an error util.InfoMsg, or a
// tea.BatchMsg containing one, since Update batches side effects together
// with whatever else ran during the same message.
func containsErrorInfoMsg(msg tea.Msg) bool {
	switch m := msg.(type) {
	case util.InfoMsg:
		return m.Type == util.InfoTypeError
	case tea.BatchMsg:
		for _, sub := range m {
			if sub != nil && containsErrorInfoMsg(sub()) {
				return true
			}
		}
	}
	return false
}
