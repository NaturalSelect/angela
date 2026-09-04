package dialog

import (
	"fmt"
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/question"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// tabHitPos scans a screen-sized grid for coordinates that hit tab
// idx in a QuestionForm's tab-bar hit compositor.
func tabHitPos(t *testing.T, compositor *lipgloss.Compositor, idx, maxW, maxH int) (x, y int) {
	t.Helper()
	for y := range maxH {
		for x := range maxW {
			hit := compositor.Hit(x, y)
			if hit.Empty() {
				continue
			}
			var got int
			if _, err := fmt.Sscanf(hit.ID(), "tab_%d", &got); err == nil && got == idx {
				return x, y
			}
		}
	}
	t.Fatalf("tab %d not found on screen", idx)
	return 0, 0
}

func TestShortLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"three words or fewer are kept as-is", "Proceed now?", "Proceed now?"},
		{"more than three words is truncated to three", "Should we deploy to production now?", "Should we deploy"},
		{"newlines are collapsed to spaces before truncation", "Line one\nLine two extra words", "Line one Line"},
		{"empty text stays empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, shortLabel(tc.in))
		})
	}
}

func TestQuestionForm_IsAnswered(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())
	require.False(t, f.isAnswered(0), "unanswered by default")
	require.False(t, f.isAnswered(99), "out-of-range index is not answered")

	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle q1 to Yes
	f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, f.isAnswered(0))
}

func TestQuestionForm_ShortHelp(t *testing.T) {
	t.Parallel()

	t.Run("question tab appends the view-chat hint last", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		help := f.ShortHelp()
		require.NotEmpty(t, help)
		require.Equal(t, f.keyViewChat, help[len(help)-1])
		require.Contains(t, help, f.keyPrevTab)
		require.Contains(t, help, f.keyNextTab)
	})

	t.Run("confirm tab delegates to the confirm component", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["}) // wrap to confirm
		require.True(t, f.isConfirmTab())
		require.Equal(t, f.confirmComp.ShortHelp(), f.ShortHelp())
	})
}

func TestQuestionForm_Height(t *testing.T) {
	t.Parallel()

	t.Run("single question without tabs has no tab-chrome overhead", func(t *testing.T) {
		t.Parallel()
		single := newTestQuestionForm(t, singleYesNoRequest())
		multi := newTestQuestionForm(t, twoQuestionRequest())
		require.False(t, single.showTabs)
		require.True(t, multi.showTabs)
		require.Greater(t, multi.Height(60)-multi.questions[0].Height(60), single.Height(60)-single.questions[0].Height(60))
	})

	t.Run("uses the tallest tab so switching does not jump", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		want := 4 // tab chrome
		maxQ := 0
		for _, q := range f.questions {
			if qh := q.Height(60); qh > maxQ {
				maxQ = qh
			}
		}
		if ch := f.confirmComp.Height(60); ch > maxQ {
			maxQ = ch
		}
		want += maxQ
		require.Equal(t, want, f.Height(60))
	})
}

func TestQuestionForm_CollapsedHeight(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())
	require.Equal(t, 1, f.CollapsedHeight())
}

func TestQuestionForm_DrawCollapsed(t *testing.T) {
	t.Parallel()

	t.Run("multi-question shows the active question and answered count", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 60, 1
		scr := uv.NewScreenBuffer(w, h)
		f.DrawCollapsed(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "First?")
		require.Contains(t, view, "0/2 answered")
	})

	t.Run("the confirm tab shows its own title", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["}) // wrap to confirm
		require.True(t, f.isConfirmTab())

		const w, h = 60, 1
		scr := uv.NewScreenBuffer(w, h)
		f.DrawCollapsed(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, f.confirmComp.Title)
	})

	t.Run("a single question shows just the question text with no count", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, singleYesNoRequest())
		const w, h = 60, 1
		scr := uv.NewScreenBuffer(w, h)
		f.DrawCollapsed(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "Proceed?")
		require.NotContains(t, view, "answered")
	})

	t.Run("a narrow area does not panic", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 5, 1
		scr := uv.NewScreenBuffer(w, h)
		require.NotPanics(t, func() {
			f.DrawCollapsed(scr, image.Rect(0, 0, w, h))
		})
	})
}

