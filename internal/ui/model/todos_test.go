package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestShowTodos_NoSessionIsNoOp(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.session = nil

	cmd := m.showTodos()
	require.Nil(t, cmd)
	require.Equal(t, 0, m.chat.Len())
}

// TestShowTodos_AppendsFormattedList verifies the notice carries every
// todo, using the active form for the in-progress one, matching what
// chat.FormatTodosList renders in the session details panel.
func TestShowTodos_AppendsFormattedList(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.session = &session.Session{
		ID: "s1",
		Todos: []session.Todo{
			{Content: "Task 1", Status: session.TodoStatusCompleted},
			{Content: "Task 2", Status: session.TodoStatusInProgress, ActiveForm: "Working on Task 2"},
			{Content: "Task 3", Status: session.TodoStatusPending},
		},
	}

	m.showTodos()
	require.Equal(t, 1, m.chat.Len())

	item, ok := m.chat.list.ItemAt(0).(*chat.SystemNoticeItem)
	require.True(t, ok, "showTodos must append a *chat.SystemNoticeItem")

	out := ansi.Strip(item.Render(120))
	require.Contains(t, out, "Task 1")
	require.Contains(t, out, "Working on Task 2", "in-progress todos show their active form")
	require.Contains(t, out, "Task 3")
}

func TestShowTodos_EmptyTodosShowsPlaceholder(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.session = &session.Session{ID: "s1"}

	m.showTodos()
	require.Equal(t, 1, m.chat.Len())

	item, ok := m.chat.list.ItemAt(0).(*chat.SystemNoticeItem)
	require.True(t, ok)
	require.Contains(t, ansi.Strip(item.Render(120)), "No todos for this session yet.")
}

// TestShowTodos_RepeatedCallsAppendDistinctNotices pins the behavior
// documented on showTodos: unlike askSideQuestion's answer, which
// completes an existing pending item, each todo snapshot is its own
// new notice, so running the command again grows the transcript
// instead of replacing the last one.
func TestShowTodos_RepeatedCallsAppendDistinctNotices(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.session = &session.Session{
		ID:    "s1",
		Todos: []session.Todo{{Content: "Task 1", Status: session.TodoStatusPending}},
	}

	m.showTodos()
	m.showTodos()
	require.Equal(t, 2, m.chat.Len())

	first, ok := m.chat.list.ItemAt(0).(*chat.SystemNoticeItem)
	require.True(t, ok)
	second, ok := m.chat.list.ItemAt(1).(*chat.SystemNoticeItem)
	require.True(t, ok)
	require.NotEqual(t, first.ID(), second.ID())
}
