package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func newTestSingleChoice(t *testing.T) *SingleChoice {
	t.Helper()
	s := styles.CharmtonePantera()
	req := question.Question{
		ID:   "q1",
		Type: question.TypeSingleChoice,
		Text: "Pick one",
		Choices: []question.Choice{
			{ID: "a", Label: "Alpha"},
			{ID: "b", Label: "Beta"},
			{ID: "c", Label: "Gamma"},
		},
	}
	return NewSingleChoice(&s, req)
}

func newTestMultiChoice(t *testing.T) *MultiChoice {
	t.Helper()
	s := styles.CharmtonePantera()
	req := question.Question{
		ID:   "q1",
		Type: question.TypeMultiChoice,
		Text: "Pick some",
		Choices: []question.Choice{
			{ID: "a", Label: "Alpha"},
			{ID: "b", Label: "Beta"},
			{ID: "c", Label: "Gamma"},
		},
	}
	return NewMultiChoice(&s, req)
}

// TestMoveDownFromHoverIsSymmetric verifies that the first arrow
// press after hovering moves one step in the arrow's direction
// relative to the hovered item: down lands below it, up lands above
// it. Previously moveDown landed on the hovered item itself, making
// the first down-press a dead keystroke.
func TestMoveDownFromHoverIsSymmetric(t *testing.T) {
	t.Parallel()

	t.Run("down moves below hovered", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.mouseActive = true
		d.hoveredChoice = 1 // hovering Beta
		d.moveDown()
		require.Equal(t, 2, d.cursorIdx, "down from hovered index 1 should land on 2")
		require.False(t, d.mouseActive, "keyboard nav must exit hover mode")
	})

	t.Run("up moves above hovered", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.mouseActive = true
		d.hoveredChoice = 1 // hovering Beta
		d.moveUp()
		require.Equal(t, 0, d.cursorIdx, "up from hovered index 1 should land on 0")
		require.False(t, d.mouseActive, "keyboard nav must exit hover mode")
	})

	t.Run("down with no hovered choice lands on first", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.mouseActive = true
		d.hoveredChoice = -1
		d.moveDown()
		require.Equal(t, 0, d.cursorIdx)
	})
}

// TestHoverStaysLiveWhileFillInFocused verifies that hover feedback
// keeps tracking the mouse even while the fill-in textarea is
// focused, so the user can still see what they're pointing at.
func TestHoverStaysLiveWhileFillInFocused(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	// Navigate to the fill-in item and focus it, mimicking the
	// keyboard-driven path into the fill-in field.
	d.cursorIdx = len(d.Request.Choices) // fill-in index
	require.True(t, d.isFillIn())
	d.fillIn.Focus()
	// Draw once so the hit-test compositor for the choice rows is
	// built; setHover resolves the hovered choice through it.
	scr := uv.NewScreenBuffer(40, 20)
	d.Draw(scr, image.Rect(0, 0, 40, 20))

	d.setHover(2, 6) // somewhere over a choice row

	require.True(t, d.mouseActive, "hover should stay live while editing the fill-in")
	require.GreaterOrEqual(t, d.hoveredChoice, 0, "the hovered choice should be resolved")
}

// TestFillInArrowIgnoresHover verifies that arrow keys pressed while
// the fill-in is focused move relative to the fill-in, not to a
// choice the mouse happens to be hovering over.
func TestFillInArrowIgnoresHover(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = len(d.Request.Choices) // on the fill-in
	d.fillIn.Focus()
	d.mouseActive = true
	d.hoveredChoice = 0 // mouse is hovering the first choice

	// Up from the fill-in should land on the last real choice, not
	// adopt the hovered index 0.
	d.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})

	require.Equal(t, len(d.Request.Choices)-1, d.cursorIdx,
		"arrow up from the fill-in should move to the last choice")
	require.False(t, d.mouseActive)
}

// TestHoverActiveWhenFillInBlurred verifies hover still works once
// the cursor is on the fill-in but the textarea is not focused
// (e.g. after pressing escape), so leaving the editor restores
// normal mouse behavior.
func TestHoverActiveWhenFillInBlurred(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = 0
	d.fillIn.Blur()

	d.setHover(3, 3)

	require.True(t, d.mouseActive, "hover should activate when no textarea is focused")
}

// enterKey is the key press used to trigger selection in tests.
var (
	enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}
	spaceKey = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
)

// TestSingleChoiceEnterAdoptsHover verifies that pressing Enter while
// hovering a choice selects the hovered item, not the stale cursor.
func TestSingleChoiceEnterAdoptsHover(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = 0 // stale keyboard cursor on Alpha
	d.mouseActive = true
	d.hoveredChoice = 2 // hovering Gamma

	done, _ := d.HandleKey(enterKey)

	require.True(t, done, "Enter should submit the selection")
	require.Equal(t, []string{"c"}, d.Response().SelectedIDs,
		"Enter while hovering should select the hovered choice")
}

// TestMultiChoiceToggleAdoptsHover verifies that pressing space while
// hovering a choice toggles the hovered item, not the stale cursor.
func TestMultiChoiceToggleAdoptsHover(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	d.cursorIdx = 0 // stale keyboard cursor on Alpha
	d.mouseActive = true
	d.hoveredChoice = 1 // hovering Beta

	d.HandleKey(spaceKey)

	require.Equal(t, []string{"b"}, d.Response().SelectedIDs,
		"space while hovering should toggle the hovered choice")
	require.False(t, d.mouseActive, "toggling must exit hover mode")
}

