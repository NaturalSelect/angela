package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// cancelCalls records which sessions were asked to cancel, which were
// asked to be abandoned, and which were asked to load, so a test can tell
// "the request reached the backend" from "it reached the right session",
// tell the two gestures apart by the channel each took, and observe a
// navigation that is otherwise only visible as a deferred command.
type cancelCalls struct {
	cancelled []string
	abandoned []string
	loaded    []string
}

// newIdleMockWorkspace stubs the quietest answers (not ready, not busy,
// permissions not skipped) for tests that only resolve escapeCancels() /
// cancelLeavesBranch() and never reach AgentCancel, AgentAbandonBranch,
// or GetSession.
func newIdleMockWorkspace(t *testing.T) *MockWorkspace {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsReady().Return(false).AnyTimes()
	ws.EXPECT().AgentIsBusy().Return(false).AnyTimes()
	ws.EXPECT().PermissionMode().Return(permission.ModeManual).AnyTimes()
	return ws
}

// newCancelRecordingWorkspace extends newIdleMockWorkspace with recording
// for the three calls a confirmed cancel/abort can make: AgentCancel,
// AgentAbandonBranch, and the GetSession a returning navigation performs.
// GetSession is stubbed to fail deliberately, keeping the rest of the load
// off the test's back while still proving the navigation was started.
func newCancelRecordingWorkspace(t *testing.T) (*MockWorkspace, *cancelCalls) {
	t.Helper()

	ws := newIdleMockWorkspace(t)
	ws.EXPECT().SetCurrentSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	calls := &cancelCalls{}
	ws.EXPECT().AgentCancel(gomock.Any()).Do(func(sessionID string) {
		calls.cancelled = append(calls.cancelled, sessionID)
	}).AnyTimes()
	ws.EXPECT().AgentAbandonBranch(gomock.Any()).Do(func(sessionID string) {
		calls.abandoned = append(calls.abandoned, sessionID)
	}).AnyTimes()
	ws.EXPECT().GetSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sessionID string) (session.Session, error) {
			calls.loaded = append(calls.loaded, sessionID)
			return session.Session{}, errors.New("stub: not loading in tests")
		}).AnyTimes()

	return ws, calls
}

// drain runs a command and everything it batches, so the side effects a
// deferred load performs become observable.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			drain(sub)
		}
	}
}

// newBranchCancelUI builds a UI sitting inside a branch with one frame on
// the session stack, which is the shape both abort entry points act on.
func newBranchCancelUI(t *testing.T, busy bool) (*UI, *cancelCalls) {
	t.Helper()

	ws, calls := newCancelRecordingWorkspace(t)
	ui := &UI{
		com:          &common.Common{Workspace: ws},
		session:      &session.Session{ID: "branch-1", ParentSessionID: "parent-1", Agent: "pairing"},
		sessionStack: []sessionStackFrame{{id: "parent-1", title: "parent"}},
		agentReady:   true,
	}
	ui.sessionIsBranch = true
	ui.agentBusyCache.set(busy)
	return ui, calls
}

// TestEscapeCancelsGate pins which sessions let escape mean "stop" instead
// of "go back". An idle branch is the case the feature adds: it has no
// running turn, so before this the key fell through to navigation and the
// branch could never be abandoned.
func TestEscapeCancelsGate(t *testing.T) {
	t.Parallel()

	t.Run("an idle branch claims the key", func(t *testing.T) {
		t.Parallel()

		ui, _ := newBranchCancelUI(t, false)
		require.True(t, ui.escapeCancels())
	})

	t.Run("a busy branch claims the key", func(t *testing.T) {
		t.Parallel()

		ui, _ := newBranchCancelUI(t, true)
		require.True(t, ui.escapeCancels())
	})

	t.Run("an idle root session does not", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:        &common.Common{Workspace: newIdleMockWorkspace(t)},
			session:    &session.Session{ID: "root"},
			agentReady: true,
		}
		ui.agentBusyCache.set(false)
		require.False(t, ui.escapeCancels(),
			"escape must still fall through to navigation on an ordinary session")
	})

	t.Run("an idle sub-agent transcript does not", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:          &common.Common{Workspace: newIdleMockWorkspace(t)},
			session:      &session.Session{ID: "sub", ParentSessionID: "root", Agent: "task"},
			sessionStack: []sessionStackFrame{{id: "root", title: "root"}},
			agentReady:   true,
		}
		ui.agentBusyCache.set(false)
		require.False(t, ui.escapeCancels(),
			"escape must still leave a sub-agent transcript rather than cancel")
	})

	// Regression: a sub-agent transcript is almost always viewed while its
	// parent turn is still running — that is the case the user opens it to
	// watch. Before this the busy check fired first for every session, so
	// escape could never back out of a running sub-agent's transcript.
	t.Run("a busy sub-agent transcript does not", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:          &common.Common{Workspace: newIdleMockWorkspace(t)},
			session:      &session.Session{ID: "sub", ParentSessionID: "root", Agent: "task"},
			sessionStack: []sessionStackFrame{{id: "root", title: "root"}},
			agentReady:   true,
		}
		ui.agentBusyCache.set(true)
		require.False(t, ui.escapeCancels(),
			"escape must leave a running sub-agent transcript, not cancel the parent turn")
	})

	t.Run("a busy root session still claims the key", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:        &common.Common{Workspace: newIdleMockWorkspace(t)},
			session:    &session.Session{ID: "root"},
			agentReady: true,
		}
		ui.agentBusyCache.set(true)
		require.True(t, ui.escapeCancels(), "regression: busy sessions always cancelled")
	})
}

