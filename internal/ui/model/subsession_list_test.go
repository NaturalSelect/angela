package model

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newSubSessionListUI is newSubSessionUI with a mock workspace the test can
// script the sub-session list on. Messages fed through Update refresh the
// busy state, the prompt queue and the LSP states, so those probes are
// answered too.
func newSubSessionListUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()

	m := newSubSessionUI(t)
	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().CreateAgentToolSessionID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(messageID, toolCallID string) string {
			return fmt.Sprintf("%s$$%s", messageID, toolCallID)
		}).AnyTimes()
	stubBusyProbe(ws, true, false, permission.ModeManual, &workspace.ActiveAgent{})
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()
	m.com.Workspace = ws
	return m, ws
}

// subSession builds a child as the coordinator reports it.
func subSession(id string, kind agenttools.SubSessionKind, status agenttools.SubSessionStatus) agenttools.SubSession {
	return agenttools.SubSession{ID: id, ParentID: "root", Agent: "explore", Task: id + " task", Kind: kind, Status: status}
}

// openSubSessionsList opens the list the way the user does: the command
// reads the children off the Update goroutine and the result opens the
// dialog.
func openSubSessionsList(t *testing.T, m *UI, ws *MockWorkspace, subs ...agenttools.SubSession) []tea.Msg {
	t.Helper()
	ws.EXPECT().AgentSubSessions(gomock.Any(), m.session.ID).Return(subs, nil)
	return runCmds(m, m.openSubSessionsDialog())
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

	tests := []struct {
		name string
		sub  agenttools.SubSession
		want dialog.SubSessionEntry
	}{
		{
			name: "running foreground agent can be stopped",
			sub:  subSession("fg", agenttools.SubSessionKindForeground, agenttools.SubSessionRunning),
			want: dialog.SubSessionEntry{State: dialog.SubSessionRunning, End: dialog.SubSessionEndStop},
		},
		{
			name: "running background agent can be stopped",
			sub:  subSession("bg", agenttools.SubSessionKindBackground, agenttools.SubSessionRunning),
			want: dialog.SubSessionEntry{State: dialog.SubSessionRunning, End: dialog.SubSessionEndStop},
		},
		{
			name: "running branch waits for the user and can be aborted",
			sub:  subSession("br", agenttools.SubSessionKindBranch, agenttools.SubSessionRunning),
			want: dialog.SubSessionEntry{State: dialog.SubSessionWaiting, End: dialog.SubSessionEndAbort},
		},
		{
			name: "running child of unknown kind cannot be ended",
			sub:  subSession("unknown", agenttools.SubSessionKindUnknown, agenttools.SubSessionRunning),
			want: dialog.SubSessionEntry{State: dialog.SubSessionRunning, End: dialog.SubSessionEndNone},
		},
		{
			name: "completed",
			sub:  subSession("done", agenttools.SubSessionKindForeground, agenttools.SubSessionCompleted),
			want: dialog.SubSessionEntry{State: dialog.SubSessionDone, End: dialog.SubSessionEndNone},
		},
		{
			name: "failed",
			sub:  subSession("failed", agenttools.SubSessionKindBackground, agenttools.SubSessionFailed),
			want: dialog.SubSessionEntry{State: dialog.SubSessionFailed, End: dialog.SubSessionEndNone},
		},
		{
			name: "stopped foreground agent",
			sub:  subSession("stopped", agenttools.SubSessionKindForeground, agenttools.SubSessionStopped),
			want: dialog.SubSessionEntry{State: dialog.SubSessionStopped, End: dialog.SubSessionEndNone},
		},
		{
			name: "abandoned branch reads as stopped",
			sub:  subSession("abandoned", agenttools.SubSessionKindBranch, agenttools.SubSessionStopped),
			want: dialog.SubSessionEntry{State: dialog.SubSessionStopped, End: dialog.SubSessionEndNone},
		},
		{
			name: "child from an earlier process",
			sub:  subSession("old", agenttools.SubSessionKindUnknown, agenttools.SubSessionEnded),
			want: dialog.SubSessionEntry{State: dialog.SubSessionEnded, End: dialog.SubSessionEndNone},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.want.SessionID = tt.sub.ID
			tt.want.Agent = tt.sub.Agent
			tt.want.Task = tt.sub.Task

			require.Equal(t, []dialog.SubSessionEntry{tt.want}, subSessionEntries([]agenttools.SubSession{tt.sub}))
		})
	}
}

