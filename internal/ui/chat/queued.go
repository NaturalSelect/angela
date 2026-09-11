package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// queuedMarker labels a prompt that has been accepted but not yet sent.
const queuedMarker = "⋯ queued"

// QueuedMessageItem renders a prompt that is waiting for the agent to
// finish the current turn. It is not backed by a stored message: the
// queue lives in the agent's run state and is replaced wholesale
// whenever it changes, so the item carries only its text, attachments,
// and position.
type QueuedMessageItem struct {
	*list.Versioned

	prompt      string
	atts        []message.Attachment
	attachments *attachments.Renderer
	index       int
	sty         *styles.Styles
}

// NewQueuedMessageItem creates a chat entry for a prompt still waiting in
// the queue. index is its position, used only to give the item a stable
// identity within the current queue.
func NewQueuedMessageItem(sty *styles.Styles, prompt string, atts []message.Attachment, index int) MessageItem {
	return &QueuedMessageItem{
		Versioned: list.NewVersioned(),
		prompt:    prompt,
		atts:      atts,
		attachments: attachments.NewRenderer(
			sty.Attachments.Normal,
			sty.Attachments.Deleting,
			sty.Attachments.Image,
			sty.Attachments.Text,
			sty.Attachments.Skill,
			sty.Attachments.Remove,
		),
		index: index,
		sty:   sty,
	}
}

// ID implements Identifiable.
func (m *QueuedMessageItem) ID() string {
	return fmt.Sprintf("queued-%d", m.index)
}

// Finished implements list.Item. A queued prompt never changes in place;
// the whole tail is rebuilt when the queue moves.
func (m *QueuedMessageItem) Finished() bool {
	return true
}

// RawRender implements MessageItem.
func (m *QueuedMessageItem) RawRender(width int) string {
	return m.render(width)
}

// Render implements list.Item. Queued entries get the same left gutter as
// every other blurred message in the transcript (assistant replies, tool
// calls) so they line up instead of sitting flush against the edge.
func (m *QueuedMessageItem) Render(width int) string {
	content := m.render(width)
	if content == "" {
		return content
	}
	prefix := m.sty.Messages.AssistantBlurred.Render()
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

func (m *QueuedMessageItem) render(width int) string {
	width = cappedMessageWidth(width)
	if width <= 0 {
		return ""
	}

	text := strings.TrimSpace(m.prompt)
	if text == "" && len(m.atts) == 0 {
		return ""
	}

	var body string
	if text != "" {
		body = m.sty.Messages.QueuedText.Width(width).Render(text)
	}
	if len(m.atts) > 0 {
		attachmentsStr := m.attachments.Render(m.atts, false, false, width)
		if body == "" {
			body = attachmentsStr
		} else {
			body = strings.Join([]string{body, "", attachmentsStr}, "\n")
		}
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.sty.Messages.QueuedMarker.Render(queuedMarker),
		body,
	)
}
