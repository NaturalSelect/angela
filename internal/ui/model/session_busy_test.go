package model

import (
	"context"
	"reflect"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// newBusyUIWithWorkspace builds a UI wired to ws with an active session
// "s1", enough state for Update to run end to end. It is the shared body
// behind every gomock-backed constructor (newMockBusyUI,
// detailsMockWorkspace's callers, ...) that needs to pre-configure a
// MockWorkspace before the UI is built around it.
func newBusyUIWithWorkspace(ws workspace.Workspace) *UI {
	com := common.DefaultCommon(ws)
	t := com.Styles
	return &UI{
		com:         com,
		status:      NewStatus(com, nil),
		header:      newHeader(com),
		completions: completions.New(t.Completions.Normal, t.Completions.Focused, t.Completions.Match),
		chat:        NewChat(com, config.ScrollbarDefault),
		textarea:    textarea.New(),
		state:       uiChat,
		focus:       uiFocusEditor,
		width:       140,
		height:      45,
		session:     &session.Session{ID: "s1"},
		keyMap:      DefaultKeyMap(),
		dialog:      dialog.NewOverlay(),
		attachments: attachments.New(attachments.NewRenderer(
			t.Attachments.Normal,
			t.Attachments.Deleting,
			t.Attachments.Image,
			t.Attachments.Text,
			t.Attachments.Skill,
			t.Attachments.Remove,
		), attachments.Keymap{}),
	}
}

// newMockBusyUI builds a UI wired to a MockWorkspace with an active
// session "s1", enough state for Update to run end to end. Config()
// defaults to nil — reaching for config is usually the bug a test pins —
// and WorkingDir() to "", since the render path reads it incidentally
// (header.go, ui.go) in tests that have nothing to do with it. Every
// other method is left for each test to expect, since which calls
// happen and how many times is exactly what these tests check: a call
// this test never stubs fails immediately and names the method, which is
// a strictly stronger check than the zero counters it replaces.
func newMockBusyUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	return newBusyUIWithWorkspace(ws), ws
}

// pinTTLs makes the TTL backstop inert for the duration of the test so
// assertions about event-driven refreshes cannot flake by straddling a TTL
// boundary (the tests using it must not call t.Parallel).
func pinTTLs(t *testing.T) {
	t.Helper()
	oldBusy, oldQueue, oldLSP := busyCacheTTL, promptQueueTTL, lspStatesTTL
	busyCacheTTL = time.Hour
	promptQueueTTL = time.Hour
	lspStatesTTL = time.Hour
	t.Cleanup(func() { busyCacheTTL, promptQueueTTL, lspStatesTTL = oldBusy, oldQueue, oldLSP })
}

// warmCaches marks all memoized workspace state fresh so only explicit
// invalidation (not startup staleness) can trigger refresh dispatches.
// The agent stamp is set to the current session, as a landed probe would
// leave it.
func warmCaches(m *UI, busy bool) {
	m.agentBusyCache.set(busy)
	m.permissionModeCache.set(permission.ModeManual)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.promptQueueCheckedAt = time.Now()
	m.lspCheckedAt = time.Now()
}

// runCmds executes a command tree the way the Bubble Tea runtime would,
// feeding cache-refresh messages back into Update. It returns every
// message the tree produced, so tests can assert on what the user would
// have been told. Callers that only care about side effects on the stub
// can ignore the result.
func runCmds(m *UI, cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var msgs []tea.Msg
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			msgs = append(msgs, runCmds(m, c)...)
		}
	case busyStateMsg, promptQueueMsg, agentRunSubmittedMsg, lspStatesMsg, agentModelChangedMsg, branchStatusMsg:
		msgs = append(msgs, msg)
		_, next := m.Update(msg)
		msgs = append(msgs, runCmds(m, next)...)
	default:
		if seq := sequencedCmds(msg); seq != nil {
			for _, c := range seq {
				msgs = append(msgs, runCmds(m, c)...)
			}
			break
		}
		if msg != nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// sequencedCmds extracts the commands from a message that is a slice of
// tea.Cmd. tea.Sequence's message type is unexported and so cannot be
// named in a type switch; matching on its shape lets the harness run
// sequenced commands in order, the way the runtime does.
func sequencedCmds(msg tea.Msg) []tea.Cmd {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil
	}
	cmds := make([]tea.Cmd, v.Len())
	for i := range cmds {
		cmds[i], _ = v.Index(i).Interface().(tea.Cmd)
	}
	return cmds
}

// plainMsg is an arbitrary tea.Msg standing in for keystroke/mouse/tick
// traffic through Update.
type plainMsg struct{}

