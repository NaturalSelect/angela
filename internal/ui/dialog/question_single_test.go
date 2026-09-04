package dialog

import (
	"fmt"
	"image"
	"testing"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSingleChoice_GetRequest(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	require.Equal(t, d.Request, d.GetRequest())
}

func TestSingleChoice_RespondFillIn(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	resp := d.respondFillIn("custom answer")
	require.Equal(t, d.Request.ID, resp.QuestionID)
	require.Equal(t, "custom answer", resp.FillInText)
	require.Empty(t, resp.SelectedIDs)
}

func TestNumKeyBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		n     int
		label string
	}{
		{"zero clamps to one choice", 0, "1-1"},
		{"negative clamps to one choice", -3, "1-1"},
		{"in range", 3, "1-3"},
		{"above nine caps at nine", 15, "1-9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := numKeyBinding(tc.n)
			require.Equal(t, tc.label, b.Help().Key)
			require.Equal(t, "quick select", b.Help().Desc)
		})
	}
}

func TestSingleChoice_ShortHelp(t *testing.T) {
	t.Parallel()

	t.Run("default shows navigation and selection keys", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		require.Len(t, d.ShortHelp(), 6)
	})

	t.Run("note editor open shows save/close only", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
		require.Len(t, d.ShortHelp(), 2)
	})

	t.Run("fill-in focused shows the trimmed set", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		require.Len(t, d.ShortHelp(), 3)
	})
}

func TestSingleChoice_HeightHeightChangedSetFocusedSetHover(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	require.Positive(t, d.Height(40))
	require.False(t, d.HeightChanged())

	d.SetFocused(true)
	require.True(t, d.focused)

	// setHover with no compositor yet (never drawn) still records the
	// hover as active with no resolved choice.
	d.SetHover(3, 3)
	require.True(t, d.mouseActive)
	require.Equal(t, -1, d.hoveredChoice)
}

func TestSingleChoice_HandlePaste(t *testing.T) {
	t.Parallel()

	t.Run("dropped when the fill-in is not focused", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		require.Nil(t, d.HandlePaste(tea.PasteMsg{Content: "x"}))
	})

	t.Run("forwarded to the fill-in when focused", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		d.HandlePaste(tea.PasteMsg{Content: "pasted"})
		require.Equal(t, "pasted", d.fillIn.Value())
	})
}

// TestSingleChoice_HandleMouseClick verifies clicking a choice selects
// it without submitting, clicking the fill-in focuses it, and a miss
// (including before any Draw) is not handled.
func TestSingleChoice_HandleMouseClick(t *testing.T) {
	t.Parallel()

	t.Run("miss before any draw is unhandled", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		done, handled := d.HandleMouseClick(0, 0)
		require.False(t, done)
		require.False(t, handled)
	})

	t.Run("clicking a choice selects but does not submit", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		const w, h = 40, 20
		scr := uv.NewScreenBuffer(w, h)
		d.Draw(scr, image.Rect(0, 0, w, h))

		x, y := choiceHitPos(t, d.choiceCompositor, 1, w, h) // Beta
		done, handled := d.HandleMouseClick(x, y)
		require.False(t, done, "single choice does not auto-submit on click")
		require.True(t, handled)
		require.Equal(t, 1, d.cursorIdx)
		require.Equal(t, []string{"b"}, d.Response().SelectedIDs)
	})

	t.Run("clicking the fill-in row focuses it without submitting", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
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

// choiceHitPos scans a screen-sized grid for coordinates that hit
// choice idx (or the fill-in row, whose index equals len(choices)) in
// a choiceList's hit compositor.
func choiceHitPos(t *testing.T, compositor *lipgloss.Compositor, idx, maxW, maxH int) (x, y int) {
	t.Helper()
	for y := range maxH {
		for x := range maxW {
			hit := compositor.Hit(x, y)
			if hit.Empty() {
				continue
			}
			var got int
			if _, err := fmt.Sscanf(hit.ID(), "choice_%d", &got); err == nil && got == idx {
				return x, y
			}
		}
	}
	t.Fatalf("choice %d not found on screen", idx)
	return 0, 0
}

func TestSingleChoice_HandleKey(t *testing.T) {
	t.Parallel()

	t.Run("close key answers empty and closes", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.True(t, done)
		require.Nil(t, cmd)
		require.Equal(t, d.Request.ID, d.lastResponse.QuestionID)
		require.Empty(t, d.lastResponse.SelectedIDs)
	})

	t.Run("a number key jumps directly to that choice", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
		require.False(t, done)
		require.Equal(t, 1, d.cursorIdx)
		require.False(t, d.mouseActive)
	})

	t.Run("enter while hovering adopts the hovered choice", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.mouseActive = true
		d.hoveredChoice = 2
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.True(t, done)
		require.Equal(t, []string{"c"}, d.Response().SelectedIDs)
	})

	t.Run("enter while hovering the fill-in focuses instead of submitting", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.mouseActive = true
		d.hoveredChoice = len(d.Request.Choices)
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.False(t, done)
		require.True(t, d.fillIn.Focused())
	})

	t.Run("note key on a real choice notes that choice, not the question", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = 0
		d.HandleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
		require.True(t, d.noteEditor.Focused())
		require.Equal(t, "a", d.activeNoteKey)
	})

	t.Run("fill-in done with text submits a free-form answer", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		d.fillIn.SetValue("something else")
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.True(t, done)
		require.Equal(t, "something else", d.Response().FillInText)
	})

	t.Run("fill-in done with no text does not submit", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.False(t, done)
	})

	t.Run("closing the fill-in blurs it and still records any typed text", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		d.fillIn.SetValue("draft")
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.True(t, done)
		require.False(t, d.fillIn.Focused())
		require.Equal(t, "draft", d.Response().FillInText)
	})

	t.Run("a number key while the fill-in is focused types instead of jumping", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		d.cursorIdx = len(d.Request.Choices)
		d.fillIn.Focus()
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
		require.False(t, done)
		require.Equal(t, "2", d.fillIn.Value())
	})

	t.Run("an unmatched key is a no-op", func(t *testing.T) {
		t.Parallel()
		d := newTestSingleChoice(t)
		done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyF13})
		require.False(t, done)
		require.Nil(t, cmd)
	})
}

// TestSingleChoice_NoteEditorTakesPriorityAndNavClosesIt verifies the
// note-editor-active branch: the close key saves and exits, while a
// nav key both closes the note and moves the cursor.
func TestSingleChoice_NoteEditorTakesPriorityAndNavClosesIt(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
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

func TestSingleChoice_Draw(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Pick one")
	require.Contains(t, view, "Alpha")
	require.Contains(t, view, "Beta")
	require.Contains(t, view, "Gamma")
}

// TestSingleChoice_DrawWithFillInFocusedReportsCursor verifies the
// hardware cursor is surfaced while the fill-in textarea is active.
func TestSingleChoice_DrawWithFillInFocusedReportsCursor(t *testing.T) {
	t.Parallel()

	d := newTestSingleChoice(t)
	d.cursorIdx = len(d.Request.Choices)
	d.fillIn.Focus()

	const w, h = 40, 20
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, cur)
}