func TestQuestionForm_GetQuestionText(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())
	require.Equal(t, "First?", f.getQuestionText(0))
	require.Equal(t, "Confirm", f.getQuestionText(f.numQuestions), "the confirm tab falls back to its label")
	require.Equal(t, "", f.getQuestionText(999))
}

func TestQuestionForm_HeightChanged(t *testing.T) {
	t.Parallel()

	f := newTestQuestionForm(t, twoQuestionRequest())
	require.False(t, f.HeightChanged(), "all question heights are deterministic")
}

func TestQuestionForm_SetFocused(t *testing.T) {
	t.Parallel()

	t.Run("propagates to the active question", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.SetFocused(false)
		require.False(t, f.focused)

		f.SetFocused(true)
		require.True(t, f.focused)
	})

	t.Run("propagates to the confirm component on the confirm tab", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["})
		require.True(t, f.isConfirmTab())
		f.SetFocused(true)
		require.True(t, f.confirmComp.focused)
	})

	t.Run("an empty form neither panics nor focuses anything", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, question.Request{ID: "empty"})
		require.NotPanics(t, func() {
			f.SetFocused(true)
		})
	})
}

func TestQuestionForm_SetHover(t *testing.T) {
	t.Parallel()

	t.Run("propagates to the active question", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.SetHover(3, 4)
		require.Equal(t, 3, f.hoverX)
		require.Equal(t, 4, f.hoverY)
		yn := f.questions[0].(*YesNo)
		require.Equal(t, 3, yn.hoverX)
		require.Equal(t, 4, yn.hoverY)
	})

	t.Run("propagates to the confirm component on the confirm tab", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["})
		f.SetHover(5, 6)
		require.Equal(t, 5, f.confirmComp.hoverX)
		require.Equal(t, 6, f.confirmComp.hoverY)
	})
}

func TestQuestionForm_HandlePaste(t *testing.T) {
	t.Parallel()

	t.Run("no-op on the confirm tab", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["})
		require.Nil(t, f.HandlePaste(tea.PasteMsg{Content: "x"}))
	})

	t.Run("forwards to a question that supports pasting", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // advance to q2 (FreeText)
		require.Equal(t, 1, f.activeIdx)
		f.questions[1].SetFocused(true)
		f.HandlePaste(tea.PasteMsg{Content: "pasted"})
		ft := f.questions[1].(*FreeText)
		require.Equal(t, "pasted", ft.editor.Value())
	})
}

func TestQuestionForm_HandleWheel(t *testing.T) {
	t.Parallel()

	t.Run("on the confirm tab, scrolls within bounds", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["})
		require.True(t, f.isConfirmTab())

		f.HandleWheel(0, -1) // clamped at zero
		require.Zero(t, f.confirmComp.scrollOffset)

		f.HandleWheel(0, 1)
		require.Equal(t, 1, f.confirmComp.scrollOffset)

		f.HandleWheel(0, -1)
		require.Zero(t, f.confirmComp.scrollOffset)
	})

	t.Run("delegates to a question that supports wheel scrolling", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.activeIdx = 1 // FreeText
		f.HandleWheel(0, 2)
		ft := f.questions[1].(*FreeText)
		require.Equal(t, 2, ft.scrollOffset)
	})

	t.Run("is a no-op on a question that does not support wheel scrolling", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		require.NotPanics(t, func() {
			f.HandleWheel(0, 2) // q1 is YesNo
		})
	})

	t.Run("an out-of-range active index is a no-op", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, singleYesNoRequest())
		f.activeIdx = 5
		require.NotPanics(t, func() {
			f.HandleWheel(0, 1)
		})
	})
}

