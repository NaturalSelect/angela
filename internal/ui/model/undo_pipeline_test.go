package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
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

// TestUndoResultRestoresPoppedAttachmentsAheadOfDraft checks that the files
// and images of the undone message return to the editor, ahead of any
// attachment already on the draft.
func TestUndoResultRestoresPoppedAttachmentsAheadOfDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	m.attachments.Update(message.Attachment{FileName: "draft.txt"})

	m.Update(undoResultMsg{sessionID: "s1", result: undo.Result{
		PoppedText: "restore me",
		PoppedAttachments: []message.Attachment{
			{FileName: "a.png", MimeType: "image/png"},
			{FileName: "b.txt", MimeType: "text/plain"},
		},
	}})

	var names []string
	for _, a := range m.attachments.List() {
		names = append(names, a.FileName)
	}
	require.Equal(t, []string{"a.png", "b.txt", "draft.txt"}, names)
}

// TestUndoResultForAnotherSessionIsHeldNotApplied checks that an undo
// finishing after the user switched sessions leaves the visible draft
// alone and hands the popped text and files back once the undone session
// is shown again.
func TestUndoResultForAnotherSessionIsHeldNotApplied(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	m.textarea.SetValue("draft in s1")
	m.attachments.Update(message.Attachment{FileName: "draft.txt"})

	m.Update(undoResultMsg{sessionID: "s2", result: undo.Result{
		PoppedText:        "from s2",
		PoppedAttachments: []message.Attachment{{FileName: "s2.png", MimeType: "image/png"}},
	}})

	require.Equal(t, "draft in s1", m.textarea.Value(), "the visible draft must not take another session's text")
	require.Len(t, m.attachments.List(), 1, "the visible draft must not take another session's files")
	require.Len(t, m.stashedPrompts["s2"], 1)

	m.restoreStashedPrompts("s2")
	require.Equal(t, "from s2\n\ndraft in s1", m.textarea.Value())
	var names []string
	for _, a := range m.attachments.List() {
		names = append(names, a.FileName)
	}
	require.Equal(t, []string{"s2.png", "draft.txt"}, names)
}

// TestRestoredPromptWithOnlyFilesLeavesTheDraftTextAlone checks that
// restoring a prompt that had files but no text does not push the draft
// down by a blank line.
func TestRestoredPromptWithOnlyFilesLeavesTheDraftTextAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	m.textarea.SetValue("draft")
	m.stashRestoredPrompts("s1", []message.QueuedPrompt{{
		Attachments: []message.Attachment{{FileName: "only.png", MimeType: "image/png"}},
	}})

	m.restoreStashedPrompts("s1")

	require.Equal(t, "draft", m.textarea.Value())
	require.Len(t, m.attachments.List(), 1)
}
