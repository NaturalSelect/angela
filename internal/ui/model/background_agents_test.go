package model

import (
	"encoding/json"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const (
	bgAgentID      = "agt_0123abcd"
	otherBgAgentID = "agt_89abcdef"
)

func backgroundReceipt(t *testing.T, callID, agentID string) message.ToolResult {
	t.Helper()
	meta, err := json.Marshal(agenttools.AgentResponseMetadata{Background: true, AgentID: agentID})
	require.NoError(t, err)
	return message.ToolResult{ToolCallID: callID, Content: "launched", Metadata: string(meta)}
}

// backgroundLaunchMessages is a transcript holding one background launch:
// the assistant's call and the receipt it got back at once.
func backgroundLaunchMessages(t *testing.T, msgID, callID, agentID string) []message.Message {
	t.Helper()
	return []message.Message{
		{
			ID:        msgID,
			SessionID: "s1",
			Role:      message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{ID: callID, Name: toolnames.Agent, Input: `{"subagent_type":"explore","description":"map the repo"}`, Finished: true},
			},
		},
		{
			ID:        msgID + "-result",
			SessionID: "s1",
			Role:      message.Tool,
			Parts:     []message.ContentPart{backgroundReceipt(t, callID, agentID)},
		},
	}
}

func noticeMessage(id string, notices ...agenttools.BackgroundAgentNotice) message.Message {
	text := ""
	for _, n := range notices {
		text += agenttools.RenderBackgroundAgentNotice(n) + "\n"
	}
	return message.Message{
		ID:        id,
		SessionID: "s1",
		Role:      message.User,
		Parts:     []message.ContentPart{message.TextContent{Text: reminder.Wrap(text)}},
	}
}

func endedNotice(agentID string, status agenttools.BackgroundAgentStatus) agenttools.BackgroundAgentNotice {
	return agenttools.BackgroundAgentNotice{AgentID: agentID, Status: status, Summary: "Agent ended"}
}

// newBackgroundTranscriptUI is a UI whose workspace answers what loading a
// transcript with agent calls asks: the child session's name and its
// (empty) messages.
func newBackgroundTranscriptUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()
	m, ws := newMockBusyUI(t)
	ws.EXPECT().CreateAgentToolSessionID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(messageID, toolCallID string) string {
			return fmt.Sprintf("%s$$%s", messageID, toolCallID)
		}).AnyTimes()
	ws.EXPECT().ListMessages(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return m, ws
}

func agentItemsOf(items []chat.MessageItem) []*chat.AgentToolMessageItem {
	return agentToolItemsIn(items)
}

func backgroundAgentItem(t *testing.T, m *UI, msgID, callID, agentID string) *chat.AgentToolMessageItem {
	t.Helper()
	item := dispatchedAgent(m, msgID, callID, `{"subagent_type":"explore","description":"map the repo"}`, true, false)
	receipt := backgroundReceipt(t, callID, agentID)
	item.SetResult(&receipt)
	return item
}

// The notice is the only record of how the agent ended, and it sits in a
// reminder the transcript hides. Loading must still read it.
func TestBuildSessionItemsBackfillsTheOutcomeFromANotice(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)

	msgs := append(backgroundLaunchMessages(t, "m1", "c1", bgAgentID),
		noticeMessage("rem1", endedNotice(bgAgentID, agenttools.BackgroundAgentCompleted)))
	items, _ := m.buildSessionItems("s1", msgs)

	agents := agentItemsOf(items)
	require.Len(t, agents, 1)
	require.True(t, agents[0].IsBackground())
	require.Equal(t, agenttools.BackgroundAgentCompleted, agents[0].BackgroundOutcome())
	require.Equal(t, chat.ToolStatusSuccess, agents[0].Status())
	for _, item := range items {
		require.NotEqual(t, "rem1", item.ID(), "the reminder itself must stay out of the transcript")
	}
}

