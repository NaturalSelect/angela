package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newSummarizeGateUI builds a UI backed by a mock workspace with the
// commands dialog open, so ActionSummarize dispatch can be driven the same
// way the command palette drives it. The mock enforces its expectations
// structurally: an unexpected call fails the test via ctrl.Finish, and an
// expected call that never happens fails it too.
func newSummarizeGateUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()

	m := &UI{
		com:        &common.Common{Workspace: ws},
		session:    &session.Session{ID: "current"},
		agentReady: true,
	}
	m.dialog = dialog.NewOverlay(passThroughDialog{})
	return m
}

// TestSummarizeIgnoresUnrelatedSessionBusy is the regression for the
// /compact bug: another session's run must not block a manual compact on
// an idle one. The mock only expects the session-scoped check for
// "current" — if the dispatch fell back to a process-wide busy cache
// reflecting another session's activity, the call sequence here would not
// match and the test would fail.
func TestSummarizeIgnoresUnrelatedSessionBusy(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(false)
	ws.EXPECT().AgentSummarize(gomock.Any(), "current").Return(nil)

	m := newSummarizeGateUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionSummarize{SessionID: "current"})
	require.NotNil(t, cmd, "an idle session's summarize must be dispatched, not refused")
	drain(cmd)
}

// TestSummarizeRefusesItsOwnSessionBusy pins the other half: a summarize
// request for a session that is itself busy must still be refused, and
// must never reach AgentSummarize — the mock has no expectation for it, so
// any call to it would fail the test immediately.
func TestSummarizeRefusesItsOwnSessionBusy(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(true)

	m := newSummarizeGateUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionSummarize{SessionID: "current"})
	require.NotNil(t, cmd)
	msg := cmd()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "a busy session must report the warning message, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}
