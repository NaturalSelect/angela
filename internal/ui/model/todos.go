package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// hasIncompleteTodos returns true if there are any non-completed todos.
func hasIncompleteTodos(todos []session.Todo) bool {
	return session.HasIncompleteTodos(todos)
}

// hasInProgressTodo returns true if there is at least one in-progress todo.
func hasInProgressTodo(todos []session.Todo) bool {
	for _, todo := range todos {
		if todo.Status == session.TodoStatusInProgress {
			return true
		}
	}
	return false
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
