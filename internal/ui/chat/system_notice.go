package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// SystemNoticeItem renders a transcript-only notice — a System-role
// message such as an agent switch. The model never sees these (they are
// dropped from the LLM context), so the chat is the only place they
// exist for the user.
type SystemNoticeItem struct {
	*list.Versioned
	*cachedMessageItem

	id   string
	text string
	sty  *styles.Styles
}

var _ MessageItem = (*SystemNoticeItem)(nil)

// NewSystemNoticeItem creates a notice item from a System-role message.
func NewSystemNoticeItem(sty *styles.Styles, msg *message.Message) MessageItem {
	return &SystemNoticeItem{
		Versioned:         list.NewVersioned(),
		cachedMessageItem: &cachedMessageItem{},
		id:                fmt.Sprintf("%s:system-notice", msg.ID),
		text:              msg.Content().Text,
		sty:               sty,
	}
}

// Finished implements list.Item. The text is fixed at construction.
func (s *SystemNoticeItem) Finished() bool {
	return true
}

// ID implements MessageItem.
func (s *SystemNoticeItem) ID() string {
	return s.id
}

// RawRender implements MessageItem.
func (s *SystemNoticeItem) RawRender(width int) string {
	innerWidth := max(0, width-MessageLeftPaddingTotal)
	content, _, ok := s.getCachedRender(innerWidth)
	if !ok {
		content = common.Section(s.sty,
			s.sty.Messages.AssistantInfoDuration.Render(s.text), innerWidth)
		s.setCachedRender(content, innerWidth, lipgloss.Height(content))
	}
	return content
}

// Render implements MessageItem.
func (s *SystemNoticeItem) Render(width int) string {
	if cached, ok := s.getCachedPrefixedRender(width, 0); ok {
		return cached
	}
	prefix := s.sty.Messages.SectionHeader.Render()
	lines := strings.Split(s.RawRender(width), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	out := strings.Join(lines, "\n")
	s.setCachedPrefixedRender(out, width, 0)
	return out
}
