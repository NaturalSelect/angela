package model

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func subSessionListConfig() *config.Config {
	return &config.Config{
		Agents: map[string]config.Agent{
			config.AgentExplore: {ID: config.AgentExplore, Mode: config.AgentModeSubagent},
			"plan":              {ID: "plan", Mode: config.AgentModeBranch},
		},
	}
}

// newSubSessionListUI is newSubSessionUI with a workspace that also answers
// Config(), which the list reads to tell a branch from an ordinary agent.
func newSubSessionListUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()

	m := newSubSessionUI(t)
	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().CreateAgentToolSessionID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(messageID, toolCallID string) string {
			return fmt.Sprintf("%s$$%s", messageID, toolCallID)
		}).AnyTimes()
	ws.EXPECT().Config().Return(subSessionListConfig()).AnyTimes()
	m.com.Workspace = ws
	return m, ws
}

func dispatchedAgent(m *UI, messageID, toolCallID, input string, finished, canceled bool) *chat.AgentToolMessageItem {
	item := chat.NewAgentToolMessageItem(
		m.com.Styles,
		message.ToolCall{ID: toolCallID, Name: toolnames.Agent, Input: input, Finished: finished},
		nil,
		canceled,
	)
	item.SetMessageID(messageID)
	return item
}

func withResult(item *chat.AgentToolMessageItem, isError bool) *chat.AgentToolMessageItem {
	item.SetResult(&message.ToolResult{ToolCallID: item.ToolCall().ID, IsError: isError})
	return item
}

// The list is drawn from the transcript, so it must carry exactly the
// agent calls made at this level, in the order they were made.
func TestChatAgentToolItemsIsTopLevelOnlyInTranscriptOrder(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)

	first := dispatchedAgent(m, "msg-1", "call-1", `{"subagent_type":"explore"}`, true, false)
	nested := dispatchedAgent(m, "msg-x", "call-nested", `{"subagent_type":"explore"}`, true, false)
	first.AddNestedTool(nested)
	second := dispatchedAgent(m, "msg-2", "call-2", `{"subagent_type":"explore"}`, true, false)

	m.chat.SetMessages(first, &testMessageItem{id: "text", text: "between"}, second)

	require.Equal(t, []*chat.AgentToolMessageItem{first, second}, m.chat.AgentToolItems())
}

func TestSubSessionEntriesDescribeEachDirectChild(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)

	m.chat.SetMessages(
		withResult(dispatchedAgent(m, "msg-1", "call-1",
			`{"subagent_type":"explore","description":"map the repo"}`, true, false), false),
		dispatchedAgent(m, "msg-2", "call-2",
			`{"subagent_type":"explore","description":"trace login"}`, true, false),
		dispatchedAgent(m, "msg-3", "call-3",
			`{"subagent_type":"plan","description":"design the API"}`, true, false),
		withResult(dispatchedAgent(m, "msg-4", "call-4",
			`{"subagent_type":"explore","description":"scan deps"}`, true, false), true),
		dispatchedAgent(m, "msg-5", "call-5",
			`{"subagent_type":"explore","description":"stopped early"}`, true, true),
		// Still streaming in: the child session does not exist yet.
		dispatchedAgent(m, "msg-6", "call-6", `{"subagent_type":"ex`, false, false),
	)

	require.Equal(t, []dialog.SubSessionEntry{
		{SessionID: "msg-1$$call-1", Agent: "explore", Task: "map the repo", State: dialog.SubSessionDone},
		{SessionID: "msg-2$$call-2", Agent: "explore", Task: "trace login", State: dialog.SubSessionRunning},
		{SessionID: "msg-3$$call-3", Agent: "plan", Task: "design the API", State: dialog.SubSessionWaiting},
		{SessionID: "msg-4$$call-4", Agent: "explore", Task: "scan deps", State: dialog.SubSessionFailed},
		{SessionID: "msg-5$$call-5", Agent: "explore", Task: "stopped early", State: dialog.SubSessionFailed},
	}, m.subSessionEntries())
}

func TestOpenSubSessionsDialogListsTheChildren(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)
	m.chat.SetMessages(dispatchedAgent(m, "msg-1", "call-1", `{"subagent_type":"explore"}`, true, false))

	cmd := m.openSubSessionsDialog()

	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
}

// An empty picker would be a dead end; the user is told instead.
func TestOpenSubSessionsDialogWithoutChildrenOnlyReportsInfo(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)
	m.chat.SetMessages(&testMessageItem{id: "text", text: "no agents here"})

	cmd := m.openSubSessionsDialog()

	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "the notice must reach the status bar")
	require.Equal(t, "No sub-sessions in this session.", info.Msg)
	require.False(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
}

func TestOpenSubSessionsDialogAlreadyOpenBringsToFront(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.SubSessionsID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openSubSessionsDialog()

	require.Nil(t, cmd)
	require.Equal(t, dialog.SubSessionsID, m.dialog.DialogLast().ID())
}

// Picking a row must take the same road as pressing enter on the agent
// block: the real load command runs and the parent lands on the stack, so
// the breadcrumb and the way back both work. Switching sessions through the
// switcher would clear that stack instead. The pick is made with a real
// enter key press on the real dialog, so the dialog, the action and the
// handler are all exercised together.
func TestPickingASubSessionKeepsTheBreadcrumb(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)
	m.chat.SetMessages(dispatchedAgent(m, "msg-1", "call-1", `{"subagent_type":"explore"}`, true, false))

	childID := "msg-1$$call-1"
	ws.EXPECT().GetSession(gomock.Any(), childID).
		Return(session.Session{ID: childID, Title: "explore", ParentSessionID: "root"}, nil)
	ws.EXPECT().ListSessionHistory(gomock.Any(), childID).Return([]history.File(nil), nil)
	ws.EXPECT().FileTrackerListReadFiles(gomock.Any(), childID).Return(nil, nil)
	ws.EXPECT().AgentIsSessionBranch(childID).Return(false)
	ws.EXPECT().SetCurrentSession(gomock.Any(), childID).Return(nil)

	require.Nil(t, m.openSubSessionsDialog())
	require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID))

	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.False(t, m.dialog.ContainsDialog(dialog.SubSessionsID), "the picker closes on a pick")
	require.False(t, m.inSubSession(), "the push waits for the load to succeed")

	var loaded *loadSessionMsg
	for _, msg := range runCmds(m, cmd) {
		if l, ok := msg.(loadSessionMsg); ok {
			loaded = &l
		}
	}
	require.NotNil(t, loaded, "the pick must load the child session")
	require.False(t, loaded.clearStack)

	m.Update(*loaded)

	require.Equal(t, childID, m.session.ID)
	require.Len(t, m.sessionStack, 1)
	require.Equal(t, "root", m.sessionStack[0].id)
	require.Equal(t, []string{"Root task", "explore"}, m.sessionTrail())
}
