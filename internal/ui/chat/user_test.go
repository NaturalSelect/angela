package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
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

// RawRender is used by callers that need the message content without the
// surrounding band; it must still surface the message text.
func TestUserMessageItem_RawRender(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "u-raw",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello raw render"},
		},
	}
	item := NewUserMessageItem(&sty, msg, nil)
	require.Contains(t, ansi.Strip(item.RawRender(80)), "hello raw render")
}

func TestUserMessageItem_ID(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{ID: "u-id-1", Role: message.User}
	item := NewUserMessageItem(&sty, msg, nil)
	require.Equal(t, "u-id-1", item.ID())
}

// A loaded_skill payload is a structured notice, not prose: it must render
// through the skill indicator rather than as plain markdown text.
func TestUserMessageItem_RenderSkillInvocation(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("valid_xml_shows_skill_indicator", func(t *testing.T) {
		t.Parallel()
		xmlContent := `<loaded_skill><name>jq-helper</name>` +
			`<description>Query JSON with jq</description>` +
			`<location>/skills/jq</location><instructions>Use jq.</instructions></loaded_skill>`
		msg := &message.Message{
			ID:   "u-skill-1",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: xmlContent},
			},
		}
		item := NewUserMessageItem(&sty, msg, nil)
		out := ansi.Strip(item.Render(100))
		require.Contains(t, out, "Loaded Skill")
		require.Contains(t, out, "jq-helper")
		require.Contains(t, out, "Query JSON with jq")
	})

	t.Run("malformed_xml_falls_back_to_markdown", func(t *testing.T) {
		t.Parallel()
		xmlContent := `<loaded_skill>not valid <xml`
		msg := &message.Message{
			ID:   "u-skill-2",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: xmlContent},
			},
		}
		item := NewUserMessageItem(&sty, msg, nil)
		out := ansi.Strip(item.Render(100))
		require.Contains(t, out, "not valid")
		require.NotContains(t, out, "Loaded Skill")
	})
}

// Attachments on an already-posted message must render inline, both when
// they follow text and when the message is attachments-only.
func TestUserMessageItem_RenderAttachments(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	renderer := attachments.NewRenderer(
		lipgloss.Style{}, lipgloss.Style{}, lipgloss.Style{},
		lipgloss.Style{}, lipgloss.Style{}, lipgloss.Style{},
	)

	t.Run("text_and_attachment", func(t *testing.T) {
		t.Parallel()
		msg := &message.Message{
			ID:   "u-att-1",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "see attached"},
				message.BinaryContent{Path: "/tmp/photo.png", MIMEType: "image/png", Data: []byte("fake")},
			},
		}
		item := NewUserMessageItem(&sty, msg, renderer)
		out := ansi.Strip(item.Render(100))
		require.Contains(t, out, "see attached")
		require.Contains(t, out, "photo.png")
	})

	t.Run("attachment_only", func(t *testing.T) {
		t.Parallel()
		msg := &message.Message{
			ID:   "u-att-2",
			Role: message.User,
			Parts: []message.ContentPart{
				message.BinaryContent{Path: "/tmp/doc.txt", MIMEType: "text/plain", Data: []byte("fake")},
			},
		}
		item := NewUserMessageItem(&sty, msg, renderer)
		out := ansi.Strip(item.Render(100))
		require.Contains(t, out, "doc.txt")
	})
}

func TestUserMessageItem_HandleKeyEvent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "u-key-1",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "copy me"},
		},
	}
	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)

	for _, k := range []string{"c", "y"} {
		handled, cmd := item.HandleKeyEvent(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		require.True(t, handled)
		require.NotNil(t, cmd)
	}

	handled, cmd := item.HandleKeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, handled)
	require.Nil(t, cmd)
}
