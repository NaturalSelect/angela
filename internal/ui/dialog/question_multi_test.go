package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestMultiChoice_GetRequest(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	require.Equal(t, d.Request, d.GetRequest())
}

func TestMultiChoice_Respond(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	d.selected[0] = true
	d.selected[2] = true
	d.fillIn.SetValue("  extra  ")
	d.notes["a"] = "note a"

	resp := d.respond()
	require.Equal(t, "q1", resp.QuestionID)
	require.Equal(t, []string{"a", "c"}, resp.SelectedIDs)
	require.Equal(t, "extra", resp.FillInText)
	require.Equal(t, map[string]string{"a": "note a"}, resp.Notes)
}

func TestMultiChoice_ShortHelp(t *testing.T) {
	t.Parallel()

	t.Run("default shows navigation and toggle keys", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		require.Len(t, d.ShortHelp(), 7)
	})

	t.Run("note editor open shows save/close only", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
		require.Len(t, d.ShortHelp(), 2)
	})

	t.Run("fill-in focused shows the trimmed set", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		require.Len(t, d.ShortHelp(), 3)
	})
}

func TestMultiChoice_HeightHeightChangedSetFocusedSetHover(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	require.Positive(t, d.Height(40))
	require.False(t, d.HeightChanged())

	d.SetFocused(true)
	require.True(t, d.focused)

	d.SetHover(3, 3)
	require.True(t, d.mouseActive)
	require.Equal(t, -1, d.hoveredChoice)
}

func TestMultiChoice_HandlePaste(t *testing.T) {
	t.Parallel()

	t.Run("dropped when the fill-in is not focused", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		require.Nil(t, d.HandlePaste(tea.PasteMsg{Content: "x"}))
	})

	t.Run("forwarded to the fill-in when focused", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		d.HandlePaste(tea.PasteMsg{Content: "pasted"})
		require.Equal(t, "pasted", d.fillIn.Value())
	})
}

func TestMultiChoice_HandleMouseClick(t *testing.T) {
	t.Parallel()

	t.Run("miss before any draw is unhandled", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		done, handled := d.HandleMouseClick(0, 0)
		require.False(t, done)
		require.False(t, handled)
	})

	t.Run("clicking a choice toggles it on then off", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		const w, h = 40, 20
		scr := uv.NewScreenBuffer(w, h)
		d.Draw(scr, image.Rect(0, 0, w, h))

		x, y := choiceHitPos(t, d.choiceCompositor, 1, w, h) // Beta
		done, handled := d.HandleMouseClick(x, y)
		require.False(t, done, "multi choice never auto-submits on click")
		require.True(t, handled)
		require.Equal(t, []string{"b"}, d.Response().SelectedIDs)

		done, handled = d.HandleMouseClick(x, y)
		require.False(t, done)
		require.True(t, handled)
		require.Empty(t, d.Response().SelectedIDs, "clicking again toggles the choice back off")
	})

	t.Run("clicking the fill-in row focuses it without submitting", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		const w, h = 40, 20
		scr := uv.NewScreenBuffer(w, h)
		d.Draw(scr, image.Rect(0, 0, w, h))

		x, y := choiceHitPos(t, d.choiceCompositor, len(d.Request.Choices), w, h)
		done, handled := d.HandleMouseClick(x, y)
		require.False(t, done)
		require.True(t, handled)
		require.True(t, d.fillIn.Focused())
	})
}

func TestMultiChoice_HandleKey(t *testing.T) {
	t.Parallel()

	t.Run("close key answers empty and closes", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.True(t, done)
		require.Nil(t, cmd)
		require.Equal(t, d.Request.ID, d.lastResponse.QuestionID)
		require.Empty(t, d.lastResponse.SelectedIDs)
	})

	t.Run("a number key toggles a choice on then off", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
		require.False(t, done)
		require.Equal(t, 1, d.cursorIdx)
		require.Equal(t, []string{"b"}, d.Response().SelectedIDs)

		done, _ = d.HandleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
		require.False(t, done)
		require.Empty(t, d.Response().SelectedIDs, "pressing the same number again toggles it off")
	})

	t.Run("a number key while the fill-in is focused types instead of toggling", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
		require.False(t, done)
		require.Equal(t, "2", d.fillIn.Value())
		require.Empty(t, d.Response().SelectedIDs)
	})

	t.Run("space toggles the choice under the cursor", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = 0
		done, _ := d.HandleKey(spaceKey)
		require.False(t, done)
		require.Equal(t, []string{"a"}, d.Response().SelectedIDs)
	})

	t.Run("space while hovering the fill-in slot focuses it instead of toggling", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.mouseActive = true
		d.hoveredChoice = len(d.Request.Choices)
		done, _ := d.HandleKey(spaceKey)
		require.False(t, done)
		require.True(t, d.fillIn.Focused())
		require.False(t, d.mouseActive)
	})

	t.Run("enter on a real choice submits the current selection", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.selected[0] = true
		done, _ := d.HandleKey(enterKey)
		require.True(t, done)
		require.Equal(t, []string{"a"}, d.Response().SelectedIDs)
	})

	t.Run("enter on the fill-in slot focuses it instead of submitting", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		done, _ := d.HandleKey(enterKey)
		require.False(t, done)
		require.True(t, d.fillIn.Focused())
	})

	t.Run("note key on a real choice notes that choice, not the question", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = 0
		d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
		require.True(t, d.noteEditor.Focused())
		require.Equal(t, "a", d.activeNoteKey)
	})

	t.Run("fill-in done with text submits the current selection plus text", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		d.fillIn.SetValue("other")
		done, _ := d.HandleKey(enterKey)
		require.True(t, done)
		require.Equal(t, "other", d.Response().FillInText)
	})

	t.Run("fill-in done with no text does not submit", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		done, _ := d.HandleKey(enterKey)
		require.False(t, done)
	})

	t.Run("closing the fill-in blurs it without submitting", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		d.fillIn.SetValue("draft")
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.False(t, done, "escape from the fill-in only blurs it in multi-choice")
		require.False(t, d.fillIn.Focused())
	})

	t.Run("an unmatched key is a no-op", func(t *testing.T) {
		t.Parallel()
		d := newTestMultiChoice(t)
		done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyF13})
		require.False(t, done)
		require.Nil(t, cmd)
	})
}

// TestMultiChoice_NoteEditorTakesPriorityAndNavClosesIt mirrors the
// single-choice case: a nav key while the note editor is focused
// both closes the note and moves the cursor.
func TestMultiChoice_NoteEditorTakesPriorityAndNavClosesIt(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	d.cursorIdx = 0
	d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	require.True(t, d.noteEditor.Focused())

	d.noteEditor.SetValue("note for alpha")
	done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	require.False(t, done)
	require.False(t, d.noteEditor.Focused(), "a nav key must close the note editor")
	require.Equal(t, 1, d.cursorIdx, "the nav key must also move the cursor")
	require.Equal(t, "note for alpha", d.notes["a"])
}

func TestMultiChoice_Draw(t *testing.T) {
	t.Parallel()

	d := newTestMultiChoice(t)
	d.selected[1] = true
	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Pick some")
	require.Contains(t, view, "Alpha")
	require.Contains(t, view, "Beta")
	require.Contains(t, view, "Gamma")
}
