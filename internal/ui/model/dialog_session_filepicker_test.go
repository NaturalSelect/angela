package model

import (
	"context"
	"errors"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// sessionsDlgThreeSessions opens the Sessions dialog on top of a
// three-session workspace, with the currently active session ("s1")
// as the first / already-selected item.
func sessionsDlgThreeSessions(t *testing.T) *UI {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	sessions := []session.Session{
		{ID: "s1", Title: "One"},
		{ID: "s2", Title: "Two"},
		{ID: "s3", Title: "Three"},
	}
	ws.EXPECT().ListSessions(gomock.Any()).Return(sessions, nil)

	require.Nil(t, m.openSessionsDialog())
	return m
}

// ---------------------------------------------------------------------
// Sessions dialog: open guards / errors
// ---------------------------------------------------------------------

func TestSessionsDialog_OpenListSessionsErrorReportsAndDoesNotOpen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	boom := errors.New("list sessions failed: disk error")
	ws.EXPECT().ListSessions(gomock.Any()).Return(nil, boom)

	cmd := m.openSessionsDialog()
	require.NotNil(t, cmd)
	msg, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, msg.Type)
	require.Contains(t, msg.Msg, "list sessions failed: disk error")
	require.False(t, m.dialog.ContainsDialog(dialog.SessionsID))
}

func TestSessionsDialog_OpenEmptyListSucceedsAndSelectIsSafe(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return(nil, nil)

	cmd := m.openSessionsDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.SessionsID))

	require.NotPanics(t, func() {
		action := m.dialog.Update(keyMsg("enter"))
		require.Nil(t, action, "selecting with zero sessions must not fabricate a selection")
	})
}

func TestSessionsDialog_ReopenBringsToFrontNotDuplicated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	// Exactly once: the second open must short-circuit on
	// ContainsDialog before ever calling ListSessions again.
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)

	require.Nil(t, m.openSessionsDialog())
	require.Nil(t, m.openSessionsDialog())
	require.True(t, m.dialog.ContainsDialog(dialog.SessionsID))

	m.dialog.CloseDialog(dialog.SessionsID)
	require.False(t, m.dialog.ContainsDialog(dialog.SessionsID),
		"a lingering duplicate would still be open after a single close")
}

// ---------------------------------------------------------------------
// Sessions dialog: delete flow
// ---------------------------------------------------------------------

func TestSessionsDialog_DeleteGatedWhenSessionBusy(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
	ws.EXPECT().AgentIsReady().Return(true)
	ws.EXPECT().AgentIsSessionBusy("s1").Return(true)
	// Deliberately no DeleteSession expectation: gomock fails the test
	// if the busy gate does not stop the delete.

	require.Nil(t, m.openSessionsDialog())
	action := m.dialog.Update(ctrlKey('x'))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	msg, ok := ac.Cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.Contains(t, msg.Msg, "busy")
}

func TestSessionsDialog_DeleteNotBusyThenConfirmDeletesSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agentReady bool
	}{
		{"agent not ready short-circuits the busy check", false},
		{"agent ready but this session is idle", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			ws := NewMockWorkspace(ctrl)
			m := newDialogUI(t, ws)
			ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
			ws.EXPECT().AgentIsReady().Return(tt.agentReady)
			if tt.agentReady {
				ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
			}
			ws.EXPECT().DeleteSession(gomock.Any(), "s1").Return(nil)

			require.Nil(t, m.openSessionsDialog())
			require.Nil(t, m.dialog.Update(ctrlKey('x')), "entering delete-confirm mode returns no action")

			action := m.dialog.Update(keyMsg("y"))
			ac, ok := action.(dialog.ActionCmd)
			require.True(t, ok)
			require.Nil(t, ac.Cmd())
		})
	}
}