func TestSubSessionEntriesKeepTheOrderTheCoordinatorGave(t *testing.T) {
	t.Parallel()

	entries := subSessionEntries([]agenttools.SubSession{
		subSession("c", agenttools.SubSessionKindForeground, agenttools.SubSessionCompleted),
		subSession("a", agenttools.SubSessionKindBackground, agenttools.SubSessionRunning),
		subSession("b", agenttools.SubSessionKindBranch, agenttools.SubSessionRunning),
	})

	require.Len(t, entries, 3)
	require.Equal(t, []string{"c", "a", "b"}, []string{entries[0].SessionID, entries[1].SessionID, entries[2].SessionID})
}

func TestOpenSubSessionsDialogListsTheChildren(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)

	openSubSessionsList(t, m, ws, subSession("child", agenttools.SubSessionKindForeground, agenttools.SubSessionRunning))

	require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
}

// Reading the children may be a network call, so the dialog must not open
// before the command has run.
func TestOpenSubSessionsDialogReadsTheChildrenOnlyWhenTheCommandRuns(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)

	cmd := m.openSubSessionsDialog()

	require.NotNil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.SubSessionsID))

	ws.EXPECT().AgentSubSessions(gomock.Any(), "root").
		Return([]agenttools.SubSession{subSession("child", agenttools.SubSessionKindForeground, agenttools.SubSessionRunning)}, nil)
	msg, ok := cmd().(subSessionsLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "root", msg.sessionID)
}

// An empty picker would be a dead end; the user is told instead.
func TestOpenSubSessionsDialogWithoutChildrenOnlyReportsInfo(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)

	msgs := openSubSessionsList(t, m, ws)

	var info *util.InfoMsg
	for _, msg := range msgs {
		if i, ok := msg.(util.InfoMsg); ok {
			info = &i
		}
	}
	require.NotNil(t, info, "the notice must reach the status bar")
	require.Equal(t, "No sub-sessions in this session.", info.Msg)
	require.False(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
}

// A list that cannot be read must say so rather than read as "none".
func TestOpenSubSessionsDialogReportsALoadError(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)
	ws.EXPECT().AgentSubSessions(gomock.Any(), "root").Return(nil, errors.New("daemon unreachable"))

	msgs := runCmds(m, m.openSubSessionsDialog())

	var info *util.InfoMsg
	for _, msg := range msgs {
		if i, ok := msg.(util.InfoMsg); ok {
			info = &i
		}
	}
	require.NotNil(t, info)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "daemon unreachable")
	require.False(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
}

// The user may have moved to another session while the children were being
// read; opening the list of the one they left would show the wrong rows.
func TestSubSessionsLoadedForAnotherSessionIsDropped(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)
	ws.EXPECT().AgentSubSessions(gomock.Any(), "root").
		Return([]agenttools.SubSession{subSession("child", agenttools.SubSessionKindForeground, agenttools.SubSessionRunning)}, nil)
	msg := m.openSubSessionsDialog()()

	m.session = &session.Session{ID: "elsewhere", Title: "Elsewhere"}
	m.Update(msg)

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

	childID := "msg-1$$call-1"
	ws.EXPECT().GetSession(gomock.Any(), childID).
		Return(session.Session{ID: childID, Title: "explore", ParentSessionID: "root"}, nil)
	ws.EXPECT().ListSessionHistory(gomock.Any(), childID).Return([]history.File(nil), nil)
	ws.EXPECT().FileTrackerListReadFiles(gomock.Any(), childID).Return(nil, nil)
	ws.EXPECT().AgentIsSessionBranch(childID).Return(false)
	ws.EXPECT().SetCurrentSession(gomock.Any(), childID).Return(nil)

	openSubSessionsList(t, m, ws, subSession(childID, agenttools.SubSessionKindForeground, agenttools.SubSessionCompleted))
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