// stubBusyProbe wires AgentIsReady/AgentIsBusy/AgentActive/
// PermissionMode on ws — the group dispatchBusyRefresh calls
// together — to fixed ready/busy/mode values and to whatever *active
// holds at call time, so a test can reassign it later (mirroring
// countingWorkspace's mutable active field) and have the next probe see
// the new value. Each expectation is AnyTimes: which of these run, and
// how often, is not what the tests using this helper are pinning.
func stubBusyProbe(ws *MockWorkspace, ready, busy bool, mode permission.PermissionMode, active *workspace.ActiveAgent) {
	ws.EXPECT().AgentIsReady().Return(ready).AnyTimes()
	ws.EXPECT().AgentIsBusy().Return(busy).AnyTimes()
	ws.EXPECT().AgentActive(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string) (workspace.ActiveAgent, error) {
			return *active, nil
		}).AnyTimes()
	ws.EXPECT().PermissionMode().Return(mode).AnyTimes()
}

// TestUpdateDoesNotProbeWorkspacePerMessage pins the hot-path fix: Update
// used to call AgentQueuedPrompts (a synchronous HTTP GET in client/server
// mode) at the top of every message while the agent was busy, and the
// placeholder path probed AgentIsReady/AgentIsBusy/PermissionMode —
// every keystroke blocked the single Update goroutine on network round-
// trips. Now Update performs no synchronous workspace call at all; refreshes
// are dispatched as commands.
func TestUpdateDoesNotProbeWorkspacePerMessage(t *testing.T) {
	pinTTLs(t)

	// Nothing beyond Config/WorkingDir is stubbed: any workspace call
	// during these 25 messages fails immediately and names the method,
	// which is what proves Update makes none.
	m, _ := newMockBusyUI(t)

	for range 25 {
		m.Update(plainMsg{})
	}
}

// TestReadsNeverProbeWorkspace pins the read side of the invariant: the
// busy/permission-mode getters used by render paths serve the memoized
// value and never probe, so View can never block on HTTP.
func TestReadsNeverProbeWorkspace(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)

	for range 10 {
		m.isAgentBusy()
		m.permissionModeCached()
	}
}

// TestStreamingUpdatedEventsDoNotProbe pins the streaming path: per-chunk
// message UpdatedEvents arrive once per streamed token and must neither
// probe the workspace synchronously nor schedule busy/queue refreshes —
// only CreatedEvents (run boundaries) do.
func TestStreamingUpdatedEventsDoNotProbe(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	warmCaches(m, true)

	for range 25 {
		m.Update(pubsub.Event[message.Message]{
			Type:    pubsub.UpdatedEvent,
			Payload: message.Message{ID: "m1", SessionID: "s1", Role: message.Assistant},
		})
	}
	require.False(t, m.busyFetchInFlight,
		"per-chunk UpdatedEvents must not schedule a busy refresh")
	require.False(t, m.promptQueueInFlight,
		"per-chunk UpdatedEvents must not schedule a queue refresh")
}

// TestMessageCreatedEventRefreshesBusyAndQueue: a CreatedEvent is a run
// boundary and must invalidate the memoized busy state and fetch fresh
// busy/queue values off-thread.
func TestMessageCreatedEventRefreshesBusyAndQueue(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)

	_, cmd := m.Update(pubsub.Event[message.Message]{
		Type:    pubsub.CreatedEvent,
		Payload: message.Message{ID: "m1", SessionID: "s1", Role: message.User},
	})
	require.True(t, m.busyFetchInFlight, "CreatedEvent must schedule a busy refresh")
	require.True(t, m.promptQueueInFlight, "CreatedEvent must schedule a queue refresh")
	// Nothing stubbed yet beyond Config/WorkingDir: the event handler
	// itself must not probe synchronously.

	active := workspace.ActiveAgent{}
	stubBusyProbe(ws, true, true, permission.ModeManual, &active)
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return([]string{"queued prompt"}).AnyTimes()

	runCmds(m, cmd)
	require.True(t, m.isAgentBusy(), "refreshed busy state must land in the cache")
	require.Equal(t, 1, m.promptQueue, "refreshed queue count must land in the cache")
	require.False(t, m.busyFetchInFlight)
	require.False(t, m.promptQueueInFlight)
}

// TestAgentTerminalNotificationsRefreshBusy pins the busy→idle edge: the
// agent clears its active request before publishing TypeAgentFinished (and
// TypeAgentError) precisely so observers can re-probe. The handler must
// invalidate the memoized busy state and re-fetch busy + queue.
func TestAgentTerminalNotificationsRefreshBusy(t *testing.T) {
	pinTTLs(t)

	for _, typ := range []notify.Type{notify.TypeAgentFinished, notify.TypeAgentError} {
		t.Run(string(typ), func(t *testing.T) {
			m, ws := newMockBusyUI(t)
			warmCaches(m, true) // stale: still busy
			active := workspace.ActiveAgent{}
			stubBusyProbe(ws, true, false, permission.ModeManual, &active) // agent now idle
			ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
			require.True(t, m.isAgentBusy())

			_, cmd := m.Update(pubsub.Event[notify.Notification]{
				Type:    pubsub.CreatedEvent,
				Payload: notify.Notification{Type: typ, SessionID: "s1"},
			})
			require.True(t, m.busyFetchInFlight, "terminal notification must schedule a busy refresh")
			require.True(t, m.promptQueueInFlight, "terminal notification must schedule a queue refresh")

			runCmds(m, cmd)
			require.False(t, m.isAgentBusy(),
				"busy→idle edge must reach the cache without waiting for the TTL")
		})
	}
}

