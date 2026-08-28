package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

// cancelRecordingWorkspace records which sessions were asked to cancel and
// which were asked to load, so a test can tell "cancel reached the backend"
// from "cancel reached the right session", and can observe a navigation
// that is otherwise only visible as a deferred command.
type cancelRecordingWorkspace struct {
	workspace.Workspace
	cancelled []string
	loaded    []string
}

func (w *cancelRecordingWorkspace) AgentCancel(sessionID string) {
	w.cancelled = append(w.cancelled, sessionID)
}

// The cancel path also fires an off-thread busy re-probe. It is not what
// these tests are about, so it is stubbed to the quietest answers that let
// it run to completion.
func (w *cancelRecordingWorkspace) AgentIsReady() bool           { return false }
func (w *cancelRecordingWorkspace) AgentIsBusy() bool            { return false }
func (w *cancelRecordingWorkspace) PermissionSkipRequests() bool { return false }

// SetCurrentSession is presence tracking that rides along with every load.
func (w *cancelRecordingWorkspace) SetCurrentSession(context.Context, string) error { return nil }

// GetSession is the first thing a session load does. Recording the id and
// failing here keeps the rest of the load off the test's back while still
// proving the navigation was started.
func (w *cancelRecordingWorkspace) GetSession(_ context.Context, sessionID string) (session.Session, error) {
	w.loaded = append(w.loaded, sessionID)
	return session.Session{}, errors.New("stub: not loading in tests")
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
func newBranchCancelUI(t *testing.T, busy bool) (*UI, *cancelRecordingWorkspace) {
	t.Helper()

	ws := &cancelRecordingWorkspace{}
	ui := &UI{
		com:          &common.Common{Workspace: ws},
		session:      &session.Session{ID: "branch-1", ParentSessionID: "parent-1", Agent: "pairing"},
		sessionStack: []sessionStackFrame{{id: "parent-1", title: "parent"}},
		agentReady:   true,
	}
	ui.sessionIsBranch = true
	ui.agentBusyCache.set(busy)
	return ui, ws
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
			com:        &common.Common{Workspace: &cancelRecordingWorkspace{}},
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
			com:          &common.Common{Workspace: &cancelRecordingWorkspace{}},
			session:      &session.Session{ID: "sub", ParentSessionID: "root", Agent: "task"},
			sessionStack: []sessionStackFrame{{id: "root", title: "root"}},
			agentReady:   true,
		}
		ui.agentBusyCache.set(false)
		require.False(t, ui.escapeCancels(),
			"escape must still leave a sub-agent transcript rather than cancel")
	})

	t.Run("a busy root session still claims the key", func(t *testing.T) {
		t.Parallel()

		ui := &UI{
			com:        &common.Common{Workspace: &cancelRecordingWorkspace{}},
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
			com:        &common.Common{Workspace: &cancelRecordingWorkspace{}},
			session:    &session.Session{ID: "root"},
			agentReady: true,
		}
		ui.agentBusyCache.set(true)
		require.False(t, ui.cancelLeavesBranch())
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
	require.NotContains(t, ws.loaded, "parent-1",
		"interrupting a turn must leave the user in the branch")
}

// TestEscOnAnOrdinarySessionNeverNavigates is the regression guard on a
// global hot path: opening the gate for branches must not make an ordinary
// cancel start reloading sessions.
func TestEscOnAnOrdinarySessionNeverNavigates(t *testing.T) {
	t.Parallel()

	ws := &cancelRecordingWorkspace{}
	ui := &UI{
		com:         &common.Common{Workspace: ws},
		session:     &session.Session{ID: "root"},
		agentReady:  true,
		isCanceling: true,
	}
	ui.agentBusyCache.set(true)

	drain(ui.cancelAgent())
	require.Equal(t, []string{"root"}, ws.cancelled)
	require.Empty(t, ws.loaded, "cancelling an ordinary turn must not navigate")
}

// TestAbortBranchAbandonsAndReturns pins the /abort handler: it cancels the
// session it names and hands the view back.
func TestAbortBranchAbandonsAndReturns(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, true)

	drain(ui.abortBranch("branch-1"))

	require.Equal(t, []string{"branch-1"}, ws.cancelled,
		"the command must abandon the branch it names")
	require.Contains(t, ws.loaded, "parent-1",
		"aborting must return to the parent conversation")
}

// TestAbortBranchIsUnconditional separates the command from the escape
// gesture: escape spares a busy branch, but a user who typed "abort" meant
// it whether or not a turn happens to be running.
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
			require.Equal(t, []string{"branch-1"}, ws.cancelled)
			require.Contains(t, ws.loaded, "parent-1")
		})
	}
}