func TestQuestionForm_HandleMouseClick(t *testing.T) {
	t.Parallel()

	t.Run("clicking a tab switches to it and reports handled without done", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		x, y := tabHitPos(t, f.compositor, 1, w, h)
		done, handled := f.HandleMouseClick(x, y)
		require.False(t, done)
		require.True(t, handled)
		require.Equal(t, 1, f.activeIdx)
	})

	t.Run("clicking the already-active tab is handled without switching", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		x, y := tabHitPos(t, f.compositor, 0, w, h)
		done, handled := f.HandleMouseClick(x, y)
		require.False(t, done)
		require.True(t, handled)
		require.Equal(t, 0, f.activeIdx)
	})

	t.Run("clicking a button on the active question advances but does not submit when a confirm tab remains", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		yn := f.questions[0].(*YesNo)
		x, y := hitButtonPos(t, yn.compositor, 0, w, h) // Yes
		done, handled := f.HandleMouseClick(x, y)
		require.False(t, done)
		require.True(t, handled)
		require.Equal(t, 1, f.activeIdx, "answering the last real question before confirm must advance the tab")
	})

	t.Run("clicking a button on the last question submits directly when there is no confirm tab", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, singleYesNoRequest())
		var got []question.Answer
		f.OnAnswer = func(r []question.Answer) { got = r }

		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		yn := f.questions[0].(*YesNo)
		x, y := hitButtonPos(t, yn.compositor, 0, w, h) // Yes
		done, handled := f.HandleMouseClick(x, y)
		require.True(t, done)
		require.True(t, handled)
		require.Len(t, got, 1)
		require.True(t, *got[0].Yes)
	})

	t.Run("clicking a button on the confirm tab delegates to it", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["})
		require.True(t, f.isConfirmTab())

		var got []question.Answer
		f.OnAnswer = func(r []question.Answer) { got = r }

		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		x, y := hitButtonPos(t, f.confirmComp.compositor, 0, w, h) // Yup!
		done, handled := f.HandleMouseClick(x, y)
		require.True(t, done)
		require.True(t, handled)
		require.NotNil(t, got)
	})

	t.Run("a miss outside any interactive element is unhandled", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, singleYesNoRequest())
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		done, handled := f.HandleMouseClick(1000, 1000)
		require.False(t, done)
		require.False(t, handled)
	})
}

func TestQuestionForm_Draw(t *testing.T) {
	t.Parallel()

	t.Run("renders the tab bar and the active question", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "First?")
	})

	t.Run("a single question renders without tab chrome", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, singleYesNoRequest())
		require.False(t, f.showTabs)
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "Proceed?")
		require.Nil(t, f.compositor)
	})

	t.Run("the confirm tab is drawn through the confirm component", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: '[', Text: "["})
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, f.confirmComp.Title)
	})

	t.Run("a cursor from the active question is offset by the tab chrome height", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.activeIdx = 1 // FreeText always reports a cursor once focused
		f.questions[1].SetFocused(true)
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		cur := f.Draw(scr, image.Rect(0, 0, w, h))
		require.NotNil(t, cur)
		require.Positive(t, cur.Y, "the cursor must be pushed down past the tab bar")
	})

	t.Run("a very narrow width collapses to single-tab counter mode without panicking", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, question.Request{
			ID: "narrow",
			Questions: []question.Question{
				{ID: "q1", Type: question.TypeYesNo, Text: "One?"},
				{ID: "q2", Type: question.TypeYesNo, Text: "Two?"},
				{ID: "q3", Type: question.TypeYesNo, Text: "Three?"},
			},
		})
		const w, h = 16, 20
		scr := uv.NewScreenBuffer(w, h)
		require.NotPanics(t, func() {
			f.Draw(scr, image.Rect(0, 0, w, h))
		})
		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "/4") // "N/M" counter, 4 tabs incl. confirm
	})

	t.Run("a moderately narrow width truncates tab labels fairly without panicking", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, question.Request{
			ID: "medium",
			Questions: []question.Question{
				{ID: "q1", Type: question.TypeYesNo, Text: "A longer first question label"},
				{ID: "q2", Type: question.TypeYesNo, Text: "A longer second question label"},
			},
		})
		const w, h = 40, 20
		scr := uv.NewScreenBuffer(w, h)
		require.NotPanics(t, func() {
			f.Draw(scr, image.Rect(0, 0, w, h))
		})
	})

	t.Run("hovering a non-active tab highlights it without panicking", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		f.Draw(scr, image.Rect(0, 0, w, h))

		x, y := tabHitPos(t, f.compositor, 1, w, h)
		f.SetHover(x, y)
		require.NotPanics(t, func() {
			f.Draw(scr, image.Rect(0, 0, w, h))
		})
	})

	t.Run("an answered tab is styled distinctly without panicking", func(t *testing.T) {
		t.Parallel()
		f := newTestQuestionForm(t, twoQuestionRequest())
		f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // answers q1, advances to q2
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		require.NotPanics(t, func() {
			f.Draw(scr, image.Rect(0, 0, w, h))
		})
	})
}