func TestSessionsDialog_DeleteConfirmWorkspaceErrorSurfaces(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
	ws.EXPECT().AgentIsReady().Return(false)
	boom := errors.New("delete failed: locked")
	ws.EXPECT().DeleteSession(gomock.Any(), "s1").Return(boom)

	require.Nil(t, m.openSessionsDialog())
	m.dialog.Update(ctrlKey('x'))
	action := m.dialog.Update(keyMsg("y"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	msg, ok := ac.Cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, msg.Type)
	require.Contains(t, msg.Msg, "delete failed: locked")
}

func TestSessionsDialog_DeleteCancelReturnsToNormalModeAndSelectStillWorks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cancelKey string
	}{
		{"cancel with n", "n"},
		{"cancel with esc", "esc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			ws := NewMockWorkspace(ctrl)
			m := newDialogUI(t, ws)
			ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
			ws.EXPECT().AgentIsReady().Return(false)
			// No DeleteSession expectation: cancelling must never call it.

			require.Nil(t, m.openSessionsDialog())
			m.dialog.Update(ctrlKey('x'))
			require.Nil(t, m.dialog.Update(keyMsg(tt.cancelKey)))

			action := m.dialog.Update(keyMsg("enter"))
			sel, ok := action.(dialog.ActionSelectSession)
			require.True(t, ok)
			require.Equal(t, "s1", sel.Session.ID)
		})
	}
}

// ---------------------------------------------------------------------
// Sessions dialog: rename flow
// ---------------------------------------------------------------------

func TestSessionsDialog_RenameEmptyTitleIsNoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
	// No SaveSession expectation: the rename input starts empty, and
	// confirming with nothing typed must be a silent no-op.

	require.Nil(t, m.openSessionsDialog())
	m.dialog.Update(ctrlKey('r'))
	require.Nil(t, m.dialog.Update(keyMsg("enter")))
}

func TestSessionsDialog_RenameWhitespaceOnlyIsNoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
	// No SaveSession expectation: TrimSpace must reduce this to empty.

	require.Nil(t, m.openSessionsDialog())
	m.dialog.Update(ctrlKey('r'))
	m.dialog.Update(keyMsg("   "))
	require.Nil(t, m.dialog.Update(keyMsg("enter")))
}

func TestSessionsDialog_RenameConfirmSavesTrimmedTitle(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "Old"}}, nil)
	ws.EXPECT().SaveSession(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, sess session.Session) (session.Session, error) {
			require.Equal(t, "s1", sess.ID)
			require.Equal(t, "New Title", sess.Title)
			return sess, nil
		})

	require.Nil(t, m.openSessionsDialog())
	m.dialog.Update(ctrlKey('r'))
	m.dialog.Update(keyMsg("  New Title  "))
	action := m.dialog.Update(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	require.Nil(t, ac.Cmd())
}

func TestSessionsDialog_RenameConfirmWorkspaceErrorSurfaces(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "Old"}}, nil)
	boom := errors.New("rename failed: conflict")
	ws.EXPECT().SaveSession(gomock.Any(), gomock.Any()).Return(session.Session{}, boom)

	require.Nil(t, m.openSessionsDialog())
	m.dialog.Update(ctrlKey('r'))
	m.dialog.Update(keyMsg("Renamed"))
	action := m.dialog.Update(keyMsg("enter"))
	ac, ok := action.(dialog.ActionCmd)
	require.True(t, ok)
	msg, ok := ac.Cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, msg.Type)
	require.Contains(t, msg.Msg, "rename failed: conflict")
}

func TestSessionsDialog_RenameCancelDiscardsInput(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1", Title: "One"}}, nil)
	// No SaveSession expectation: cancelling must never save.

	require.Nil(t, m.openSessionsDialog())
	m.dialog.Update(ctrlKey('r'))
	m.dialog.Update(keyMsg("abc"))
	require.Nil(t, m.dialog.Update(keyMsg("esc")))

	action := m.dialog.Update(keyMsg("enter"))
	sel, ok := action.(dialog.ActionSelectSession)
	require.True(t, ok)
	require.Equal(t, "One", sel.Session.Title,
		"the discarded edit must never have touched the underlying session")
}

// ---------------------------------------------------------------------
// Sessions dialog: navigation / filter
// ---------------------------------------------------------------------

