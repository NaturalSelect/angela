package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestQueuedMessageItem_IdentityAndFinished(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewQueuedMessageItem(&sty, "do the thing", 3)

	require.Equal(t, "queued-3", item.ID())
	require.True(t, item.Finished(), "a queued prompt is always considered finished")
}

func TestQueuedMessageItem_RawRenderShowsMarkerAndPrompt(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewQueuedMessageItem(&sty, "run the tests", 0)

	out := ansi.Strip(item.RawRender(80))
	require.Contains(t, out, "queued")
	require.Contains(t, out, "run the tests")
}

// Render must apply the blurred assistant gutter to every line so a
// queued entry lines up with the rest of the transcript instead of
// sitting flush against the edge.
func TestQueuedMessageItem_RenderAddsGutterToEveryLine(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewQueuedMessageItem(&sty, "first line\nsecond line", 1)

	rendered := item.Render(80)
	raw := item.RawRender(80)

	prefix := sty.Messages.AssistantBlurred.Render()
	rawLines := strings.Split(raw, "\n")
	renderedLines := strings.Split(rendered, "\n")
	require.Len(t, renderedLines, len(rawLines))
	for i, ln := range rawLines {
		require.Equal(t, prefix+ln, renderedLines[i])
	}
}

// A blank prompt renders to nothing at all: no marker, no empty body,
// and Render must not panic on the empty RawRender output.
func TestQueuedMessageItem_BlankPromptRendersEmpty(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewQueuedMessageItem(&sty, "   \n  ", 0)

	require.Empty(t, item.RawRender(80))
	require.Empty(t, item.Render(80))
}

// A width too small to hold anything after the message padding is
// capped to <= 0 by cappedMessageWidth, which must short-circuit to an
// empty render rather than panicking on a negative width.
func TestQueuedMessageItem_TinyWidthRendersEmpty(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewQueuedMessageItem(&sty, "hello", 0)

	require.Empty(t, item.RawRender(1))
}
