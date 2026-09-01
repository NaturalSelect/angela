package chat

import (
	"encoding/xml"
	"image"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
)

// Geometry of the user message band. The band is a filled surface, so these
// are cell offsets into it rather than padding on a string.
//
//	[gap 1][prompt 2][text ...][timestamp 10][pad 2]
const (
	userBandGap            = 1
	userBandPromptWidth    = 2
	userBandTimestampWidth = 10
	userBandPadRight       = 2
	// userBandPadY is the blank row above and below the text. It belongs to
	// the band because list gap rows are empty strings and cannot carry a
	// background.
	userBandPadY    = 1
	userPromptGlyph = "❯ "
)

// userBandTextX is the column where the message text starts.
const userBandTextX = userBandGap + userBandPromptWidth

// userBandTextWidth is how much room the text gets once the left gap,
// prompt, reserved timestamp columns and right padding are taken out.
func userBandTextWidth(width int) int {
	return max(width-userBandTextX-userBandTimestampWidth-userBandPadRight, 1)
}

// skillInvocation represents the XML structure for a loaded skill.
type skillInvocation struct {
	Name         string `xml:"name"`
	Description  string `xml:"description"`
	Location     string `xml:"location"`
	Instructions string `xml:"instructions"`
}

// UserMessageItem represents a user message in the chat UI.
type UserMessageItem struct {
	*list.Versioned
	*highlightableMessageItem
	*cachedMessageItem
	*focusableMessageItem

	attachments *attachments.Renderer
	message     *message.Message
	sty         *styles.Styles
}

// NewUserMessageItem creates a new UserMessageItem.
func NewUserMessageItem(sty *styles.Styles, message *message.Message, attachments *attachments.Renderer) MessageItem {
	v := list.NewVersioned()
	return &UserMessageItem{
		Versioned:                v,
		highlightableMessageItem: defaultHighlighter(sty, v),
		cachedMessageItem:        &cachedMessageItem{},
		focusableMessageItem:     newFocusableMessageItem(v),
		attachments:              attachments,
		message:                  message,
		sty:                      sty,
	}
}

// Finished implements list.Item. User messages are immutable once
// submitted, so the entry is always safe to freeze.
func (m *UserMessageItem) Finished() bool {
	return true
}

// RawRender implements [MessageItem].
func (m *UserMessageItem) RawRender(width int) string {
	return m.renderBody(cappedMessageWidth(width))
}

// renderBody renders the message text at exactly the given width. The band
// needs a narrower width than RawRender's default cap, so the width is a
// parameter rather than derived here.
func (m *UserMessageItem) renderBody(width int) string {
	content, height, ok := m.getCachedRender(width)
	// cache hit
	if ok {
		return m.renderHighlighted(content, width, height)
	}

	msgContent := strings.TrimSpace(m.message.Content().Text)

	// Check if this is a skill invocation (loaded_skill XML)
	if strings.HasPrefix(msgContent, "<loaded_skill>") {
		content = m.renderSkillInvocation(msgContent, width)
		height = lipgloss.Height(content)
		m.setCachedRender(content, width, height)
		return m.renderHighlighted(content, width, height)
	}

	renderer := common.MarkdownRenderer(m.sty, width)
	mu := common.LockMarkdownRenderer(renderer)

	mu.Lock()
	result, err := renderer.Render(msgContent)
	mu.Unlock()

	if err != nil {
		content = msgContent
	} else {
		content = strings.TrimSuffix(result, "\n")
	}

	if len(m.message.BinaryContent()) > 0 {
		attachmentsStr := m.renderAttachments(width)
		if content == "" {
			content = attachmentsStr
		} else {
			content = strings.Join([]string{content, "", attachmentsStr}, "\n")
		}
	}

	height = lipgloss.Height(content)
	m.setCachedRender(content, width, height)
	return m.renderHighlighted(content, width, height)
}

