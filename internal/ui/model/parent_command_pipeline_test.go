package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newParentPaletteUI builds a full Update-capable UI (chat, textarea,
// header, ... via newBusyUIWithWorkspace) with the real Commands dialog
// open, sitting inside a branch that was reached without drilling down —
// sessionStack is nil, the shape dialog.ActionSelectSession leaves behind
// when the session switcher jumps straight to a branch. This is exactly
// the case leaveSubSession could not resolve before it learned to fall
// back to the session's own ParentSessionID.
func newParentPaletteUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()

	ws.EXPECT().Config().Return(&config.Config{}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().IsInSandbox().Return(false).AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	m.session = &session.Session{ID: "branch-1", ParentSessionID: "parent-1", Agent: "pairing"}
	m.sessionIsBranch = true
	m.sessionStack = nil

	cmdsDialog, err := dialog.NewCommands(m.com, m.session.ID, true, false, false, m.viewingBranch(), m.hasParentSession(), nil, nil, nil)
	require.NoError(t, err)
	m.dialog = dialog.NewOverlay(cmdsDialog)

	return m
}

// TestParentPaletteReturnsToParentWithNoStack is the full-chain regression
// requested for the branch/parent navigation bug: it drives the real
// Commands palette by keyboard exactly like a user — open it, type
// "parent" to filter down to the Go to Parent entry, press enter — and
// then runs the resulting command tree through to the loadSessionMsg it
// produces, feeding that back into Update. The assertion is that the view
// actually lands on the parent session, not merely that a load was
// attempted: that stronger bar is what the earlier (session-stack-only)
// implementation of leaveSubSession would have failed, since this branch
// has nothing on its stack to pop.
func TestParentPaletteReturnsToParentWithNoStack(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().SetCurrentSession(gomock.Any(), "parent-1").Return(nil)
	ws.EXPECT().GetSession(gomock.Any(), "parent-1").Return(session.Session{ID: "parent-1", Title: "Parent"}, nil)
	ws.EXPECT().ListSessionHistory(gomock.Any(), "parent-1").Return(nil, nil)
	ws.EXPECT().FileTrackerListReadFiles(gomock.Any(), "parent-1").Return(nil, nil)
	ws.EXPECT().AgentIsSessionBranch("parent-1").Return(false)

	m := newParentPaletteUI(t, ws)
	require.False(t, m.inSubSession(), "the fixture must start with nothing on the stack")

	for _, r := range "parent" {
		drain(m.handleDialogMsg(charKeyMsg(r)))
	}
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "selecting /parent by keyboard produced no command")

	msgs := runCmds(m, cmd)
	var loaded *loadSessionMsg
	for _, msg := range msgs {
		if lm, ok := msg.(loadSessionMsg); ok {
			loaded = &lm
		}
	}
	require.NotNil(t, loaded, "/parent must dispatch a load of the branch's parent")

	m.Update(*loaded)

	require.Equal(t, "parent-1", m.session.ID, "the view must land on the parent session")
	require.False(t, m.viewingBranch(), "the parent session is not itself a branch")
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close")
}
