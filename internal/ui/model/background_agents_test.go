package model

import (
	"encoding/json"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
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

// ctrl+x in the list ends a running agent by cancelling its own session, the
// road the rest of the UI already takes to stop a sub-agent. The list stays
// open so several agents can be stopped in one visit.
func TestCtrlXInTheListStopsARunningAgentWhateverItsKind(t *testing.T) {
	t.Parallel()

	for _, kind := range []agenttools.SubSessionKind{
		agenttools.SubSessionKindForeground,
		agenttools.SubSessionKindBackground,
	} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			m, ws := newSubSessionListUI(t)
			openSubSessionsList(t, m, ws, subSession("child", kind, agenttools.SubSessionRunning))

			ws.EXPECT().AgentCancel("child")
			cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
			runCmds(m, cmd)

			require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID), "the list stays open so several agents can be stopped")
		})
	}
}

// A branch waiting for the user is given up the way /abort gives it up, not
// by cancelling a turn, which a branch spares on purpose. The view stays on
// the parent: only the branch is ended.
func TestCtrlXInTheListAbortsAWaitingBranch(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)
	openSubSessionsList(t, m, ws, subSession("branch", agenttools.SubSessionKindBranch, agenttools.SubSessionRunning))

	ws.EXPECT().AgentAbandonBranch("branch")
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	runCmds(m, cmd)

	require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID), "the list stays open")
	require.Equal(t, "root", m.session.ID, "the view stays on the parent")
	require.False(t, m.inSubSession())
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

func TestAbortingFromTheListAbandonsOnlyWhenTheCommandRuns(t *testing.T) {
	t.Parallel()
	m, ws := newSubSessionListUI(t)

	cmd := m.abortSubSessionCmd("branch")
	require.NotNil(t, cmd)

	ws.EXPECT().AgentAbandonBranch("branch")
	require.Nil(t, cmd())
}

// A child of unknown kind, or one that has already ended, offers no way to
// end it, so the key must neither cancel nor abandon anything nor close the
// list.
func TestCtrlXInTheListLeavesAChildThatCannotBeEndedAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sub  proto.SubSession
	}{
		{"unknown kind", subSession("child", agenttools.SubSessionKindUnknown, agenttools.SubSessionRunning)},
		{"completed", subSession("child", agenttools.SubSessionKindForeground, agenttools.SubSessionCompleted)},
		{"from an earlier process", subSession("child", agenttools.SubSessionKindUnknown, agenttools.SubSessionEnded)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, ws := newSubSessionListUI(t)
			openSubSessionsList(t, m, ws, tt.sub)

			cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
			runCmds(m, cmd)

			require.True(t, m.dialog.ContainsDialog(dialog.SubSessionsID))
		})
	}
}

// The parent's turn is over but its background agent is not, so the status
// line must keep reading as at work or the user takes the session for done.
func TestTurnStatusStaysBusyWhileABackgroundAgentRuns(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.backgroundAgentsCache.set(1)
	require.False(t, m.isSessionBusy())

	status := ansi.Strip(m.renderTurnStatus(200))

	require.Contains(t, status, "1 background agent running")
	require.NotContains(t, status, "Thinking")
}

func TestTurnStatusCountsEveryRunningBackgroundAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.backgroundAgentsCache.set(2)

	require.Contains(t, ansi.Strip(m.renderTurnStatus(200)), "2 background agents running")
}

// Esc interrupts the session's own turn, and a background agent survives
// that. Offering to stop would promise something the key does not do.
func TestTurnStatusOffersNoStopForBackgroundOnlyWork(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.backgroundAgentsCache.set(1)

	status := ansi.Strip(m.renderTurnStatus(200))

	require.Contains(t, status, "background agent running")
	require.NotContains(t, status, "esc")
	require.False(t, m.escapeCancels())
}

// While the turn itself runs, what it is doing outranks the agent it left
// behind, and esc keeps its meaning.
func TestTurnStatusPrefersTheTurnOverABackgroundAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.backgroundAgentsCache.set(1)
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)

	status := ansi.Strip(m.renderTurnStatus(200))

	require.Contains(t, status, "Thinking")
	require.NotContains(t, status, "background agent")
	require.Contains(t, status, "esc stop")
}

func TestTurnStatusGoesIdleOnceTheBackgroundAgentEnds(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.backgroundAgentsCache.set(1)
	require.Contains(t, ansi.Strip(m.renderTurnStatus(200)), "background agent running")

	m.backgroundAgentsCache.set(0)

	require.NotContains(t, ansi.Strip(m.renderTurnStatus(200)), "background agent")
}

func TestSpinnerFollowsABackgroundAgentWithNoBusyTurn(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.backgroundAgentsCache.set(1)

	require.NotNil(t, m.syncTurnSpinner(), "the spinner must start for a running background agent")
	require.True(t, m.turnIsSpinning)
	require.Nil(t, m.syncTurnSpinner(), "a second sync must not start another tick chain")

	m.backgroundAgentsCache.set(0)

	require.Nil(t, m.syncTurnSpinner())
	require.False(t, m.turnIsSpinning)
}

// The count arrives with the busy probe, so the spinner has to start from
// the probe result alone, with nothing in the transcript to go by.
func TestBusyProbeResultStartsTheSpinnerForABackgroundAgent(t *testing.T) {
	t.Parallel()
	m, _ := newBackgroundTranscriptUI(t)
	m.busyFetchInFlight = true

	cmds := m.applyBusyState(busyStateMsg{gen: m.busyFetchGen, backgroundAgents: 2, mode: permission.ModeManual})

	require.Equal(t, 2, m.backgroundAgentsCache.val)
	require.NotEmpty(t, cmds, "the spinner tick must be returned")
	require.True(t, m.turnIsSpinning)
}

func TestBusyRefreshCarriesTheBackgroundAgentCount(t *testing.T) {
	t.Parallel()
	m, ws := newBackgroundTranscriptUI(t)
	ws.EXPECT().AgentIsReady().Return(true)
	ws.EXPECT().AgentIsBusy().Return(false)
	ws.EXPECT().AgentSessionStatus("s1").Return(false, 3)
	ws.EXPECT().AgentActive(gomock.Any(), "s1").Return(workspace.ActiveAgent{}, nil)
	ws.EXPECT().PermissionMode().Return(permission.ModeManual)

	msg, ok := m.dispatchBusyRefresh()().(busyStateMsg)

	require.True(t, ok)
	require.Equal(t, 3, msg.backgroundAgents)
}