// TestAgentRetryingSetsTurnStatus pins that a retry notification reaches
// the UI through the same pubsub path as every other agent event, and
// that it does so without touching the busy/queue caches: the turn was
// already known to be busy, and a retry doesn't change that.
func TestAgentRetryingSetsTurnStatus(t *testing.T) {
	m, _ := newMockBusyUI(t)
	warmCaches(m, true)

	_, cmd := m.Update(pubsub.Event[notify.Notification]{
		Type: pubsub.CreatedEvent,
		Payload: notify.Notification{
			Type:             notify.TypeAgentRetrying,
			SessionID:        "s1",
			RetryAttempt:     2,
			RetryMaxAttempts: 3,
			RetryDelay:       10 * time.Second,
			Message:          "Rate limited",
		},
	})

	require.Nil(t, cmd, "a retry notification only updates in-memory state")
	require.NotNil(t, m.retryStatus)
	require.Contains(t, m.renderTurnStatus(200), "Retrying 2/3")
}

// TestAgentTerminalNotificationClearsRetryStatus pins that a turn ending
// (success or error) drops any retry banner left over from earlier in
// that same turn, scoped to the session that actually finished so an
// unrelated session's retry status can't be wiped out from under it.
func TestAgentTerminalNotificationClearsRetryStatus(t *testing.T) {
	for _, typ := range []notify.Type{notify.TypeAgentFinished, notify.TypeAgentError} {
		t.Run(string(typ), func(t *testing.T) {
			pinTTLs(t)
			m, ws := newMockBusyUI(t)
			warmCaches(m, true)
			active := workspace.ActiveAgent{}
			stubBusyProbe(ws, true, false, permission.ModeManual, &active)
			ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()

			m.retryStatus = &retryStatus{sessionID: "s1", attempt: 1, maxAttempt: 3, until: time.Now().Add(time.Minute)}

			_, cmd := m.Update(pubsub.Event[notify.Notification]{
				Type:    pubsub.CreatedEvent,
				Payload: notify.Notification{Type: typ, SessionID: "s1"},
			})
			runCmds(m, cmd)

			require.Nil(t, m.retryStatus, "the turn that was retrying just ended")
		})
	}
}

// TestMergedBranchFreezesWithoutLeavingTheSession pins the reactive half of
// freezing a merged branch: a user who watches the merge happen, without
// navigating away and back, must lose the editor the instant the branch
// resolves — mirroring how an ordinary sub-agent transcript already
// behaves rather than requiring a reload to notice. The busy→idle edge is
// what triggers the re-probe, since that is the only edge a branch's last
// turn can end on without the user leaving.
func TestMergedBranchFreezesWithoutLeavingTheSession(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	m.session.ParentSessionID = "parent-1"
	m.sessionIsBranch = true
	m.textarea.Focus()
	warmCaches(m, true) // stale: still busy
	active := workspace.ActiveAgent{}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active) // agent now idle
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().AgentIsSessionBranch("s1").Return(false) // the merge just resolved it

	require.True(t, m.viewingBranch(), "fixture must start on a live branch")

	_, cmd := m.Update(pubsub.Event[notify.Notification]{
		Type:    pubsub.CreatedEvent,
		Payload: notify.Notification{Type: notify.TypeAgentFinished, SessionID: "s1"},
	})
	runCmds(m, cmd)

	require.False(t, m.sessionIsBranch, "a resolved branch must stop reporting as one")
	require.True(t, m.viewingSubAgent(), "a merged branch freezes exactly like a sub-agent transcript")
	require.False(t, m.textarea.Focused(), "the editor must give up focus once the branch freezes")
	require.Equal(t, uiFocusMain, m.focus)
}

