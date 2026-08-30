package chat

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// The user band paints onto a cell surface, where a wide rune occupies two
// columns. Post-processing those columns used to blank the rune out, so a
// message in Chinese arrived on screen with only its ASCII words left.
func TestUserMessageItemRender_KeepsWideRunes(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:        "u1",
		Role:      message.User,
		CreatedAt: time.Now().Unix(),
		Parts: []message.ContentPart{
			message.TextContent{Text: "帮我看看 opencode 和 angela 的区别"},
		},
	}
	item := NewUserMessageItem(&sty, msg, nil)

	out := item.Render(80)

	require.Contains(t, out, "帮我看看")
	require.Contains(t, out, "的区别")
	require.Contains(t, out, "opencode")
	require.Contains(t, out, "angela")
	require.Equal(t, 80, maxRenderedWidth(out), "band lost or gained columns")
}

// maxRenderedWidth is the widest visible line in a rendered band.
func maxRenderedWidth(rendered string) int {
	w := 0
	for line := range strings.SplitSeq(rendered, "\n") {
		w = max(w, lipgloss.Width(line))
	}
	return w
}