func TestBuildSessionItemsMatchesEachNoticeToItsOwnAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)

	msgs := append(backgroundLaunchMessages(t, "m1", "c1", bgAgentID),
		backgroundLaunchMessages(t, "m2", "c2", otherBgAgentID)...)
	msgs = append(msgs,
		noticeMessage("rem1",
			endedNotice(otherBgAgentID, agenttools.BackgroundAgentStopped),
			endedNotice(bgAgentID, agenttools.BackgroundAgentFailed)))
	items, _ := m.buildSessionItems("s1", msgs)

	agents := agentItemsOf(items)
	require.Len(t, agents, 2)
	require.Equal(t, agenttools.BackgroundAgentFailed, agents[0].BackgroundOutcome())
	require.Equal(t, agenttools.BackgroundAgentStopped, agents[1].BackgroundOutcome())
}

// No notice and no live session is the restart case: the agent died with the
// process and nobody will ever report it.
func TestBuildSessionItemsMarksAnAgentLostToARestartFailed(t *testing.T) {
	t.Parallel()
	m, ws := newBackgroundTranscriptUI(t)
	ws.EXPECT().AgentIsSessionBusy("m1$$c1").Return(false)

	items, _ := m.buildSessionItems("s1", backgroundLaunchMessages(t, "m1", "c1", bgAgentID))

	agents := agentItemsOf(items)
	require.Len(t, agents, 1)
	require.Equal(t, agenttools.BackgroundAgentFailed, agents[0].BackgroundOutcome())
	require.False(t, agents[0].BackgroundRunning())
}

func TestBuildSessionItemsLeavesALiveAgentRunning(t *testing.T) {
	t.Parallel()
	m, ws := newBackgroundTranscriptUI(t)
	ws.EXPECT().AgentIsSessionBusy("m1$$c1").Return(true)

	items, _ := m.buildSessionItems("s1", backgroundLaunchMessages(t, "m1", "c1", bgAgentID))

	agents := agentItemsOf(items)
	require.Len(t, agents, 1)
	require.True(t, agents[0].BackgroundRunning())
	require.Empty(t, agents[0].BackgroundOutcome())
}

// A notice settles the question, so the probe — a round trip in
// client/server mode — is not paid for it. The mock fails on any call it
// was not told to expect.
func TestBuildSessionItemsSkipsTheProbeForAnAgentThatReported(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)

	msgs := append(backgroundLaunchMessages(t, "m1", "c1", bgAgentID),
		noticeMessage("rem1", endedNotice(bgAgentID, agenttools.BackgroundAgentCompleted)))

	require.NotPanics(t, func() { m.buildSessionItems("s1", msgs) })
}

func TestBuildSessionItemsIgnoresAnOrdinaryReminder(t *testing.T) {
	t.Parallel()
	m, ws := newBackgroundTranscriptUI(t)
	ws.EXPECT().AgentIsSessionBusy("m1$$c1").Return(true)

	ordinary := message.Message{
		ID: "rem1", SessionID: "s1", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: reminder.Wrap("remember the todo list")}},
	}
	items, _ := m.buildSessionItems("s1", append(backgroundLaunchMessages(t, "m1", "c1", bgAgentID), ordinary))

	agents := agentItemsOf(items)
	require.Len(t, agents, 1)
	require.True(t, agents[0].BackgroundRunning())
}

// A call that is not a background launch has no agent ID, so a notice can
// never land on it and the probe is never asked about it.
func TestBuildSessionItemsLeavesAnOrdinaryAgentCallAlone(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)

	msgs := []message.Message{
		{
			ID: "m1", SessionID: "s1", Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{ID: "c1", Name: toolnames.Agent, Input: `{"subagent_type":"explore"}`, Finished: true},
			},
		},
		{
			ID: "m1-result", SessionID: "s1", Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{ToolCallID: "c1", Content: "report"}},
		},
		noticeMessage("rem1", endedNotice(bgAgentID, agenttools.BackgroundAgentFailed)),
	}
	items, _ := m.buildSessionItems("s1", msgs)

	agents := agentItemsOf(items)
	require.Len(t, agents, 1)
	require.False(t, agents[0].IsBackground())
	require.Equal(t, chat.ToolStatusSuccess, agents[0].Status())
}

