package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// stubSessionSwitch wires the workspace calls a switch to another
// session makes, with the busy probe answering busy for every session.
func stubSessionSwitch(ws *MockWorkspace, busy bool) {
	active := workspace.ActiveAgent{}
	stubBusyProbe(ws, true, busy, permission.ModeManual, &active)
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).Return(nil).AnyTimes()
	ws.EXPECT().ListMessages(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	ws.EXPECT().ListUserMessages(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
}

// TestSessionSwitchToABusySessionStartsItsAnimations is the review case:
// the user leaves an idle session while its own busy probe is still in
// flight, so the probe for the new session cannot be dispatched yet. The
// new session's transcript then loads before its busy answer does, and
// its in-flight items must start animating once that answer lands
// instead of staying frozen until something else restarts them.
func TestSessionSwitchToABusySessionStartsItsAnimations(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	m.busyFetchInFlight = true
	stubSessionSwitch(ws, true)

	m.Update(loadSessionMsg{session: &session.Session{ID: "s2"}})
	require.True(t, m.busyFetchInFlight, "the old session's probe is still the one in flight")

	item := &testAnimatableItem{testMessageItem: testMessageItem{id: "running", text: "r"}}
	m.Update(sessionMessagesMsg{sessionID: "s2", items: []chat.MessageItem{item}})
	require.Zero(t, item.startAnimationCalls, "the busy answer has not landed yet")

	// The old probe lands, is discarded for its stale generation, and
	// re-dispatches the probe for s2, which reports the turn running.
	_, cmd := m.Update(busyStateMsg{gen: m.busyFetchGen - 1, forSession: "s1"})
	runCmds(m, cmd)

	require.True(t, m.isSessionBusy())
	require.Equal(t, 1, item.startAnimationCalls,
		"the busy answer arriving after the transcript must start the items it left frozen")
}

// TestSessionSwitchDropsThePreviousSessionsBusyAnswer is the other
// direction: leaving a busy session for an idle one must not let the
// stale flag start animations on the idle session's unfinished items,
// which would leave a ghost spinner behind.
func TestSessionSwitchDropsThePreviousSessionsBusyAnswer(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, true)
	m.busyFetchInFlight = true
	stubSessionSwitch(ws, false)

	m.Update(loadSessionMsg{session: &session.Session{ID: "s2"}})
	require.False(t, m.isSessionBusy(), "s1's answer says nothing about s2")

	item := &testAnimatableItem{testMessageItem: testMessageItem{id: "killed", text: "k"}}
	m.Update(sessionMessagesMsg{sessionID: "s2", items: []chat.MessageItem{item}})
	require.Zero(t, item.startAnimationCalls)
}

// TestReloadingTheSameSessionKeepsItsBusyAnswer pins that only a real
// switch drops the flag: reloading the open session (after a reconnect,
// say) must not blank a turn that is still running.
func TestReloadingTheSameSessionKeepsItsBusyAnswer(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, true)
	stubSessionSwitch(ws, true)

	m.Update(loadSessionMsg{session: &session.Session{ID: "s1"}})
	require.True(t, m.isSessionBusy())
}

// TestBusyEdgeDoesNotRestartAnimationsOfARunningSession pins that the
// edge restart fires once, on idle to busy. A probe that merely confirms
// a busy session would otherwise restart every spinner on each refresh.
func TestBusyEdgeDoesNotRestartAnimationsOfARunningSession(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	warmCaches(m, true)
	item := &testAnimatableItem{testMessageItem: testMessageItem{id: "running", text: "r"}}
	m.chat.SetMessages(item)

	m.busyFetchInFlight = true
	m.applyBusyState(busyStateMsg{gen: m.busyFetchGen, sessionBusy: true, agentBusy: true, mode: permission.ModeManual})

	require.Zero(t, item.startAnimationCalls)
}
