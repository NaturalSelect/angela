package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSystemNoticeItem_IdentityAndFinished(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "sys-1",
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Switched to the coder agent."},
		},
	}
	item := NewSystemNoticeItem(&sty, msg)

	require.Equal(t, "sys-1:system-notice", item.ID())
	require.True(t, item.Finished())
}

func TestSystemNoticeItem_RawRenderShowsText(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "sys-1",
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Switched to the coder agent."},
		},
	}
	item := NewSystemNoticeItem(&sty, msg)

	out := ansi.Strip(item.RawRender(80))
	require.Contains(t, out, "Switched to the coder agent.")

	// A second call must hit the cached render and produce identical
	// output.
	out2 := ansi.Strip(item.RawRender(80))
	require.Equal(t, out, out2)
}

// Render prefixes every line with the section-header gutter, and the
// prefixed-render cache must serve identical output across repeat
// calls at the same width.
func TestSystemNoticeItem_RenderIsCachedAndPrefixed(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "sys-1",
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Agent switched."},
		},
	}
	item := NewSystemNoticeItem(&sty, msg)

	first := item.Render(80)
	second := item.Render(80)
	require.Equal(t, first, second)
	require.Contains(t, ansi.Strip(first), "Agent switched.")
}