// A notice arriving while the session is open is the same fact as one read
// from storage, and it must reach the agent already on screen.
func TestAppendSessionMessageAppliesALiveNotice(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	running := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	m.chat.SetMessages(running)
	require.True(t, running.BackgroundRunning())

	cmd := m.appendSessionMessage(noticeMessage("rem1", endedNotice(bgAgentID, agenttools.BackgroundAgentStopped)))

	require.Nil(t, cmd)
	require.Equal(t, agenttools.BackgroundAgentStopped, running.BackgroundOutcome())
	require.Nil(t, m.chat.MessageItem("rem1"), "the reminder must not become a chat item")
}

func TestAppendSessionMessageIgnoresANoticeForAnotherAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	running := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	m.chat.SetMessages(running)

	m.appendSessionMessage(noticeMessage("rem1", endedNotice(otherBgAgentID, agenttools.BackgroundAgentCompleted)))

	require.True(t, running.BackgroundRunning())
}

func TestAppendSessionMessageIgnoresAnOrdinaryReminder(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	running := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	m.chat.SetMessages(running)

	cmd := m.appendSessionMessage(message.Message{
		ID: "rem1", SessionID: "s1", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: reminder.Wrap("remember the todo list")}},
	})

	require.Nil(t, cmd)
	require.True(t, running.BackgroundRunning())
}

// The status line says what the turn is doing. An agent that went off on
// its own is not the turn's business, so it must not read as "Agent".
func TestLastPendingToolSkipsARunningBackgroundAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	background := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	require.True(t, background.BackgroundRunning())
	m.chat.SetMessages(background)

	_, ok := m.chat.LastPendingTool()

	require.False(t, ok)
}

func TestLastPendingToolStillReportsAnOrdinaryRunningAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	foreground := dispatchedAgent(m, "m1", "c1", `{"subagent_type":"explore"}`, true, false)
	m.chat.SetMessages(foreground)

	tc, ok := m.chat.LastPendingTool()

	require.True(t, ok)
	require.Equal(t, "c1", tc.ID)
}

// Once the agent ends it is an ordinary finished call; nothing is pending.
func TestLastPendingToolIgnoresABackgroundAgentThatEnded(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	ended := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	ended.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)
	m.chat.SetMessages(ended)

	_, ok := m.chat.LastPendingTool()

	require.False(t, ok)
}

func TestBackgroundAgentRunningNamesOnlyTheRunningOnes(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	running := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	ended := backgroundAgentItem(t, m, "m2", "c2", otherBgAgentID)
	ended.SetBackgroundOutcome(agenttools.BackgroundAgentFailed)
	foreground := dispatchedAgent(m, "m3", "c3", `{}`, true, false)

	require.True(t, backgroundAgentRunning(running))
	require.False(t, backgroundAgentRunning(ended))
	require.False(t, backgroundAgentRunning(foreground))
	require.False(t, backgroundAgentRunning(&testMessageItem{id: "text", text: "hi"}))
}

// The parent's turn is over, but the agent it launched is not: its spinner
// has to start when the session is loaded even though nothing is busy.
func TestApplySessionItemsStartsTheSpinnerOfARunningBackgroundAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	require.False(t, m.isSessionBusy())
	running := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)

	cmd := m.applySessionItems([]chat.MessageItem{running})

	require.NotNil(t, cmd, "the running agent's spinner must be armed")
}

func TestApplySessionItemsStartsNoSpinnerWhenNothingRuns(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	ended := backgroundAgentItem(t, m, "m1", "c1", bgAgentID)
	ended.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)

	cmd := m.applySessionItems([]chat.MessageItem{ended})

	require.Nil(t, cmd)
}