// TestBranchStatusRefreshLeavesALiveBranchAlone pins the other side: most
// turns on a live branch are ordinary drafting, not the one that merges
// it, so a re-probe confirming the branch is still waiting must leave the
// editor alone rather than churn focus every turn.
func TestBranchStatusRefreshLeavesALiveBranchAlone(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	m.session.ParentSessionID = "parent-1"
	m.sessionIsBranch = true
	m.textarea.Focus()
	warmCaches(m, true)
	active := workspace.ActiveAgent{}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().AgentIsSessionBranch("s1").Return(true) // still waiting on the user

	_, cmd := m.Update(pubsub.Event[notify.Notification]{
		Type:    pubsub.CreatedEvent,
		Payload: notify.Notification{Type: notify.TypeAgentFinished, SessionID: "s1"},
	})
	runCmds(m, cmd)

	require.True(t, m.sessionIsBranch)
	require.False(t, m.viewingSubAgent(), "a branch still waiting on the user must keep its editor")
	require.True(t, m.textarea.Focused())
}

// TestSessionSwitchRefreshesQueueAndBusy: switching sessions must drop the
// previous session's queue pill and memoized busy state and fetch the new
// session's, so esc never offers to clear the wrong queue.
func TestSessionSwitchRefreshesQueueAndBusy(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, true)
	m.promptQueue = 5 // stale queue pill from the previous session
	m.promptQueueItems = []string{"x", "y", "z", "w", "v"}
	active := workspace.ActiveAgent{}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return([]string{"a", "b"}).AnyTimes()
	ws.EXPECT().ListMessages(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ws.EXPECT().ListUserMessages(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	_, cmd := m.Update(loadSessionMsg{session: &session.Session{ID: "s2"}})
	require.Zero(t, m.promptQueue, "switching sessions must drop the old session's queue pill")
	require.True(t, m.promptQueueInFlight, "session switch must schedule a queue refresh")
	require.True(t, m.busyFetchInFlight, "session switch must schedule a busy refresh")

	runCmds(m, cmd)
	require.Equal(t, 2, m.promptQueue, "the new session's queue must be fetched")
	require.Equal(t, []string{"a", "b"}, m.promptQueueItems)
}

// TestSessionSwitchLoadsTheTranscriptOffThread is B3. Loading a session
// costs one ListMessages round-trip plus one more per nested agent tool
// call, and it used to run inline in Update, stalling the render loop
// for the whole tree.
func TestSessionSwitchLoadsTheTranscriptOffThread(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)

	_, cmd := m.Update(loadSessionMsg{session: &session.Session{ID: "s2"}})
	// ListMessages deliberately left unstubbed until after this point:
	// the transcript must not be fetched on the Update goroutine.

	active := workspace.ActiveAgent{}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().ListUserMessages(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	var listMessageCalls int
	ws.EXPECT().ListMessages(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string) ([]message.Message, error) {
			listMessageCalls++
			return nil, nil
		})

	runCmds(m, cmd)
	require.Equal(t, 1, listMessageCalls, "the transcript must still be fetched")
}

// TestCyclePermissionModeWritesThroughCache: both permission-mode cycle
// paths share cyclePermissionMode, which must write the known new value
// through the cache — no invalidation, no re-probe.
func TestCyclePermissionModeWritesThroughCache(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)

	// A single EXPECT (default Times(1)) both answers the cycle's read
	// and proves nothing reads again afterwards: a second call would
	// find no matching expectation left and fail.
	ws.EXPECT().PermissionMode().Return(permission.ModeManual)
	ws.EXPECT().PermissionSetMode(permission.ModeAutoAcceptEdits)

	got := m.cyclePermissionMode()
	require.Equal(t, permission.ModeAutoAcceptEdits, got)

	require.Equal(t, permission.ModeAutoAcceptEdits, m.permissionModeCached(), "the new value must be served from the cache")
	require.True(t, m.permissionModeCache.fresh(busyCacheTTL), "write-through must stamp the cache fresh")
	m.permissionModeCached()
	m.permissionModeCached() // reads after the cycle must not re-probe

	ws.EXPECT().PermissionMode().Return(permission.ModeAutoAcceptEdits)
	ws.EXPECT().PermissionSetMode(permission.ModeYolo)
	got = m.cyclePermissionMode()
	require.Equal(t, permission.ModeYolo, got)
	require.Equal(t, permission.ModeYolo, m.permissionModeCached())
}

