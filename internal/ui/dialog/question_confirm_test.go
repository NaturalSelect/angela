package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/question"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestConfirm(t *testing.T) *ConfirmComponent {
	t.Helper()
	sty := testStyles()
	requests := []question.Question{
		{ID: "q1", Type: question.TypeSingleChoice, Text: "Pick one", Choices: []question.Choice{
			{ID: "a", Label: "Alpha"},
			{ID: "b", Label: "Beta"},
		}},
		{ID: "q2", Type: question.TypeYesNo, Text: "Proceed?"},
	}
	yes := true
	answers := []*question.Answer{
		{QuestionID: "q1", SelectedIDs: []string{"a"}},
		{QuestionID: "q2", Yes: &yes},
	}
	return NewConfirmComponent(sty, "Confirm", "Please review", []string{"Pick one", "Proceed?"}, requests, answers)
}

func TestNewConfirmComponent_DefaultTitle(t *testing.T) {
	t.Parallel()

	t.Run("empty title falls back to the default", func(t *testing.T) {
		t.Parallel()
		c := NewConfirmComponent(testStyles(), "", "", nil, nil, nil)
		require.Equal(t, "Ready to go?", c.Title)
	})

	t.Run(`"Confirm" placeholder also falls back to the default`, func(t *testing.T) {
		t.Parallel()
		c := NewConfirmComponent(testStyles(), "Confirm", "", nil, nil, nil)
		require.Equal(t, "Ready to go?", c.Title)
	})

	t.Run("a custom title is kept as-is", func(t *testing.T) {
		t.Parallel()
		c := NewConfirmComponent(testStyles(), "All set?", "", nil, nil, nil)
		require.Equal(t, "All set?", c.Title)
	})
}

func TestConfirmComponent_Response(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)
	require.Equal(t, question.Answer{}, c.Response())
}

func TestConfirmComponent_ShortHelp(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)
	require.Len(t, c.ShortHelp(), 6)
}

func TestConfirmComponent_UnansweredCount(t *testing.T) {
	t.Parallel()

	no := false
	tests := []struct {
		name    string
		answers []*question.Answer
		want    int
	}{
		{"all answered", []*question.Answer{{SelectedIDs: []string{"a"}}, {Yes: &no}}, 0},
		{"nil answer counts as unanswered", []*question.Answer{nil, {Yes: &no}}, 1},
		{"empty answer counts as unanswered", []*question.Answer{{}, {FillInText: "x"}}, 1},
		{"no answers", nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTestConfirm(t)
			c.Answers = tc.answers
			require.Equal(t, tc.want, c.unansweredCount())
		})
	}
}

func TestConfirmComponent_AnswerSummary(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)

	t.Run("out of range index is not answered", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "(not answered)", c.answerSummary(5))
	})

	t.Run("selected choice IDs resolve to labels", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "Alpha", c.answerSummary(0))
	})

	t.Run("yes/no answers render as Yes or No", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "Yes", c.answerSummary(1))

		no := false
		c2 := newTestConfirm(t)
		c2.Answers[1] = &question.Answer{QuestionID: "q2", Yes: &no}
		require.Equal(t, "No", c2.answerSummary(1))
	})

	t.Run("fill-in text is included alongside selections", func(t *testing.T) {
		t.Parallel()
		c2 := newTestConfirm(t)
		c2.Answers[0] = &question.Answer{QuestionID: "q1", SelectedIDs: []string{"b"}, FillInText: "extra"}
		require.Equal(t, "Beta; extra", c2.answerSummary(0))
	})

	t.Run("an empty answer at a valid index is not answered", func(t *testing.T) {
		t.Parallel()
		c2 := newTestConfirm(t)
		c2.Answers[0] = &question.Answer{QuestionID: "q1"}
		require.Equal(t, "(not answered)", c2.answerSummary(0))
	})
}

func TestConfirmComponent_ChoiceLabel(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)
	require.Equal(t, "Alpha", c.choiceLabel(0, "a"))
	require.Equal(t, "unknown-id", c.choiceLabel(0, "unknown-id"), "an unresolved ID falls back to itself")
	require.Equal(t, "x", c.choiceLabel(99, "x"), "an out-of-range question index falls back to the ID")
}

func TestConfirmComponent_Height(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)
	base := c.Height(60)
	require.Positive(t, base)

	t.Run("a description adds height", func(t *testing.T) {
		t.Parallel()
		withDesc := newTestConfirm(t)
		withDesc.Description = ""
		noDescHeight := withDesc.Height(60)
		withDesc.Description = "Some extra context about this confirmation."
		require.Greater(t, withDesc.Height(60), noDescHeight)
	})

	t.Run("unanswered questions add a warning line", func(t *testing.T) {
		t.Parallel()
		c2 := newTestConfirm(t)
		answered := c2.Height(60)
		c2.Answers[0] = nil
		require.Greater(t, c2.Height(60), answered)
	})

	t.Run("non-positive width falls back to lastWidth", func(t *testing.T) {
		t.Parallel()
		c2 := newTestConfirm(t)
		c2.lastWidth = 60
		require.Equal(t, c2.Height(60), c2.Height(0))
	})

	t.Run("non-positive width and lastWidth fall back to the max content width", func(t *testing.T) {
		t.Parallel()
		c2 := newTestConfirm(t)
		require.Zero(t, c2.lastWidth)
		require.Positive(t, c2.Height(0))
	})
}

