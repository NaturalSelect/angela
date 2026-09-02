package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------
// YesNo (internal/ui/dialog/question_yesno.go)
// ---------------------------------------------------------------------

func newTestYesNo(t *testing.T) *YesNo {
	t.Helper()
	sty := styles.CharmtonePantera()
	return NewYesNo(&sty, question.Question{ID: "q1", Text: "Proceed?"})
}

func TestYesNo_DefaultSelectionIsNoForSafety(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	resp := d.Response()
	require.NotNil(t, resp.Yes)
	require.False(t, *resp.Yes)
}

func TestYesNo_CloseKeyReturnsDoneWithNilCmd(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, done)
	require.Nil(t, cmd)
}

func TestYesNo_LeftRightTogglesSelection(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	require.False(t, *d.Response().Yes) // default: No

	done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.False(t, done)
	require.Nil(t, cmd)
	require.True(t, *d.Response().Yes, "left/right must toggle, not just move")

	d.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.False(t, *d.Response().Yes, "second toggle flips back to No")
}

func TestYesNo_EnterConfirmsCurrentToggleState(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle to Yes

	done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, done)
	require.Nil(t, cmd)
	require.True(t, *d.Response().Yes)
}

// TestYesNo_YShortcutBug_ResponseContradictsThePressedKey documents a
// real bug: the Y/N keyboard shortcuts call answer(), which only writes
// to the lastResponse field. Response() never reads lastResponse - it
// always recomputes live from the left/right toggle (selectedNo). So
// pressing "y" while the toggle is still on its default "No" position
// returns done=true (as if an answer were recorded) but Response()
// reports Yes=false, the opposite of the key the user pressed.
func TestYesNo_YShortcutBug_ResponseContradictsThePressedKey(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.True(t, done)

	resp := d.Response()
	require.NotNil(t, resp.Yes)
	require.False(t, *resp.Yes, "current (buggy) behavior: pressing Y is silently overridden by the untouched toggle state")
}

// TestYesNo_NShortcutBug_ResponseContradictsThePressedKeyAfterToggle is
// the symmetric case: once the toggle has been moved to Yes, pressing
// "n" is likewise overridden by the (now Yes) toggle state instead of
// recording No.
func TestYesNo_NShortcutBug_ResponseContradictsThePressedKeyAfterToggle(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle to Yes

	done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.True(t, done)

	resp := d.Response()
	require.NotNil(t, resp.Yes)
	require.True(t, *resp.Yes, "current (buggy) behavior: pressing N is silently overridden by the toggled Yes state")
}

func TestYesNo_NoteKeyOpensNoteEditor(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.False(t, done)
	require.True(t, d.noteEditor.Focused(), "openNote must focus the note editor synchronously")
}

func TestYesNo_NoteEditorFocused_LetterKeysGoToNoteNotShortcuts(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.True(t, d.noteEditor.Focused())

	done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.False(t, done, "note editor must swallow the key instead of submitting")
	require.Equal(t, "y", d.noteEditor.Value(), "the key must be inserted as note text, not treated as the Yes shortcut")
}

func TestYesNo_KeyPressesNeverPanic(t *testing.T) {
	t.Parallel()

	keys := []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: tea.KeyLeft},
		{Code: tea.KeyRight},
		{Code: tea.KeyEnter},
		{Code: 'y', Text: "y"},
		{Code: 'Y', Text: "Y"},
		{Code: 'n', Text: "n"},
		{Code: 'N', Text: "N"},
		{Code: 'n', Mod: tea.ModAlt},
		{Code: tea.KeyTab},
		{Code: tea.KeyBackspace},
		{Code: '日', Text: "日"},
	}
	for i, k := range keys {
		t.Run(k.String(), func(t *testing.T) {
			t.Parallel()
			_ = i
			d := newTestYesNo(t)
			require.NotPanics(t, func() {
				d.HandleKey(k)
			})
		})
	}
}

// ---------------------------------------------------------------------
// QuestionForm (internal/ui/dialog/question_form.go)
// ---------------------------------------------------------------------

func singleYesNoRequest() question.Request {
	return question.Request{
		ID: "batch-1",
		Questions: []question.Question{
			{ID: "q1", Type: question.TypeYesNo, Text: "Proceed?"},
		},
	}
}

func twoQuestionRequest() question.Request {
	return question.Request{
		ID: "batch-2",
		Questions: []question.Question{
			{ID: "q1", Type: question.TypeYesNo, Text: "First?"},
			{ID: "q2", Type: question.TypeFreeText, Text: "Second?"},
		},
	}
}

func newTestQuestionForm(t *testing.T, batch question.Request) *QuestionForm {
	t.Helper()
	sty := styles.CharmtonePantera()
	f := NewQuestionForm(&sty, batch)
	// openBatchFormDialog always calls SetFocused(true) right after
	// installing the form; without it the active question's own
	// SetFocused(false) (from switchTab's default f.focused=false)
	// blurs its editor, silently dropping typed keys.
	f.SetFocused(true)
	return f
}

