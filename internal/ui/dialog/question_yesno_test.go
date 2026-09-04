package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// hitButtonPos scans a screen-sized grid for the coordinates that hit
// button idx in compositor. Shared by question component tests that
// build a button-hit compositor during Draw (YesNo, ConfirmComponent).
func hitButtonPos(t *testing.T, compositor *lipgloss.Compositor, idx, maxW, maxH int) (x, y int) {
	t.Helper()
	for y := range maxH {
		for x := range maxW {
			if common.HitButtonIndex(compositor, x, y) == idx {
				return x, y
			}
		}
	}
	t.Fatalf("button %d not found on screen", idx)
	return 0, 0
}

func TestYesNo_GetRequest(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	require.Equal(t, d.Request, d.GetRequest())
}

// TestYesNo_HeightGrowsWithDescriptionAndNotes verifies each optional
// section (description, saved note, active note editor) adds to the
// reported height, and that a non-positive width falls back to the
// last drawn width.
func TestYesNo_HeightGrowsWithDescriptionAndNotes(t *testing.T) {
	t.Parallel()

	sty := testStyles()
	base := NewYesNo(sty, question.Question{ID: "q1", Text: "Proceed?"})
	baseHeight := base.Height(60)

	withDesc := NewYesNo(sty, question.Question{ID: "q1", Text: "Proceed?", Description: "Extra context"})
	require.Greater(t, withDesc.Height(60), baseHeight, "a description must add height")

	withNote := NewYesNo(sty, question.Question{ID: "q1", Text: "Proceed?"})
	withNote.notes["_question"] = "a saved note"
	require.Greater(t, withNote.Height(60), baseHeight, "a saved note must add height")

	withActiveNote := NewYesNo(sty, question.Question{ID: "q1", Text: "Proceed?"})
	withActiveNote.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.Greater(t, withActiveNote.Height(60), baseHeight, "an open note editor must add height")

	base.lastWidth = 60
	require.Equal(t, base.Height(60), base.Height(0), "a non-positive width must fall back to lastWidth")
}

// TestYesNo_Draw verifies the question text and both buttons render,
// and that the cursor is only reported while the note editor is open.
func TestYesNo_Draw(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	const w, h = 60, 20
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))
	require.Nil(t, cur, "no cursor without an open note editor")

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Proceed?")
	require.Contains(t, view, "Yes")
	require.Contains(t, view, "No")

	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	cur = d.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, cur, "an open note editor must report a cursor")
}

// TestYesNo_DrawWithDescriptionRendersIt covers the description branch
// specifically, including the markdown fallback path.
func TestYesNo_DrawWithDescriptionRendersIt(t *testing.T) {
	t.Parallel()

	sty := testStyles()
	d := NewYesNo(sty, question.Question{ID: "q1", Text: "Proceed?", Description: "read the **docs**"})
	const w, h = 60, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "docs")
}

// TestYesNo_HandleMouseClick verifies clicking each button answers the
// question and that a click outside both buttons is ignored.
func TestYesNo_HandleMouseClick(t *testing.T) {
	t.Parallel()

	t.Run("clicking Yes answers true", func(t *testing.T) {
		t.Parallel()
		d := newTestYesNo(t)
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		d.Draw(scr, image.Rect(0, 0, w, h))

		x, y := hitButtonPos(t, d.compositor, 0, w, h)
		done, handled := d.HandleMouseClick(x, y)
		require.True(t, done)
		require.True(t, handled)
		require.False(t, d.selectedNo)
		require.True(t, *d.Response().Yes)
	})

	t.Run("clicking No answers false", func(t *testing.T) {
		t.Parallel()
		d := newTestYesNo(t)
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		d.Draw(scr, image.Rect(0, 0, w, h))

		x, y := hitButtonPos(t, d.compositor, 1, w, h)
		done, handled := d.HandleMouseClick(x, y)
		require.True(t, done)
		require.True(t, handled)
		require.True(t, d.selectedNo)
		require.False(t, *d.Response().Yes)
	})

	t.Run("a miss is not handled", func(t *testing.T) {
		t.Parallel()
		d := newTestYesNo(t)
		done, handled := d.HandleMouseClick(-1, -1)
		require.False(t, done)
		require.False(t, handled)
	})
}

func TestYesNo_SetHoverRecordsPosition(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.SetHover(5, 7)
	require.Equal(t, 5, d.hoverX)
	require.Equal(t, 7, d.hoverY)
}

// TestYesNo_HandlePaste verifies paste is dropped when no note editor
// is active and forwarded to it once one is open.
func TestYesNo_HandlePaste(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	require.Nil(t, d.HandlePaste(tea.PasteMsg{Content: "ignored"}))

	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	d.HandlePaste(tea.PasteMsg{Content: "pasted note"})
	require.Equal(t, "pasted note", d.noteEditor.Value())
}

// TestYesNo_ShortHelp verifies the status bar bindings switch to a
// save/close-only set while the note editor is focused, and show the
// full set of shortcuts otherwise.
func TestYesNo_ShortHelp(t *testing.T) {
	t.Parallel()

	t.Run("default shows all shortcuts", func(t *testing.T) {
		t.Parallel()
		d := newTestYesNo(t)
		require.Len(t, d.ShortHelp(), 5)
	})

	t.Run("note editor open shows save/close only", func(t *testing.T) {
		t.Parallel()
		d := newTestYesNo(t)
		d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
		help := d.ShortHelp()
		require.Len(t, help, 2)
		require.Equal(t, d.keyClose, help[0])
	})
}

// TestYesNo_NoteSurvivesIntoResponse verifies that closing the note
// editor persists the text, and it is carried on subsequent answers.
func TestYesNo_NoteSurvivesIntoResponse(t *testing.T) {
	t.Parallel()

	d := newTestYesNo(t)
	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	d.noteEditor.SetValue("please double check")
	// Closing the note (any non-nav, non-typing key handled by
	// handleNoteKey's close case) commits it to notes.
	d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, d.noteEditor.Focused())

	resp := d.respond(true)
	require.Equal(t, "please double check", resp.Notes["_question"])
}
