package chat

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestPendingSideQuestionItemShowsQuestionNotAnswer(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewPendingSideQuestionItem(&sty, "what does this function do?")

	rendered := ansi.Strip(item.Render(80))
	require.Contains(t, rendered, "what does this function do?")
	require.False(t, item.Finished(), "freshly created item should be pending")
}

func TestSideQuestionItemCompleteRendersMarkdownAnswer(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewPendingSideQuestionItem(&sty, "why?")
	item.Complete("**bold** answer")

	rendered := ansi.Strip(item.Render(80))
	require.Contains(t, rendered, "bold")
	require.True(t, item.Finished())
}

func TestSideQuestionItemFailRendersError(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewPendingSideQuestionItem(&sty, "why?")
	item.Fail(errors.New("boom"))

	rendered := ansi.Strip(item.Render(80))
	require.Contains(t, rendered, "boom")
	require.True(t, item.Finished())
}

func TestSideQuestionItemHandleKeyEventCopiesToClipboard(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewPendingSideQuestionItem(&sty, "why?")
	item.Complete("because")

	handled, cmd := item.HandleKeyEvent(tea.KeyPressMsg{Code: 'c', Text: "c"})
	require.True(t, handled)
	require.NotNil(t, cmd)
}