// TestCyclePermissionModeSupersedesInFlightProbe pins the generation bump
// in cyclePermissionMode: a busy/permission-mode probe dispatched before
// the cycle carries the old generation. Without advancing busyFetchGen its
// stale result would land with a still-matching generation and clobber the
// just-cycled value.
func TestCyclePermissionModeSupersedesInFlightProbe(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)

	// A busy/permission-mode probe carrying the pre-cycle generation is in
	// flight.
	m.busyFetchInFlight = true
	staleGen := m.busyFetchGen

	ws.EXPECT().PermissionMode().Return(permission.ModeManual)
	ws.EXPECT().PermissionSetMode(permission.ModeAutoAcceptEdits)
	require.Equal(t, permission.ModeAutoAcceptEdits, m.cyclePermissionMode())
	require.NotEqual(t, staleGen, m.busyFetchGen,
		"cycling must advance the busy generation to supersede in-flight probes")
	require.Equal(t, permission.ModeAutoAcceptEdits, m.permissionModeCached(), "cycling must write the new value through the cache")

	// The stale probe (old generation, old mode) lands. This is a direct
	// applyBusyState call, not a run command, so it triggers no further
	// workspace calls even though it re-dispatches a refresh.
	m.busyFetchInFlight = true
	cmds := m.applyBusyState(busyStateMsg{gen: staleGen, mode: permission.ModeManual})
	require.Equal(t, permission.ModeAutoAcceptEdits, m.permissionModeCached(),
		"stale probe must not overwrite the freshly cycled value")
	require.NotEmpty(t, cmds, "stale probe must re-dispatch an authoritative refresh")
	require.True(t, m.busyFetchInFlight, "re-dispatched refresh must be in flight")
}

// TestSendMessageSetsOptimisticBusy pins the esc-after-enter behavior:
// submitting a prompt optimistically marks the agent busy so an immediate
// esc routes to cancelAgent instead of reading a stale idle value and doing
// nothing.
func TestSendMessageSetsOptimisticBusy(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	ws.EXPECT().AgentReadyErr().Return(nil) // workspace still reports ready

	require.False(t, m.isAgentBusy())
	cmd := m.sendMessage("hello") // returned cmds (AgentRun etc.) deliberately not run
	require.NotNil(t, cmd)
	require.True(t, m.isAgentBusy(),
		"sendMessage must optimistically mark the agent busy")

	// esc right after enter: isAgentBusy gates cancelAgent, first press
	// arms the double-press cancel.
	require.Zero(t, m.promptQueue)
	m.cancelAgent()
	require.True(t, m.isCanceling, "first esc press must arm cancellation")

	// Second press must actually cancel.
	ws.EXPECT().AgentCancel(gomock.Any())
	m.cancelAgent()
}

// TestCancelAgentClearsQueueFromCachedCount: the queue-clear decision must
// come from the memoized count — no synchronous AgentQueuedPrompts probe —
// clearing must zero the cached count immediately, and the queued text
// must reappear in the editor instead of being discarded.
func TestCancelAgentClearsQueueFromCachedCount(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, true)
	m.promptQueue = 1
	m.promptQueueItems = []string{"a"}

	ws.EXPECT().AgentClearQueue(gomock.Any())
	// AgentQueuedPrompts/AgentQueuedPromptsList deliberately left
	// unstubbed: the decision must use the cached count, not a probe.

	m.cancelAgent()
	require.Zero(t, m.promptQueue, "the cached count must be zeroed immediately")
	require.Empty(t, m.promptQueueItems)
	require.False(t, m.isCanceling, "clearing the queue must not arm cancellation")
	require.Equal(t, "a", m.textarea.Value(),
		"the queued prompt must reappear in the editor instead of being lost")
}

// TestCancelAgentRestoresQueueOnActiveCancel: the second esc press cancels
// the active turn, which also drops any prompts still queued behind it on
// the backend. That must not lose the text — it has to reappear in the
// editor exactly like a queue-only clear does.
func TestCancelAgentRestoresQueueOnActiveCancel(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, true)
	m.isCanceling = true       // first esc press already armed cancellation
	m.busyFetchInFlight = true // keeps dispatchBusyRefresh's returned cmd nil
	m.promptQueue = 1
	m.promptQueueItems = []string{"queued follow-up"}

	ws.EXPECT().AgentCancel(gomock.Any())

	m.cancelAgent()
	require.Zero(t, m.promptQueue, "the cached count must be zeroed immediately")
	require.Empty(t, m.promptQueueItems)
	require.Equal(t, "queued follow-up", m.textarea.Value(),
		"a prompt still queued behind the cancelled turn must reappear in the editor")
}

// TestCancelAgentRestoresQueueAheadOfDraft pins the merge order: restored
// queue text must lead, in queue order, with whatever the user was
// already typing kept intact after it — a cancel must never clobber a
// half-typed follow-up.
func TestCancelAgentRestoresQueueAheadOfDraft(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, true)
	m.promptQueue = 2
	m.promptQueueItems = []string{"first queued", "second queued"}
	m.textarea.SetValue("still typing this")

	ws.EXPECT().AgentClearQueue(gomock.Any())

	m.cancelAgent()
	require.Equal(t, "first queued\n\nsecond queued\n\nstill typing this", m.textarea.Value())
}