// TestCancelLeavesBranchGate pins the other half of the split: whether a
// confirmed cancel also navigates back. Getting this wrong either strands
// the user on a dead transcript or throws them out mid-conversation.
func TestCancelLeavesBranchGate(t *testing.T) {
	t.Parallel()

	t.Run("an idle branch is abandoned, so the view goes back", func(t *testing.T) {
		t.Parallel()

		ui, _ := newBranchCancelUI(t, false)
		require.True(t, ui.cancelLeavesBranch())
	})

	t.Run("a busy branch only loses its turn, so the view stays", func(t *testing.T) {
		t.Parallel()

		ui, _ := newBranchCancelUI(t, true)
		require.False(t, ui.cancelLeavesBranch(),
			"interrupting a reply must not throw the user out of the branch")
	})

	t.Run("an ordinary busy session never navigates on cancel", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:        &common.Common{Workspace: newIdleMockWorkspace(t)},
			session:    &session.Session{ID: "root"},
			agentReady: true,
		}
		ui.agentBusyCache.set(true)
		require.False(t, ui.cancelLeavesBranch())
	})

	// Regression: a branch opened without drilling down (e.g. picked
	// straight from the session switcher, which clears the session
	// stack) has nothing on sessionStack to pop even though it has a
	// parent. cancelLeavesBranch used to also require inSubSession and
	// so answered false here, leaving the view stranded on an abandoned
	// branch instead of following it back.
	t.Run("an idle branch with nothing on the stack still goes back", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:        &common.Common{Workspace: newIdleMockWorkspace(t)},
			session:    &session.Session{ID: "branch-1", ParentSessionID: "parent-1", Agent: "pairing"},
			agentReady: true,
		}
		ui.sessionIsBranch = true
		ui.agentBusyCache.set(false)

		require.False(t, ui.inSubSession(), "the fixture must start with nothing on the stack")
		require.True(t, ui.cancelLeavesBranch(),
			"a branch always names its own parent, so leaving must not depend on a breadcrumb")
	})
}

// TestEscOnAnIdleBranchAborts drives the real two-step cancel: the first
// press must only arm, and the second must reach AgentCancel naming the
// branch rather than the parent it returns to.
func TestEscOnAnIdleBranchAborts(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, false)

	ui.cancelAgent()
	require.True(t, ui.isCanceling, "the first press arms the two-step cancel")
	require.Empty(t, ws.cancelled, "the first press must not abandon the branch")

	drain(ui.cancelAgent())
	require.False(t, ui.isCanceling)
	require.Equal(t, []string{"branch-1"}, ws.cancelled,
		"the abort must name the branch, not the parent it returns to")
	require.Empty(t, ws.abandoned,
		"escape stays on the shared cancel, which abandons an idle branch itself")
	require.Contains(t, ws.loaded, "parent-1",
		"an abandoned branch must hand the view back to its parent")
}

// TestEscOnABusyBranchOnlyInterruptsTheTurn pins that a running turn can be
// stopped without giving up the branch, so the user can cut off a reply that
// went sideways and keep talking.
func TestEscOnABusyBranchOnlyInterruptsTheTurn(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, true)
	ui.isCanceling = true

	drain(ui.cancelAgent())
	require.Equal(t, []string{"branch-1"}, ws.cancelled)
	require.Empty(t, ws.abandoned,
		"escape must never reach for the outcome only /abort names")
	require.NotContains(t, ws.loaded, "parent-1",
		"interrupting a turn must leave the user in the branch")
}