func TestQuestionForm_SingleQuestion_NoConfirmTab_EnterSubmitsDirectly(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, singleYesNoRequest())
	require.False(t, f.hasConfirm, "single-question batches must not get a confirm tab")

	var got []question.Answer
	f.OnAnswer = func(responses []question.Answer) { got = responses }

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle to Yes
	done, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, done)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Yes)
	require.True(t, *got[0].Yes)
}

func TestQuestionForm_MultiQuestion_LastQuestionAdvancesToConfirmInsteadOfSubmitting(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())
	require.True(t, f.hasConfirm)

	submitted := false
	f.OnAnswer = func([]question.Answer) { submitted = true }

	done, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // q1 default (No), auto-advance
	require.False(t, done)
	require.False(t, submitted)
	require.Equal(t, 1, f.activeIdx)

	f.HandleKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	done, _ = f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // q2 (last real question)
	require.False(t, done)
	require.False(t, submitted, "must not submit until the Confirm tab is itself confirmed")
	require.True(t, f.isConfirmTab())
}

func TestQuestionForm_ConfirmTabDefaultYes_EnterSubmitsAllAnswers(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())

	var got []question.Answer
	f.OnAnswer = func(responses []question.Answer) { got = responses }

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // q1: No (default), advance
	f.HandleKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // q2: "h", advance to confirm
	require.True(t, f.isConfirmTab())

	done, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // confirm defaults to Yes
	require.True(t, done)
	require.Len(t, got, 2)
	require.NotNil(t, got[0].Yes)
	require.False(t, *got[0].Yes)
	require.Equal(t, "h", got[1].FillInText)
}

func TestQuestionForm_ConfirmTabRejectedWithNothingTyped_SkipsUntouchedYesNo(t *testing.T) {
	t.Parallel()

	// twoQuestionRequest is [YesNo, FreeText]. YesNo.Response() always
	// returns a non-nil Yes pointer (default "No" for safety), so
	// switchTab's snapshot-on-leave makes q1 look "answered" the moment
	// focus passes over it, even though the user never pressed a key on
	// it. firstUnanswered() can only find q2 (FreeText), whose empty
	// Response() genuinely reports no selection.
	f := newTestQuestionForm(t, twoQuestionRequest())

	// Jump straight to the Confirm tab via tab navigation, bypassing
	// every question - tab switching works independently of whether the
	// current question has been answered.
	f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	require.True(t, f.isConfirmTab())

	f.HandleKey(tea.KeyPressMsg{Code: 'n', Text: "n"}) // confirmYes = false
	done, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, done)
	require.False(t, f.isConfirmTab())
	require.Equal(t, 1, f.activeIdx, "q1's untouched YesNo default counts as \"answered\", so q2 (FreeText) is the first real gap")
}

// TestQuestionForm_ConfirmTabRejectedAllYesNo_FirstUnansweredNeverTriggers
// documents the extreme case of the same quirk: when every question is a
// YesNo, none of them can ever produce a nil/empty Response(), so
// firstUnanswered() always returns -1 and OnReject's fallback
// (switchTab(numQuestions-1)) fires instead - landing on the LAST
// question, not the first, even though nothing was ever answered.
func TestQuestionForm_ConfirmTabRejectedAllYesNo_FirstUnansweredNeverTriggers(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, question.Request{
		ID: "batch-all-yesno",
		Questions: []question.Question{
			{ID: "q1", Type: question.TypeYesNo, Text: "First?"},
			{ID: "q2", Type: question.TypeYesNo, Text: "Second?"},
		},
	})

	f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	require.True(t, f.isConfirmTab())

	f.HandleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	done, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, done)
	require.False(t, f.isConfirmTab())
	require.Equal(t, 1, f.activeIdx, "with no genuinely-empty question to find, reject falls back to the LAST question, not the first")
}

func TestQuestionForm_CloseKeyCancelsAndDoesNotSubmit(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())

	canceled := false
	submitted := false
	f.OnCancel = func() { canceled = true }
	f.OnAnswer = func([]question.Answer) { submitted = true }

	done, _ := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, done)
	require.True(t, canceled)
	require.False(t, submitted)
}

func TestQuestionForm_TabSwitchWrapsAroundBothDirections(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())
	// 3 tabs total: q1(0), q2(1), confirm(2).

	f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["}) // prev from 0 wraps to last (confirm)
	require.True(t, f.isConfirmTab())

	f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"}) // next from confirm wraps to first
	require.Equal(t, 0, f.activeIdx)
	require.False(t, f.isConfirmTab())
}

func TestQuestionForm_TabSwitchSnapshotsAnswerBeforeLeaving(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft})    // toggle q1 to Yes
	f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"}) // leave q1 without pressing Enter

	require.NotNil(t, f.answers[0], "switching tabs must snapshot the outgoing question's live answer")
	require.NotNil(t, f.answers[0].Yes)
	require.True(t, *f.answers[0].Yes)
}

func TestQuestionForm_EmptyQuestionsSlice_HandleKeyNeverPanics(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, question.Request{ID: "empty"})
	require.False(t, f.hasConfirm)

	require.NotPanics(t, func() {
		f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		f.HandleKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	})
}