// TestBackstopRefreshesStaleCaches: when the memoized state outlives its TTL
// with no event edge, the Update tail schedules exactly one off-thread
// refresh (deduplicated while in flight) and the result lands as a message.
func TestBackstopRefreshesStaleCaches(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	// Caches start at their zero value: stale by definition.

	_, cmd := m.Update(plainMsg{})
	require.True(t, m.busyFetchInFlight, "stale caches must trigger a backstop refresh")
	// Nothing stubbed yet: the backstop itself must not probe
	// synchronously.

	// A second Update while the fetch is in flight must not stack another.
	before := m.busyFetchInFlight
	m.Update(plainMsg{})
	require.Equal(t, before, m.busyFetchInFlight)

	var agentBusyCalls int
	active := workspace.ActiveAgent{}
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	ws.EXPECT().AgentIsBusy().DoAndReturn(func() bool { agentBusyCalls++; return true })
	ws.EXPECT().AgentActive(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string) (workspace.ActiveAgent, error) {
			return active, nil
		}).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()

	runCmds(m, cmd)
	require.False(t, m.busyFetchInFlight)
	require.True(t, m.isAgentBusy(), "the backstop result must land in the cache")
	require.Equal(t, 1, agentBusyCalls, "exactly one probe per backstop refresh")

	// Freshly refreshed caches must not re-dispatch.
	m.Update(plainMsg{})
	require.False(t, m.busyFetchInFlight, "fresh caches must not re-dispatch the backstop")
}

// TestSetSessionMessagesGatesAnimationsOnBusy verifies that reloading a
// session does not start spinner animations when the agent is not busy.
// A session that was killed mid-generation can persist an assistant message
// with no Finish part, which still reports isSpinning() even though nothing
// is running. Starting animations for it would leave a ghost "working"
// spinner after the session is reloaded.
func TestSetSessionMessagesGatesAnimationsOnBusy(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)

	// A message that looks unfinished (no Finish part, no content).
	msgs := []message.Message{
		{
			ID:        "m1",
			SessionID: "s1",
			Role:      message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "thinking..."},
			},
		},
	}

	// When the agent is not busy, applying items must not start animations.
	items, _ := m.buildSessionItems("s1", msgs)
	cmd := m.applySessionItems(items)
	require.Nil(t, cmd, "applySessionItems must not start animations when agent is idle")

	// When the agent is busy, animations should start.
	warmCaches(m, true)
	items, _ = m.buildSessionItems("s1", msgs)
	cmd = m.applySessionItems(items)
	require.NotNil(t, cmd, "applySessionItems must start animations when agent is busy")
}

// TestStaleBusyRefreshDiscardedAndReDispatched pins the generation guard for
// busy/permission state: a probe started before a newer state transition
// (here an optimistic busy write) must not overwrite the newer value when it
// lands, and the authoritative refresh must not be lost merely because the
// older probe was in flight — the stale result re-dispatches it.
func TestStaleBusyRefreshDiscardedAndReDispatched(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)

	// A busy probe is in flight; capture the generation it was dispatched
	// with, then a newer transition (optimistic send) supersedes it.
	m.busyFetchInFlight = true
	staleGen := m.busyFetchGen
	m.agentBusyCache.set(true) // optimistic busy
	m.busyFetchGen++           // newer state transition

	// The stale probe (agent reported idle) lands with the old
	// generation. This is a direct applyBusyState call, so re-dispatch
	// only builds a cmd — it never invokes the workspace.
	cmds := m.applyBusyState(busyStateMsg{gen: staleGen, agentBusy: false})
	require.True(t, m.isAgentBusy(),
		"a stale busy result must not overwrite the newer optimistic busy state")
	require.NotEmpty(t, cmds,
		"a stale busy result must re-dispatch the authoritative refresh")
	require.True(t, m.busyFetchInFlight, "the re-dispatched probe must be in flight")

	// The fresh probe (matching generation) is applied normally.
	freshGen := m.busyFetchGen
	m.applyBusyState(busyStateMsg{gen: freshGen, agentBusy: false})
	require.False(t, m.isAgentBusy(), "a current-generation result must land in the cache")
}

// TestStalePromptQueueDiscardedAndReDispatched pins the generation guard for
// the queue: a fetch started before a newer transition (here a queue clear)
// must not repopulate the cleared queue, and it must re-dispatch the
// authoritative fetch instead of being applied.
func TestStalePromptQueueDiscardedAndReDispatched(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)
	m.promptQueue = 1
	m.promptQueueItems = []string{"real"}

	// A fetch is in flight; capture its generation, then a newer transition
	// (esc clears the queue) supersedes it.
	m.promptQueueInFlight = true
	staleGen := m.promptQueueGen
	m.invalidatePromptQueue()
	m.promptQueue = 0
	m.promptQueueItems = nil

	// The stale fetch (still saw one prompt) lands for the same session.
	cmds := m.applyPromptQueue(promptQueueMsg{
		forSession: "s1",
		gen:        staleGen,
		prompts:    []string{"stale"},
	})
	require.Zero(t, m.promptQueue,
		"a stale queue result must not repopulate the cleared queue")
	require.Empty(t, m.promptQueueItems)
	require.NotEmpty(t, cmds,
		"a stale queue result must re-dispatch the authoritative fetch")
	require.True(t, m.promptQueueInFlight, "the re-dispatched fetch must be in flight")
}