func TestSessionsDialog_NavigationWrapsAcrossSessions(t *testing.T) {
	t.Parallel()

	t.Run("previous from first wraps to last", func(t *testing.T) {
		t.Parallel()
		m := sessionsDlgThreeSessions(t)
		m.dialog.Update(keyMsg("up"))
		action := m.dialog.Update(keyMsg("enter"))
		sel, ok := action.(dialog.ActionSelectSession)
		require.True(t, ok)
		require.Equal(t, "s3", sel.Session.ID)
	})

	t.Run("next three times wraps back to first", func(t *testing.T) {
		t.Parallel()
		m := sessionsDlgThreeSessions(t)
		m.dialog.Update(keyMsg("down"))
		m.dialog.Update(keyMsg("down"))
		m.dialog.Update(keyMsg("down"))
		action := m.dialog.Update(keyMsg("enter"))
		sel, ok := action.(dialog.ActionSelectSession)
		require.True(t, ok)
		require.Equal(t, "s1", sel.Session.ID)
	})
}

func TestSessionsDialog_FilterAdversarialInputDoesNotPanic(t *testing.T) {
	t.Parallel()

	adversarial := []string{
		"", " ", "\t\n", "(a)[b].*\\c+", "日本語🎉テスト", stringsRepeat("x", 500),
	}
	for _, s := range adversarial {
		t.Run(truncateForName(s), func(t *testing.T) {
			t.Parallel()
			m := sessionsDlgThreeSessions(t)
			require.NotPanics(t, func() {
				if s != "" {
					m.dialog.Update(keyMsg(s))
				}
				m.dialog.Update(keyMsg("enter"))
			})
		})
	}
}

// ---------------------------------------------------------------------
// FilePicker dialog
// ---------------------------------------------------------------------

func TestFilePickerDialog_OpenSucceedsAndInitializes(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().WorkingDir().Return(t.TempDir())

	cmd := m.openFilesDialog()
	require.True(t, m.dialog.ContainsDialog(dialog.FilePickerID))
	if cmd != nil {
		require.NotPanics(t, func() { cmd() })
	}
}

func TestFilePickerDialog_ReopenBringsToFrontNotDuplicated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	// Exactly once: the second open must short-circuit before
	// constructing (and re-querying WorkingDir for) a new picker.
	ws.EXPECT().WorkingDir().Return(t.TempDir())

	m.openFilesDialog()
	m.openFilesDialog()
	require.True(t, m.dialog.ContainsDialog(dialog.FilePickerID))

	m.dialog.CloseDialog(dialog.FilePickerID)
	require.False(t, m.dialog.ContainsDialog(dialog.FilePickerID),
		"a lingering duplicate would still be open after a single close")
}

func TestFilePickerDialog_CloseKeyReturnsActionClose(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().WorkingDir().Return(t.TempDir())
	m.openFilesDialog()

	action := m.dialog.Update(keyMsg("esc"))
	require.Equal(t, dialog.ActionClose{}, action)
}

func TestFilePickerDialog_AltEscAlsoCloses(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().WorkingDir().Return(t.TempDir())
	m.openFilesDialog()

	action := m.dialog.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModAlt})
	require.Equal(t, dialog.ActionClose{}, action)
}

func TestFilePickerDialog_SetImageCapabilitiesNilIsSafe(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	ws.EXPECT().WorkingDir().Return(t.TempDir())

	fp, _ := dialog.NewFilePicker(m.com)
	require.NotPanics(t, func() { fp.SetImageCapabilities(nil) })
}

func TestFilePickerDialog_WorkingDirFallsBackToOSGetwdWhenWorkspaceEmpty(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	// Called twice: once by NewFilePicker's own constructor, once by
	// the direct call below exercising the same wrapper method.
	ws.EXPECT().WorkingDir().Return("").Times(2)

	fp, _ := dialog.NewFilePicker(m.com)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, fp.WorkingDir())
}

func TestFilePickerDialog_WorkingDirUsesWorkspaceDirWhenSet(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	dir := t.TempDir()
	ws.EXPECT().WorkingDir().Return(dir).Times(2)

	fp, _ := dialog.NewFilePicker(m.com)
	require.Equal(t, dir, fp.WorkingDir())
}