func TestSubSessionEntriesTellABackgroundAgentsStates(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)

	running := backgroundAgentItem(t, m, "msg-1", "call-1", "agt_00000001")
	completed := backgroundAgentItem(t, m, "msg-2", "call-2", "agt_00000002")
	completed.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)
	failed := backgroundAgentItem(t, m, "msg-3", "call-3", "agt_00000003")
	failed.SetBackgroundOutcome(agenttools.BackgroundAgentFailed)
	stopped := backgroundAgentItem(t, m, "msg-4", "call-4", "agt_00000004")
	stopped.SetBackgroundOutcome(agenttools.BackgroundAgentStopped)
	m.chat.SetMessages(running, completed, failed, stopped)

	entries := m.subSessionEntries()

	require.Len(t, entries, 4)
	require.Equal(t, dialog.SubSessionRunning, entries[0].State)
	require.True(t, entries[0].Stoppable, "only a running background agent can be stopped")
	require.Equal(t, dialog.SubSessionDone, entries[1].State)
	require.False(t, entries[1].Stoppable)
	require.Equal(t, dialog.SubSessionFailed, entries[2].State)
	require.False(t, entries[2].Stoppable)
	require.Equal(t, dialog.SubSessionStopped, entries[3].State)
	require.False(t, entries[3].Stoppable)
}

// The launch receipt gives the call a result at once; it must not read as
// "done" for an agent that has barely started.
func TestSubSessionStateOfABackgroundLaunchIsRunningNotDone(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)
	running := backgroundAgentItem(t, m, "msg-1", "call-1", bgAgentID)

	require.Equal(t, dialog.SubSessionRunning, subSessionState(running, false))
}

func TestSubSessionStateOfAnOrdinaryAgentIsUnchanged(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)

	require.Equal(t, dialog.SubSessionRunning, subSessionState(dispatchedAgent(m, "m", "c", `{}`, true, false), false))
	require.Equal(t, dialog.SubSessionWaiting, subSessionState(dispatchedAgent(m, "m", "c", `{}`, true, false), true))
	require.Equal(t, dialog.SubSessionDone, subSessionState(withResult(dispatchedAgent(m, "m", "c", `{}`, true, false), false), false))
	require.Equal(t, dialog.SubSessionFailed, subSessionState(withResult(dispatchedAgent(m, "m", "c", `{}`, true, false), true), false))
	require.Equal(t, dialog.SubSessionFailed, subSessionState(dispatchedAgent(m, "m", "c", `{}`, true, true), false))
}

// ctrl+x in the list ends the agent by cancelling its own session, the road
// the rest of the UI already takes to stop a sub-agent, and closes the list.
func TestStoppingARunningBackgroundAgentFromTheListCancelsItsSession(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)
	m.chat.SetMessages(backgroundAgentItem(t, m, "msg-1", "call-1", bgAgentID))
	require.Nil(t, m.openSubSessionsDialog())

	ws.EXPECT().AgentCancel("msg-1$$call-1")
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	runCmds(m, cmd)

	require.False(t, m.dialog.ContainsDialog(dialog.SubSessionsID), "the list closes once the stop is requested")
}

// Cancelling must not run on the Update goroutine: in client/server mode it
// is a network call.
func TestStoppingFromTheListCancelsOnlyWhenTheCommandRuns(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)

	cmd := m.stopSubSessionCmd("child")
	require.NotNil(t, cmd)

	ws.EXPECT().AgentCancel("child")
	require.Nil(t, cmd())
}

// An ordinary running sub-agent is not stoppable from the list, so the key
// must neither cancel it nor close the list.
func TestCtrlXInTheListLeavesAnOrdinaryAgentAlone(t *testing.T) {
	t.Parallel()
	m, _ := newSubSessionListUI(t)
	m.chat.SetMessages(dispatchedAgent(m, "msg-1", "call-1", `{"subagent_type":"explore"}`, true, false))
	require.Nil(t, m.openSubSessionsDialog())

	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	runCmds(m, cmd)

	require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
}
