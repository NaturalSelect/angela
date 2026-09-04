package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestQuestionEditor_OpenNotePrefillsExistingText verifies that
// reopening a note on an item that already has a saved note
// pre-populates the editor instead of starting blank.
func TestQuestionEditor_OpenNotePrefillsExistingText(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.notes["a"] = "existing note"
	d.cursorIdx = 0

	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.True(t, d.noteEditor.Focused())
	require.Equal(t, "existing note", d.noteEditor.Value())
}

// TestQuestionEditor_CloseNoteDeletesWhenClearedToEmpty verifies that
// clearing a previously saved note and closing the editor removes
// the entry rather than leaving an empty string behind.
func TestQuestionEditor_CloseNoteDeletesWhenClearedToEmpty(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.notes["a"] = "existing note"
	d.cursorIdx = 0

	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.Equal(t, "existing note", d.noteEditor.Value())

	d.noteEditor.Reset()
	d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	_, ok := d.notes["a"]
	require.False(t, ok, "clearing a note to empty must delete it, not save an empty string")
}

// TestQuestionEditor_HandleNoteKeyLiteralEnterClosesAndSaves verifies
// that pressing a literal Enter while a note editor is focused saves
// and closes it, mirroring the close-key path but via a different
// branch (handleNoteKey's default case checks for "enter" directly).
func TestQuestionEditor_HandleNoteKeyLiteralEnterClosesAndSaves(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = 0
	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.True(t, d.noteEditor.Focused())

	d.noteEditor.SetValue("typed via note editor")
	done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, done, "closing a note must not close the whole question")
	require.False(t, d.noteEditor.Focused())
	require.Equal(t, "typed via note editor", d.notes["a"])
}

// TestQuestionEditor_DrawNoteEditingBranch verifies that drawing a
// choice list with an open note editor on the cursor's choice
// renders the live note content and reports a hardware cursor via
// noteCursor.
func TestQuestionEditor_DrawNoteEditingBranch(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = 0
	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	d.noteEditor.SetValue("line one\nline two")

	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, cur, "an open, focused note editor must report a cursor via noteCursor")

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "line one")
	require.Contains(t, view, "line two")
}

// TestQuestionEditor_DrawNoteSavedBranch verifies that a saved note
// on a non-active choice renders as dimmed text in the choice list,
// exercising drawNote's non-editing branch.
func TestQuestionEditor_DrawNoteSavedBranch(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	d.notes["b"] = "a saved reminder"
	d.cursorIdx = 0 // active choice is Alpha, not the noted Beta

	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "a saved reminder")
}

// TestQuestionEditor_DrawFillInSavedNotFocusedBranch verifies that a
// committed, blurred fill-in value renders in the choice list even
// when the cursor has moved to a different choice.
func TestQuestionEditor_DrawFillInSavedNotFocusedBranch(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	d.fillIn.SetValue("saved fill-in text")
	d.fillIn.Blur()
	d.cursorIdx = 0

	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "saved fill-in text")
}

// TestQuestionEditor_DrawStandaloneNoteMultiLine verifies the
// continuation-line branch of drawStandaloneNote (used by YesNo)
// renders every row of a multi-line note and reports a cursor tied
// to the first line.
func TestQuestionEditor_DrawStandaloneNoteMultiLine(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	d.noteEditor.SetValue("first line\nsecond line")

	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, cur)

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "first line")
	require.Contains(t, view, "second line")
}

// TestQuestionEditor_DrawStandaloneNoteSaved verifies a saved
// (non-editing) note renders under the YesNo buttons.
func TestQuestionEditor_DrawStandaloneNoteSaved(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.notes["_question"] = "a standalone saved note"

	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "a standalone saved note")
}
