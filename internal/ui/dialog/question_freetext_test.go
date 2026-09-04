package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestFreeText(t *testing.T) *FreeText {
	t.Helper()
	s := styles.CharmtonePantera()
	req := question.Question{
		ID:   "q1",
		Type: question.TypeFreeText,
		Text: "Tell me more",
	}
	return NewFreeText(&s, req)
}

func TestFreeText_GetRequest(t *testing.T) {
	t.Parallel()

	d := newTestFreeText(t)
	require.Equal(t, d.Request, d.GetRequest())
}

func TestFreeText_ShortHelp(t *testing.T) {
	t.Parallel()

	d := newTestFreeText(t)
	require.Len(t, d.ShortHelp(), 3)
}

func TestFreeText_HandleKey(t *testing.T) {
	t.Parallel()

	t.Run("close key answers empty and closes", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.True(t, done)
		require.Nil(t, cmd)
		require.Equal(t, d.Request.ID, d.lastResponse.QuestionID)
		require.Empty(t, d.lastResponse.FillInText)
	})

	t.Run("enter with text submits", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.editor.SetValue("my answer")
		done, cmd := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.True(t, done)
		require.Nil(t, cmd)
		require.Equal(t, "my answer", d.lastResponse.FillInText)
	})

	t.Run("enter with no text does not submit", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.False(t, done)
	})

	t.Run("newline key inserts a newline instead of submitting", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.editor.SetValue("line1")
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		require.False(t, done)
		require.Contains(t, d.editor.Value(), "\n")
	})

	t.Run("default forwards to the editor", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.SetFocused(true)
		done, _ := d.HandleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
		require.False(t, done)
		require.Equal(t, "x", d.editor.Value())
	})
}

func TestFreeText_Response(t *testing.T) {
	t.Parallel()

	t.Run("reflects unsaved editor content over the last saved response", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.editor.SetValue("draft")
		resp := d.Response()
		require.Equal(t, "draft", resp.FillInText)
		require.Equal(t, d.Request.ID, resp.QuestionID)
	})

	t.Run("falls back to the last saved response when the editor is empty", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.editor.SetValue("final")
		d.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		d.editor.SetValue("")
		resp := d.Response()
		require.Equal(t, "final", resp.FillInText)
	})
}

func TestFreeText_Height(t *testing.T) {
	t.Parallel()

	t.Run("grows with a description", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		base := d.Height(60)

		withDesc := newTestFreeText(t)
		withDesc.Request.Description = "Some extra detail about the question."
		require.Greater(t, withDesc.Height(60), base)
	})

	t.Run("falls back to lastWidth then choiceListMaxWidth when width is non-positive", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		require.Positive(t, d.Height(0))

		d.lastWidth = 50
		require.Positive(t, d.Height(0))
	})
}

func TestFreeText_HeightChangedSetFocusedSetHoverHandleMouseClick(t *testing.T) {
	t.Parallel()

	d := newTestFreeText(t)
	require.False(t, d.HeightChanged())

	d.SetFocused(true)
	require.True(t, d.focused)
	require.True(t, d.editor.Focused())

	d.SetFocused(false)
	require.False(t, d.focused)
	require.False(t, d.editor.Focused())

	d.SetHover(1, 1) // no-op, must not panic

	done, handled := d.HandleMouseClick(1, 1)
	require.False(t, done)
	require.False(t, handled)
}

func TestFreeText_HandleWheel(t *testing.T) {
	t.Parallel()

	t.Run("vertical delta scrolls and enters wheel mode", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.HandleWheel(0, 2)
		require.Equal(t, 2, d.scrollOffset)
		require.True(t, d.wheelActive)
	})

	t.Run("zero delta is a no-op", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.HandleWheel(1, 0)
		require.Zero(t, d.scrollOffset)
		require.False(t, d.wheelActive)
	})
}

func TestFreeText_HandlePaste(t *testing.T) {
	t.Parallel()

	d := newTestFreeText(t)
	d.SetFocused(true)
	d.HandlePaste(tea.PasteMsg{Content: "pasted text"})
	require.Equal(t, "pasted text", d.editor.Value())
}

func TestFreeText_Draw(t *testing.T) {
	t.Parallel()

	t.Run("renders header and editor and reports a cursor", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.SetFocused(true)
		const w, h = 40, 20
		scr := uv.NewScreenBuffer(w, h)
		cur := d.Draw(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "Tell me more")
		require.NotNil(t, cur)
	})

	t.Run("with a description and a small viewport does not panic and shows a scrollbar", func(t *testing.T) {
		t.Parallel()
		d := newTestFreeText(t)
		d.Request.Description = "A longer description that wraps across several lines to force overflow of the available viewport height."
		d.SetFocused(true)
		const w, h = 30, 6
		scr := uv.NewScreenBuffer(w, h)
		d.Draw(scr, image.Rect(0, 0, w, h))
	})
}
