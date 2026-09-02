package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/undo"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestUndoPaletteRejectedWhenSessionBusy pins the same busy guard
// ActionSummarize uses: selecting "Undo Last Turn" while the session's
// agent is busy must warn and never reach PreviewUndo. The mock has no
// expectation for PreviewUndo, so a call that slipped past the guard
// would fail the test on its own.
func TestUndoPaletteRejectedWhenSessionBusy(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(true)

	m := newCompactPaletteUI(t, ws)

	for _, r := range "undo" {
		drain(m.handleDialogMsg(charKeyMsg(r)))
	}
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "selecting undo by keyboard produced no command")

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestUndoPaletteKeyboardOpensConfirmationDialog drives the real
// Commands dialog by keyboard to select "Undo Last Turn", lets the
// resulting fetch run against a mocked PreviewUndo, and delivers its
// result back into Update exactly as the Bubble Tea runtime would. The
// undo confirmation dialog must end up on top of the stack.
func TestUndoPaletteKeyboardOpensConfirmationDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(false)
	preview := undo.Preview{
		CutMessageID: "cut-1",
		PoppedText:   "hello",
		MessageCount: 2,
		Revert:       []string{"main.go"},
	}
	ws.EXPECT().PreviewUndo(gomock.Any(), "current").Return(preview, nil)

	m := newCompactPaletteUI(t, ws)

	for _, r := range "undo" {
		drain(m.handleDialogMsg(charKeyMsg(r)))
	}
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "selecting undo by keyboard produced no command")

	m.Update(cmd())

	require.Equal(t, dialog.UndoID, m.dialog.DialogLast().ID())
}

// TestUndoPaletteNothingToUndoReportsInfo covers the ErrNothingToUndo
// path: the preview fetch fails with that sentinel and the dialog must
// never open.
func TestUndoPaletteNothingToUndoReportsInfo(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(false)
	ws.EXPECT().PreviewUndo(gomock.Any(), "current").Return(undo.Preview{}, undo.ErrNothingToUndo)

	m := newCompactPaletteUI(t, ws)

	for _, r := range "undo" {
		drain(m.handleDialogMsg(charKeyMsg(r)))
	}
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "selecting undo by keyboard produced no command")

	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)
	require.False(t, m.dialog.ContainsDialog(dialog.UndoID))
}

// TestUndoConfirmDialogRestoresPoppedTextAheadOfDraft drives the undo
// confirmation dialog by keyboard: tab off the default "Cancel" button
// onto "Undo" and confirm. The mocked Undo call's PoppedText must land
// in the editor ahead of whatever the user had already typed, exactly
// like a queued prompt restored by popQueuedPromptsToEditor.
func TestUndoConfirmDialogRestoresPoppedTextAheadOfDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	preview := undo.Preview{
		CutMessageID: "cut-1",
		PoppedText:   "restore me",
		MessageCount: 1,
		Revert:       []string{"main.go"},
	}

	m := newBusyUIWithWorkspace(ws)
	m.dialog.OpenDialog(dialog.NewUndo(m.com, m.session.ID, preview))
	m.textarea.SetValue("half-typed draft")

	ws.EXPECT().AgentIsSessionBusy("s1").Return(false)
	ws.EXPECT().Undo(gomock.Any(), "s1", "cut-1").Return(undo.Result{
		PoppedText: "restore me",
		Reverted:   []string{"main.go"},
	}, nil)

	drain(m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyTab}))
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "confirming undo produced no command")

	m.Update(cmd())

	require.Equal(t, "restore me\n\nhalf-typed draft", m.textarea.Value())
}