// renderSkillInvocation renders a loaded_skill XML as a special UI element.
func (m *UserMessageItem) renderSkillInvocation(content string, width int) string {
	var skill skillInvocation
	if err := xml.Unmarshal([]byte(content), &skill); err != nil {
		// If parsing fails, just render as markdown
		renderer := common.MarkdownRenderer(m.sty, width)
		mu := common.LockMarkdownRenderer(renderer)

		mu.Lock()
		result, err := renderer.Render(content)
		mu.Unlock()

		if err != nil {
			return content
		}
		return strings.TrimSuffix(result, "\n")
	}

	return toolOutputSkillContent(m.sty, skill.Name, skill.Description)
}

// Render implements MessageItem.
func (m *UserMessageItem) Render(width int) string {
	// Bypass the prefix cache while a highlight range is active so
	// selection drags reflect immediately without invalidating the
	// cache. Highlight changes are intentionally applied "above" the
	// prefix cache.
	useCache := !m.isHighlighted()
	var key uint64
	if m.focused {
		key = 1
	}
	if useCache {
		if cached, ok := m.getCachedPrefixedRender(width, key); ok {
			return cached
		}
	}
	out := m.renderBand(width)
	if useCache {
		m.setCachedPrefixedRender(out, width, key)
	}
	return out
}

// renderBand paints the message onto a filled surface: the prompt glyph, the
// text, and a timestamp in reserved right-hand columns. The text is wrapped
// short of those columns, so it can never collide with them.
func (m *UserMessageItem) renderBand(width int) string {
	if width <= 0 {
		return ""
	}

	textWidth := userBandTextWidth(width)
	lines := strings.Split(m.renderBody(textWidth), "\n")
	height := len(lines) + 2*userBandPadY

	base := list.ToStyle(m.sty.Messages.UserBand)
	buf := uv.NewScreenBuffer(width, height)
	common.FillRect(&buf, buf.Bounds(), base)

	// The body keeps its own markdown colors, so it is drawn rather than
	// spanned — only the cells it leaves without a background inherit the
	// fill.
	body := strings.Join(lines, "\n")
	common.DrawOnSurface(&buf, image.Rect(
		userBandTextX, userBandPadY,
		userBandTextX+textWidth, userBandPadY+len(lines),
	), base, body)

	common.SetSpan(&buf, userBandGap, userBandPadY,
		list.ToStyle(m.sty.Messages.UserBandPrompt), userPromptGlyph)

	if ts := m.timestamp(); ts != "" {
		x := width - userBandPadRight - buf.WidthMethod().StringWidth(ts)
		common.SetSpan(&buf, x, userBandPadY,
			list.ToStyle(m.sty.Messages.UserBandTimestamp), ts)
	}

	return buf.Render()
}

// timestamp is the wall-clock time the message was sent, or "" when the
// message predates timestamping.
func (m *UserMessageItem) timestamp() string {
	if m.message.CreatedAt <= 0 {
		return ""
	}
	return time.Unix(m.message.CreatedAt, 0).Format("3:04 PM")
}

// ID implements MessageItem.
func (m *UserMessageItem) ID() string {
	return m.message.ID
}

// renderAttachments renders attachments.
func (m *UserMessageItem) renderAttachments(width int) string {
	var attachments []message.Attachment
	for _, at := range m.message.BinaryContent() {
		attachments = append(attachments, message.Attachment{
			FileName: at.Path,
			MimeType: at.MIMEType,
		})
	}
	// This message is already posted, so the attachment can't be removed;
	// don't render the remove button.
	return m.attachments.Render(attachments, false, false, width)
}

// HandleKeyEvent implements KeyEventHandler.
func (m *UserMessageItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	if k := key.String(); k == "c" || k == "y" {
		text := m.message.Content().Text
		return true, common.CopyToClipboard(text, "Message copied to clipboard")
	}
	return false, nil
}
