package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// questionFormTestUI builds a fully laid-out UI (width/height set, a real
// textarea, an overlay) wired to a mock workspace. Both openBatchFormDialog
// and handleQuestionNotification are only reachable through a UI that has
// already been through updateLayoutAndSize once (openBatchFormDialog calls
// it again internally), which newDialogUI's zero-size UI does not provide.
func questionFormTestUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()

	u := newTestUI()
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	u.com.Workspace = ws
	u.dialog = dialog.NewOverlay()
	u.updateLayoutAndSize()
	return u, ws
}

func freeTextRequest(batchID string) question.Request {
	return question.Request{
		ID: batchID,
		Questions: []question.Question{{
			ID:   "q1",
			Type: question.TypeFreeText,
			Text: "What is your favorite color?",
		}},
	}
}

// ---------------------------------------------------------------------
// openBatchFormDialog / handleQuestionNotification (internal/ui/model/ui.go)
// ---------------------------------------------------------------------

func TestOpenBatchFormDialog_InstallsFormAndBlursTextarea(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)
	require.True(t, u.textarea.Focused())

	u.openBatchFormDialog(freeTextRequest("batch-1"))

	form, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok, "expected activeInline to be a *dialog.QuestionForm, got %T", u.activeInline)
	require.Equal(t, "batch-1", form.BatchID)
	require.False(t, u.textarea.Focused())
	require.Equal(t, uiFocusEditor, u.focus)
}

func TestOpenBatchFormDialog_ReplacesExistingFormWithoutStacking(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)

	u.openBatchFormDialog(freeTextRequest("batch-a"))
	first, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)

	u.openBatchFormDialog(freeTextRequest("batch-b"))
	second, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)

	require.Equal(t, "batch-b", second.BatchID)
	require.NotSame(t, first, second, "opening a new batch must install a fresh form, not mutate the old one")
}

func TestOpenBatchFormDialog_EmptyQuestionsSliceDoesNotPanic(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)
	require.NotPanics(t, func() {
		u.openBatchFormDialog(question.Request{ID: "empty-batch"})
	})
	form, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)
	require.Equal(t, "empty-batch", form.BatchID)
}

func TestOpenBatchFormDialog_OnAnswerReachesWorkspace(t *testing.T) {
	t.Parallel()

	u, ws := questionFormTestUI(t)
	var got []question.Answer
	ws.EXPECT().QuestionAnswer(gomock.Any()).DoAndReturn(func(responses []question.Answer) bool {
		got = responses
		return true
	})

	u.openBatchFormDialog(freeTextRequest("batch-1"))
	form, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)
	require.NotNil(t, form.OnAnswer, "openBatchFormDialog must wire OnAnswer")

	want := []question.Answer{{QuestionID: "q1", FillInText: "blue"}}
	form.OnAnswer(want)
	require.Equal(t, want, got)
}

func TestOpenBatchFormDialog_OnCancelReachesWorkspace(t *testing.T) {
	t.Parallel()

	u, ws := questionFormTestUI(t)
	ws.EXPECT().QuestionCancel().Return(true)

	u.openBatchFormDialog(freeTextRequest("batch-1"))
	form, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)
	require.NotNil(t, form.OnCancel, "openBatchFormDialog must wire OnCancel")

	form.OnCancel()
}

func TestHandleQuestionNotification_DismissesActiveFormAndRefocusesTextarea(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)
	u.openBatchFormDialog(freeTextRequest("batch-1"))
	require.NotNil(t, u.activeInline)
	require.False(t, u.textarea.Focused())

	u.handleQuestionNotification(question.Notification{BatchID: "batch-1"})

	require.Nil(t, u.activeInline)
	require.True(t, u.textarea.Focused())
}

func TestHandleQuestionNotification_MismatchedBatchIDStillDismisses(t *testing.T) {
	t.Parallel()

	// handleQuestionNotification's doc comment states any notification
	// dismisses the active form regardless of BatchID, since only one
	// question can be pending at a time. Attack that claim directly
	// with a notification carrying an unrelated batch ID.
	u, _ := questionFormTestUI(t)
	u.openBatchFormDialog(freeTextRequest("batch-1"))

	u.handleQuestionNotification(question.Notification{BatchID: "some-other-batch"})

	require.Nil(t, u.activeInline)
	require.True(t, u.textarea.Focused())
}

func TestHandleQuestionNotification_NoActiveFormIsNoOp(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)
	require.NotPanics(t, func() {
		u.handleQuestionNotification(question.Notification{BatchID: "whatever"})
	})
	require.Nil(t, u.activeInline)
}

// ---------------------------------------------------------------------
// Mouse clicks on a collapsed question form (internal/ui/model/ui.go)
// ---------------------------------------------------------------------

func TestUpdate_MouseClick_CollapsedQuestionFormRefocusesOnClick(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)
	u.openBatchFormDialog(freeTextRequest("batch-1"))

	// Simulate having Tab'd away to the chat: the form loses editor
	// focus, and shrinking the height clears the 2/5 collapse threshold
	// for any non-trivial form, forcing the one-line summary render.
	u.focus = uiFocusMain
	u.height = 10
	u.updateLayoutAndSize()
	form, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)
	require.True(t, u.shouldCollapseQuestion(form),
		"test setup must actually force the collapsed render path")
	require.Positive(t, u.layout.editor.Dx())
	require.Positive(t, u.layout.editor.Dy())

	u.Update(tea.MouseClickMsg{X: u.layout.editor.Min.X, Y: u.layout.editor.Min.Y})

	// Before the fix, every click was routed to the form's
	// HandleMouseClick first, which hit-tests against layout coordinates
	// from the last full (expanded) Draw. While collapsed that layout is
	// stale, so a click here must fall through handleClickFocus instead
	// of being swallowed or misrouted by the stale hit-test.
	require.Equal(t, uiFocusEditor, u.focus,
		"clicking the collapsed summary must re-expand and focus the form")
	require.Same(t, form, u.activeInline,
		"the same form must still be active, not cleared or replaced")
}

func TestUpdate_MouseClick_CollapsedQuestionFormOutsideEditorReachesChat(t *testing.T) {
	t.Parallel()

	u, _ := questionFormTestUI(t)
	u.openBatchFormDialog(freeTextRequest("batch-1"))
	u.focus = uiFocusMain
	u.height = 10
	u.updateLayoutAndSize()
	form, ok := u.activeInline.(*dialog.QuestionForm)
	require.True(t, ok)
	require.True(t, u.shouldCollapseQuestion(form))
	require.Positive(t, u.layout.main.Dx())
	require.Positive(t, u.layout.main.Dy())

	// A click above the editor band, in the chat pane, must not be
	// captured by the collapsed form's stale hit-test: it stays routed
	// to the chat pane (e.g. so re-entering a parked branch by clicking
	// its Agent tool card keeps working).
	require.NotPanics(t, func() {
		u.Update(tea.MouseClickMsg{X: u.layout.main.Min.X, Y: u.layout.main.Min.Y})
	})
	require.Equal(t, uiFocusMain, u.focus)
	require.Same(t, form, u.activeInline,
		"a click outside the editor must not disturb the parked form")
}
