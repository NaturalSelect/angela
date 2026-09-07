package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestSideQuestion(t *testing.T, question, answer string) *SideQuestion {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewSideQuestion(com, question, answer)
}

func TestSideQuestion_ID(t *testing.T) {
	t.Parallel()

	s := newTestSideQuestion(t, "question", "answer")
	require.Equal(t, SideQuestionID, s.ID())
}

func TestSideQuestion_HandleMsg(t *testing.T) {
	t.Parallel()

	t.Run("escape closes the dialog", func(t *testing.T) {
		t.Parallel()
		s := newTestSideQuestion(t, "question", "answer")
		require.Equal(t, ActionClose{}, s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
	})

	t.Run("enter closes the dialog", func(t *testing.T) {
		t.Parallel()
		s := newTestSideQuestion(t, "question", "answer")
		require.Equal(t, ActionClose{}, s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	})

	t.Run("space closes the dialog", func(t *testing.T) {
		t.Parallel()
		s := newTestSideQuestion(t, "question", "answer")
		require.Equal(t, ActionClose{}, s.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}))
	})

	t.Run("other keys are handed to the viewport instead of closing", func(t *testing.T) {
		t.Parallel()
		s := newTestSideQuestion(t, "question", "answer")
		action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
		require.Nil(t, action)
	})
}

// TestSideQuestion_Draw verifies the title, the quoted question and the
// answer all render, proving NewSideQuestion's blockquote formatting
// survives the markdown pass without eating the underlying text.
func TestSideQuestion_Draw(t *testing.T) {
	t.Parallel()

	s := newTestSideQuestion(t, "what should we build next", "the answer is 42")
	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	s.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Side Question")
	require.Contains(t, view, "what should we build next")
	require.Contains(t, view, "the answer is 42")
}

func TestSideQuestion_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	s := newTestSideQuestion(t, "question", "answer")
	require.Len(t, s.ShortHelp(), 3)

	var total int
	for _, row := range s.FullHelp() {
		total += len(row)
	}
	require.Equal(t, 3, total)
}