func TestConfirmComponent_HeightChangedSetFocusedSetHover(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)
	require.False(t, c.HeightChanged())

	c.SetFocused(true)
	require.True(t, c.focused)

	c.SetHover(4, 5)
	require.Equal(t, 4, c.hoverX)
	require.Equal(t, 5, c.hoverY)
}

func TestConfirmComponent_Draw(t *testing.T) {
	t.Parallel()

	t.Run("renders title, bullets, warning, and buttons", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		c.Answers[1] = nil // force the unanswered warning
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		cur := c.Draw(scr, image.Rect(0, 0, w, h))
		require.Nil(t, cur)

		view := ansi.Strip(scr.Render())
		require.Contains(t, view, "Ready to go?", "the constructor normalizes the literal \"Confirm\" title")
		require.Contains(t, view, "Please review")
		require.Contains(t, view, "Pick one")
		require.Contains(t, view, "Alpha")
		require.Contains(t, view, "unanswered")
		require.Contains(t, view, "Yup!")
		require.Contains(t, view, "Not yet")
	})

	t.Run("a small viewport overflows and shows a scrollbar without panicking", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		const w, h = 30, 4
		scr := uv.NewScreenBuffer(w, h)
		c.Draw(scr, image.Rect(0, 0, w, h))
	})
}

func TestConfirmComponent_HandleMouseClick(t *testing.T) {
	t.Parallel()

	t.Run("clicking Yup! confirms", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		var confirmed, rejected bool
		c.OnConfirm = func() { confirmed = true }
		c.OnReject = func() { rejected = true }
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		c.Draw(scr, image.Rect(0, 0, w, h))

		x, y := hitButtonPos(t, c.compositor, 0, w, h)
		done, handled := c.HandleMouseClick(x, y)
		require.True(t, done)
		require.True(t, handled)
		require.True(t, c.confirmYes)
		require.True(t, confirmed)
		require.False(t, rejected)
	})

	t.Run("clicking Not yet rejects", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		var rejected bool
		c.OnReject = func() { rejected = true }
		const w, h = 60, 20
		scr := uv.NewScreenBuffer(w, h)
		c.Draw(scr, image.Rect(0, 0, w, h))

		x, y := hitButtonPos(t, c.compositor, 1, w, h)
		done, handled := c.HandleMouseClick(x, y)
		require.False(t, done)
		require.True(t, handled)
		require.False(t, c.confirmYes)
		require.True(t, rejected)
	})

	t.Run("a miss before any draw is unhandled", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		done, handled := c.HandleMouseClick(0, 0)
		require.False(t, done)
		require.False(t, handled)
	})
}

func TestConfirmComponent_UpdateAnswers(t *testing.T) {
	t.Parallel()

	c := newTestConfirm(t)
	newAnswers := []*question.Answer{{QuestionID: "q1", FillInText: "new"}}
	c.UpdateAnswers(newAnswers)
	require.Equal(t, newAnswers, c.Answers)
}

func TestConfirmComponent_HandleKey(t *testing.T) {
	t.Parallel()

	t.Run("up scrolls up but is clamped at zero", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		done, cmd := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})
		require.False(t, done)
		require.Nil(t, cmd)
		require.Zero(t, c.scrollOffset)
	})

	t.Run("down scrolls down", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		done, _ := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyDown})
		require.False(t, done)
		require.Equal(t, 1, c.scrollOffset)
	})

	t.Run("left and right toggle which button is selected", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		require.True(t, c.confirmYes)
		c.HandleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		require.False(t, c.confirmYes)
		c.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight})
		require.True(t, c.confirmYes)
	})

	t.Run("y and n select yes/no directly", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		c.HandleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
		require.False(t, c.confirmYes)
		c.HandleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
		require.True(t, c.confirmYes)
	})

	t.Run("enter while yes is selected confirms", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		var confirmed bool
		c.OnConfirm = func() { confirmed = true }
		done, cmd := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.True(t, done)
		require.Nil(t, cmd)
		require.True(t, confirmed)
	})

	t.Run("enter while no is selected rejects without closing", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		c.confirmYes = false
		var rejected bool
		c.OnReject = func() { rejected = true }
		done, _ := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.False(t, done)
		require.True(t, rejected)
	})

	t.Run("close key rejects", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		var rejected bool
		c.OnReject = func() { rejected = true }
		done, _ := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.False(t, done)
		require.True(t, rejected)
	})

	t.Run("nil callbacks do not panic", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		require.NotPanics(t, func() {
			c.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		})
		c2 := newTestConfirm(t)
		c2.confirmYes = false
		require.NotPanics(t, func() {
			c2.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		})
	})

	t.Run("an unmatched key is a no-op", func(t *testing.T) {
		t.Parallel()
		c := newTestConfirm(t)
		done, cmd := c.HandleKey(tea.KeyPressMsg{Code: tea.KeyF13})
		require.False(t, done)
		require.Nil(t, cmd)
	})
}