// TestStalePromptQueuePreservesSessionScoping pins that the generation guard
// does not weaken session scoping: a fetch scoped to a different session is
// still discarded and re-fetched even when its generation would otherwise
// match.
func TestStalePromptQueuePreservesSessionScoping(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t) // active session "s1"
	warmCaches(m, false)
	m.promptQueueInFlight = true
	gen := m.promptQueueGen

	cmds := m.applyPromptQueue(promptQueueMsg{
		forSession: "other",
		gen:        gen,
		prompts:    []string{"from other session"},
	})
	require.Zero(t, m.promptQueue,
		"a result from a different session must never populate the queue")
	require.NotEmpty(t, cmds, "a session-mismatched result must re-fetch for the current session")
}

// TestRenderHelpersDoNotProbeWorkspace pins the render-path side of the
// invariant for the model and LSP info: activeAgent, lspInfo, and
// lspErrorCount render from memoized state only. They run on every frame
// (landing view, sidebar, compact header), and the probes behind them
// (AgentIsReady, AgentActive, LSPGetStates, LSPGetDiagnosticCounts) are
// synchronous HTTP round-trips in client/server mode.
func TestRenderHelpersDoNotProbeWorkspace(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.lspStates = map[string]workspace.LSPClientInfo{
		"gopls": {Name: "gopls", State: lsp.StateReady, DiagnosticCount: 3},
	}
	m.lspDiagnostics = map[string]lsp.DiagnosticCounts{
		"gopls": {Error: 2, Warning: 1},
	}

	for range 10 {
		require.NotNil(t, m.activeAgent())
		m.lspInfo(40, 5, true)
		require.Equal(t, 3, m.lspErrorCount())
	}

	// modelInfo reaches provider config only through the memoized model;
	// with the agent not ready it renders the empty state.
	m.agentReady = false
	for range 10 {
		m.modelInfo(40)
	}
}

// TestBusyRefreshCarriesReadyAndModel: the off-thread busy probe must also
// deliver the coordinator's readiness and selected model so the sidebar and
// landing view render them without per-frame probes.
func TestBusyRefreshCarriesReadyAndModel(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	require.Nil(t, m.activeAgent(), "before any probe the model is unknown")

	// Caches start stale, so the plainMsg backstop below dispatches all
	// three refreshes (busy, queue, LSP) at once.
	active := workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "test-model", Provider: "prov"}}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()

	_, cmd := m.Update(plainMsg{}) // stale caches: the backstop dispatches
	runCmds(m, cmd)

	require.True(t, m.agentReady, "the probe must land readiness in the cache")
	sel := m.activeAgent()
	require.NotNil(t, sel)
	require.Equal(t, "test-model", sel.ModelCfg.Model, "the probe must land the model in the cache")
}

// TestAgentModelChangedRefreshesModel: after a change to the session's
// agent (selection/thinking/variant cmds sequence agentModelChangedCmd),
// the handler must re-fetch ready/active off-thread — no synchronous
// probe — and the fresh model must replace the memoized one.
func TestAgentModelChangedRefreshesModel(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	m.agentActive = workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "old-model"}}

	_, cmd := m.Update(agentModelChangedMsg{})
	require.True(t, m.busyFetchInFlight, "a model change must schedule a ready/model refresh")
	// Nothing stubbed yet: the model-change handler must not probe
	// synchronously.

	active := workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "new-model"}}
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)

	runCmds(m, cmd)
	require.Equal(t, "new-model", m.agentActive.ModelCfg.Model,
		"the refreshed model must land in the cache")
}

// TestMCPStateChangedRefreshesModel pins the remaining UpdateAgentModel
// call site: an MCP state change rebuilds the agent, which can change the
// effective model, so the memoized ready/active state must be re-fetched
// off-thread afterwards — the edge the refreshActiveAgentCmd helper exists
// to make unforgettable.
func TestMCPStateChangedRefreshesModel(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	m.agentActive = workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "pre-mcp-model"}}

	// active is shared between UpdateAgentModel (which rewrites it, the
	// way a rebuild changes the effective model) and AgentActive (which
	// reads whatever it currently holds). Only the rebuild makes the new
	// model observable, so a probe that runs before it — or a rebuild
	// that never runs — would read the old value; the assertion below
	// is only satisfiable if the refresh actually runs after the
	// rebuild.
	active := workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "pre-mcp-model"}}
	var updateAgentModelCalls int
	ws.EXPECT().UpdateAgentModel(gomock.Any()).DoAndReturn(func(context.Context) error {
		updateAgentModelCalls++
		active = workspace.ActiveAgent{ModelCfg: config.SelectedModel{Model: "post-mcp-model"}}
		return nil
	})
	ws.EXPECT().MCPGetStates().Return(nil).AnyTimes()
	stubBusyProbe(ws, true, false, permission.ModeManual, &active)

	runCmds(m, m.handleStateChanged())

	require.Equal(t, 1, updateAgentModelCalls, "an MCP state change must rebuild the agent")
	require.True(t, m.agentReady)
	require.Equal(t, "post-mcp-model", m.agentActive.ModelCfg.Model,
		"the refresh must run after the rebuild, or it memoizes the pre-rebuild model")
}

