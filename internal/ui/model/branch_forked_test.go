package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// TestBranchForkedNotifiesWhenNotOnScreen pins the common case: a branch
// forked from a session other than the one on screen must reach the user
// as a toast, since nothing already visible shows that it exists.
func TestBranchForkedNotifiesWhenNotOnScreen(t *testing.T) {
	pinTTLs(t)
	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	ws.EXPECT().ParseAgentToolSessionID("msg-1$$call-1").Return("msg-1", "call-1", true)

	_, cmd := m.Update(pubsub.Event[notify.Notification]{
		Type: pubsub.CreatedEvent,
		Payload: notify.Notification{
			Type:         notify.TypeBranchForked,
			SessionID:    "msg-1$$call-1",
			SessionTitle: "fix the flaky test",
		},
	})

	require.NotNil(t, cmd, "a fork off screen must be surfaced")
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "expected a util.InfoMsg toast, got %T", cmd())
	require.Contains(t, info.Msg, "fix the flaky test")
}

// TestBranchForkedStaysQuietWhenOnScreen pins the other side: when the
// agent tool call that forked the branch is already part of the
// transcript on screen, its waiting state is visible there, so a toast
// would only repeat what the user can already see.
func TestBranchForkedStaysQuietWhenOnScreen(t *testing.T) {
	pinTTLs(t)
	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	ws.EXPECT().ParseAgentToolSessionID("msg-1$$call-1").Return("msg-1", "call-1", true)
	m.chat.SetMessages(agentItem(m, "msg-1", "call-1"))

	_, cmd := m.Update(pubsub.Event[notify.Notification]{
		Type: pubsub.CreatedEvent,
		Payload: notify.Notification{
			Type:         notify.TypeBranchForked,
			SessionID:    "msg-1$$call-1",
			SessionTitle: "fix the flaky test",
		},
	})

	require.Nil(t, cmd, "a fork already visible on screen needs no extra notification")
}

// TestBranchForkedNotifiesWhenSessionIDDoesNotParse pins that a branch
// session ID which fails to parse is treated the same as one that
// simply isn't on screen: branchForkVisible has nothing to look up,
// so it must fail closed to "not visible" rather than silently
// swallowing the notification.
func TestBranchForkedNotifiesWhenSessionIDDoesNotParse(t *testing.T) {
	pinTTLs(t)
	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	ws.EXPECT().ParseAgentToolSessionID("not-a-real-session-id").Return("", "", false)

	cmd := m.handleBranchForked(notify.Notification{
		Type:         notify.TypeBranchForked,
		SessionID:    "not-a-real-session-id",
		SessionTitle: "fix the flaky test",
	})

	require.NotNil(t, cmd, "an unparseable session id must not be treated as already visible")
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "expected a util.InfoMsg toast, got %T", cmd())
	require.Contains(t, info.Msg, "fix the flaky test")
}

// TestBranchForkVisibleFalseWhenDependenciesMissing pins the guard
// clause ahead of the session-ID lookup: a UI missing its chat model
// or workspace (as happens before either finishes wiring up) must
// report "not visible" instead of dereferencing them.
func TestBranchForkVisibleFalseWhenDependenciesMissing(t *testing.T) {
	var m UI
	require.False(t, m.branchForkVisible("anything"),
		"a zero-value UI has no chat or workspace to check against")
}