// TestEscOnAnOrdinarySessionNeverNavigates is the regression guard on a
// global hot path: opening the gate for branches must not make an ordinary
// cancel start reloading sessions.
func TestEscOnAnOrdinarySessionNeverNavigates(t *testing.T) {
	t.Parallel()

	ws, calls := newCancelRecordingWorkspace(t)
	ui := &UI{
		com:         &common.Common{Workspace: ws},
		session:     &session.Session{ID: "root"},
		agentReady:  true,
		isCanceling: true,
	}
	ui.agentBusyCache.set(true)

	drain(ui.cancelAgent())
	require.Equal(t, []string{"root"}, calls.cancelled)
	require.Empty(t, calls.abandoned, "an ordinary session has no branch to abandon")
	require.Empty(t, calls.loaded, "cancelling an ordinary turn must not navigate")
}

// TestAbortBranchAbandonsAndReturns pins the /abort handler: it abandons
// the session it names and hands the view back.
func TestAbortBranchAbandonsAndReturns(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, true)

	drain(ui.abortBranch("branch-1"))

	require.Equal(t, []string{"branch-1"}, ws.abandoned,
		"the command must abandon the branch it names")
	require.Contains(t, ws.loaded, "parent-1",
		"aborting must return to the parent conversation")
}

// TestAbortBranchIsUnconditional separates the command from the escape
// gesture: escape spares a busy branch, but a user who typed "abort" meant
// it whether or not a turn happens to be running.
//
// The channel it takes is the whole point, so it is asserted rather than
// assumed. The shared cancel spares a busy branch by design, so routing
// /abort back through it would silently restore the bug where the view left
// but the branch and its suspended parent stayed behind.
func TestAbortBranchIsUnconditional(t *testing.T) {
	t.Parallel()

	for _, busy := range []bool{false, true} {
		name := "idle branch"
		if busy {
			name = "busy branch"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ui, ws := newBranchCancelUI(t, busy)
			drain(ui.abortBranch("branch-1"))
			require.Equal(t, []string{"branch-1"}, ws.abandoned)
			require.Empty(t, ws.cancelled,
				"/abort must not fall back to the shared cancel path")
			require.Contains(t, ws.loaded, "parent-1")
		})
	}
}

// TestEscOnAnIdleBranchWithNoStackStillReachesTheParent is the full-chain
// regression for a branch opened without drilling down — e.g. picked
// directly from the session switcher, which clears the session stack via
// loadSessionOpt.clearStack. Before the fix, leaveSubSession indexed
// sessionStack unconditionally and cancelLeavesBranch refused to call it
// unless the stack was non-empty, so such a branch could be abandoned on
// the backend by the second Esc press while the view stayed on the now-dead
// transcript. leaveSubSession must fall back to the session's own
// ParentSessionID, and the two-step cancel must reach it end to end.
func TestEscOnAnIdleBranchWithNoStackStillReachesTheParent(t *testing.T) {
	t.Parallel()

	ws, calls := newCancelRecordingWorkspace(t)
	ui := &UI{
		com:        &common.Common{Workspace: ws},
		session:    &session.Session{ID: "branch-1", ParentSessionID: "parent-1", Agent: "pairing"},
		agentReady: true,
	}
	ui.sessionIsBranch = true
	ui.agentBusyCache.set(false)
	require.False(t, ui.inSubSession(), "the fixture must start with nothing on the stack")

	ui.cancelAgent()
	require.True(t, ui.isCanceling, "the first press arms the two-step cancel")
	require.Empty(t, calls.cancelled, "the first press must not abandon the branch")

	drain(ui.cancelAgent())
	require.False(t, ui.isCanceling)
	require.Equal(t, []string{"branch-1"}, calls.cancelled,
		"the abort must name the branch, not the parent it returns to")
	require.Contains(t, calls.loaded, "parent-1",
		"a branch with no stack frame must still hand the view back to its parent")
}

// TestAbortBranchWithNoStackStillReachesTheParent is /abort's counterpart
// to the Esc regression above: abortBranch used to gate the return
// navigation on inSubSession too, so /abort on a switcher-opened branch
// would abandon it on the backend and then strand the view on the dead
// transcript instead of returning to the parent.
func TestAbortBranchWithNoStackStillReachesTheParent(t *testing.T) {
	t.Parallel()

	ws, calls := newCancelRecordingWorkspace(t)
	ui := &UI{
		com:        &common.Common{Workspace: ws},
		session:    &session.Session{ID: "branch-1", ParentSessionID: "parent-1", Agent: "pairing"},
		agentReady: true,
	}
	ui.sessionIsBranch = true
	ui.agentBusyCache.set(true)
	require.False(t, ui.inSubSession(), "the fixture must start with nothing on the stack")

	drain(ui.abortBranch("branch-1"))

	require.Equal(t, []string{"branch-1"}, calls.abandoned)
	require.Contains(t, calls.loaded, "parent-1",
		"a branch with no stack frame must still hand the view back to its parent")
}