// TestLSPEventRefreshIsOffThreadAndDeduped pins the LSP side of the
// invariant: an LSP event must not fetch states synchronously in Update
// (LSPGetStates + per-server LSPGetDiagnosticCounts are HTTP round-trips in
// client/server mode, and diagnostics events arrive per edited file). It
// schedules one off-thread fetch, dedups while one is in flight, and
// re-dispatches a queued refresh when the in-flight fetch lands.
func TestLSPEventRefreshIsOffThreadAndDeduped(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)

	_, cmd := m.Update(pubsub.Event[workspace.LSPEvent]{
		Payload: workspace.LSPEvent{Type: workspace.LSPEventDiagnosticsChanged, Name: "gopls"},
	})
	require.True(t, m.lspFetchInFlight, "an LSP event must schedule an off-thread refresh")
	// Nothing stubbed yet: the LSP event handler must not probe
	// synchronously.

	// A second event while the fetch is in flight queues a re-fetch instead
	// of stacking another dispatch.
	m.Update(pubsub.Event[workspace.LSPEvent]{
		Payload: workspace.LSPEvent{Type: workspace.LSPEventDiagnosticsChanged, Name: "gopls"},
	})
	require.True(t, m.lspRefreshQueued, "an event during an in-flight fetch must queue a re-fetch")
	// Still nothing stubbed: a second event while one is in flight must
	// not probe either.

	var lspStateCalls int
	ws.EXPECT().LSPGetStates().DoAndReturn(func() map[string]workspace.LSPClientInfo {
		lspStateCalls++
		return map[string]workspace.LSPClientInfo{"gopls": {Name: "gopls", DiagnosticCount: 3}}
	}).Times(2)
	ws.EXPECT().LSPGetDiagnosticCounts("gopls").
		Return(lsp.DiagnosticCounts{Error: 2, Warning: 1}).AnyTimes()

	runCmds(m, cmd)
	require.False(t, m.lspFetchInFlight)
	require.False(t, m.lspRefreshQueued, "the queued flag must clear once the re-dispatched fetch lands")
	require.Equal(t, 3, m.lspStates["gopls"].DiagnosticCount, "fetched states must land in the cache")
	require.Equal(t, 2, m.lspDiagnostics["gopls"].Error, "fetched severity counts must land in the cache")
	require.Equal(t, 3, m.lspErrorCount())
	require.Equal(t, 2, lspStateCalls, "one fetch plus the queued re-fetch")
}

// TestRemotePermissionModeChangeUpdatesEditorPrompt pins the second fix:
// when an asynchronous busy-state refresh reports a permission mode
// different from the cached one (a remote change), applyBusyState must
// rebuild the textarea prompt function too, not just the cache —
// otherwise the rail keeps rendering the old mode's color.
func TestRemotePermissionModeChangeUpdatesEditorPrompt(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	m.textarea.Focus()
	m.textarea.SetWidth(40)
	m.permissionModeCache.set(permission.ModeManual)
	m.setEditorPrompt(permission.ModeManual)
	normalPrompt := m.textarea.View()
	require.NotContains(t, m.editorCaption(100), "yolo")

	// A remote change switches to yolo; delivered via an off-thread refresh.
	m.applyBusyState(busyStateMsg{gen: m.busyFetchGen, mode: permission.ModeYolo})
	require.Equal(t, permission.ModeYolo, m.permissionModeCached(), "the refresh must write the new mode through the cache")
	yoloPrompt := m.textarea.View()
	require.NotEqual(t, normalPrompt, yoloPrompt,
		"a remote mode change must recolor the editor rail")
	require.Contains(t, m.editorCaption(100), "yolo",
		"the caption carries the mode for readers who cannot see the rail color")

	// Switching back to manual must restore the normal prompt.
	m.applyBusyState(busyStateMsg{gen: m.busyFetchGen, mode: permission.ModeManual})
	require.Equal(t, permission.ModeManual, m.permissionModeCached())
	require.Equal(t, normalPrompt, m.textarea.View(),
		"switching back to manual must restore the normal editor rail")
	require.NotContains(t, m.editorCaption(100), "yolo")
}