// placeholderItem is a choiceItemRenderer stub for buildLines tests
// where only layout (not label content) matters.
func placeholderItem(int, question.Choice, bool, int) string { return "x" }

// fillInRowText builds the choice list and returns the first fill-in
// row's rendered text.
func fillInRowText(t *testing.T, d *SingleChoice, prefix string) string {
	t.Helper()
	lines := d.buildLines(40, prefix, placeholderItem)
	require.GreaterOrEqual(t, d.fillInTop, 0)
	require.Less(t, d.fillInTop, len(lines))
	return lines[d.fillInTop].text
}

// TestFillInPrefixIsThreadedThrough verifies the prompt string a
// component supplies actually reaches the fill-in row. Previously
// buildLines ignored it and recomputed its own, so single-select's
// pink prompt never rendered.
func TestFillInPrefixIsThreadedThrough(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = len(d.Request.Choices) // on the fill-in
	got := fillInRowText(t, d, "MARKER> ")
	require.Contains(t, got, "MARKER>", "buildLines must use the supplied fill-in prompt")
}

// TestFillInBarFollowsSelection verifies the fill-in gutter bar
// tracks the active/hover state like a choice row, rather than
// staying lit whenever the fill-in holds text.
func TestFillInBarFollowsSelection(t *testing.T) {
	t.Parallel()

	const bar = "┃"
	fillInIdx := 3 // len(choices)

	t.Run("lit when selected via keyboard", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = fillInIdx
		d.mouseActive = false
		require.Contains(t, fillInRowText(t, d, "> "), bar)
	})

	t.Run("dark when it holds text but is not selected", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.fillIn.SetValue("custom answer")
		d.cursorIdx = 0 // a choice, not the fill-in
		d.mouseActive = false
		require.NotContains(t, fillInRowText(t, d, "> "), bar,
			"the bar should not stay lit just because the fill-in has text")
	})

	t.Run("dark when selected but hovering elsewhere", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = fillInIdx
		d.mouseActive = true
		d.hoveredChoice = 0 // mouse over the first choice
		require.NotContains(t, fillInRowText(t, d, "> "), bar,
			"in hover mode the bar belongs to the hovered choice")
	})
}

// TestChoiceList_FillInCursorRelativeToArea verifies the fill-in
// cursor returned by Draw is relative to the drawing area, not
// polluted by the area's absolute screen offset. Regression test:
// fillInCursor used to add the area's absolute Min.X into the
// cursor it returns, and the caller (UI.Cursor) added Min.X again
// on top, so the terminal cursor drifted far to the right of the
// typed text whenever the dialog wasn't flush against the screen's
// left edge.
func TestChoiceList_FillInCursorRelativeToArea(t *testing.T) {
	t.Parallel()

	typeText := func(d *SingleChoice, text string) {
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		for _, r := range text {
			d.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}

	const w, h = 45, 14
	origin := newTestSingleChoice(t)
	typeText(origin, "看起来")
	scrOrigin := uv.NewScreenBuffer(w, h)
	curOrigin := origin.Draw(scrOrigin, image.Rect(0, 0, w, h))
	require.NotNil(t, curOrigin)

	const offsetX, offsetY = 20, 3
	offset := newTestSingleChoice(t)
	typeText(offset, "看起来")
	scrOffset := uv.NewScreenBuffer(offsetX+w, offsetY+h)
	curOffset := offset.Draw(scrOffset, image.Rect(offsetX, offsetY, offsetX+w, offsetY+h))
	require.NotNil(t, curOffset)

	require.Equal(t, curOrigin.X, curOffset.X,
		"the cursor must be relative to the area regardless of the area's absolute screen position")
	require.Less(t, curOffset.X, w, "cursor must stay within the area, not include its absolute offset")
}

// TestChoiceList_NoteCursorRelativeToArea is the note-editor
// counterpart of TestChoiceList_FillInCursorRelativeToArea:
// noteCursor had the identical absolute-offset bug.
func TestChoiceList_NoteCursorRelativeToArea(t *testing.T) {
	t.Parallel()

	openNoteAndType := func(d *SingleChoice, text string) {
		d.cursorIdx = 0
		d.openNote(d.noteKey())
		for _, r := range text {
			d.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}

	const w, h = 45, 14
	origin := newTestSingleChoice(t)
	openNoteAndType(origin, "看起来")
	scrOrigin := uv.NewScreenBuffer(w, h)
	curOrigin := origin.Draw(scrOrigin, image.Rect(0, 0, w, h))
	require.NotNil(t, curOrigin)

	const offsetX, offsetY = 20, 3
	offset := newTestSingleChoice(t)
	openNoteAndType(offset, "看起来")
	scrOffset := uv.NewScreenBuffer(offsetX+w, offsetY+h)
	curOffset := offset.Draw(scrOffset, image.Rect(offsetX, offsetY, offsetX+w, offsetY+h))
	require.NotNil(t, curOffset)

	require.Equal(t, curOrigin.X, curOffset.X,
		"the note cursor must be relative to the area regardless of the area's absolute screen position")
	require.Less(t, curOffset.X, w, "cursor must stay within the area, not include its absolute offset")
}
