package model

import (
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// hasIncompleteTodos returns true if there are any non-completed todos.
func hasIncompleteTodos(todos []session.Todo) bool {
	return session.HasIncompleteTodos(todos)
}

// todosInfo renders the session todo list as a details column. Like the other
// detail sections it renders from the memoized session snapshot only and must
// not probe the workspace; see lspInfo for why.
func (m *UI) todosInfo(width, maxItems int, isSection bool) string {
	t := m.com.Styles

	title := t.Resource.Heading.Render("Todos")
	if isSection {
		title = common.Section(t, title, width)
	}

	list := t.Resource.AdditionalText.Render("None")
	if m.session != nil && len(m.session.Todos) > 0 {
		list = capLines(
			t,
			chat.FormatTodosList(t, m.session.Todos, styles.TodoInProgressIcon, width),
			maxItems,
		)
	}

	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, list))
}

// capLines caps a pre-rendered block at maxItems lines, replacing the overflow
// with the same "…and N more" tail the other detail sections use.
func capLines(t *styles.Styles, block string, maxItems int) string {
	lines := strings.Split(block, "\n")
	if maxItems < 1 || len(lines) <= maxItems {
		return block
	}
	visible := lines[:maxItems-1]
	remaining := len(lines) - len(visible)
	visible = append(visible, t.Resource.AdditionalText.Render(fmt.Sprintf("…and %d more", remaining)))
	return lipgloss.JoinVertical(lipgloss.Left, visible...)
}

// todosNoticeSeq provides unique IDs for todo-list notices, mirroring
// sideQuestionSeq, so running the command more than once in the same
// session does not collide with the previous notice's item ID.
var todosNoticeSeq atomic.Int64

// todosNoticeWidth caps how wide a todo line renders before truncating.
// It mirrors chat's unexported maxTextWidth: SystemNoticeItem's text is
// fixed at construction, so the width has to be picked up front rather
// than at render time.
const todosNoticeWidth = 120

// showTodos appends the current session's todo list to the chat as an
// in-memory notice. Like askSideQuestion, the snapshot lives only in
// this view and is never written to the session's message history, so
// running the command again does not clutter the transcript on reload.
func (m *UI) showTodos() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	t := m.com.Styles
	text := chat.FormatTodosList(t, m.session.Todos, styles.TodoInProgressIcon, todosNoticeWidth)
	if text == "" {
		text = t.Resource.AdditionalText.Render("No todos for this session yet.")
	}

	item := chat.NewSystemNoticeItem(t, &message.Message{
		ID:   fmt.Sprintf("todos-notice-%d", todosNoticeSeq.Add(1)),
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: text},
		},
	})
	m.chat.AppendMessages(item)
	return m.chat.ScrollToBottomAndAnimate()
}
