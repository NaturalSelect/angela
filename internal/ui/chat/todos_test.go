package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTodosToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "todo-1", Name: toolnames.Todos, Input: `{"todos":[]}`, Finished: false}
	item := NewTodosToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "To-Do")
}

func TestTodosToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "todo-1", Name: toolnames.Todos, Input: `{"todos":[]}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "todo-1", Content: "updated"}
	item := NewTodosToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "To-Do")
}

// Before any metadata is available (e.g. the finished result has not
// arrived), the ratio and the active task name are derived directly
// from the call's own params.
func TestTodosToolRenderContext_ParamsOnlyShowsRatioAndActiveTask(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	input, err := json.Marshal(tools.TodosParams{Todos: []tools.TodoItem{
		{Content: "write code", Status: "completed"},
		{Content: "run tests", Status: "in_progress", ActiveForm: "Running tests"},
		{Content: "ship it", Status: "pending"},
	}})
	require.NoError(t, err)

	opts := &ToolRenderOpts{
		ToolCall: message.ToolCall{ID: "todo-1", Name: toolnames.Todos, Input: string(input), Finished: true},
		Status:   ToolStatusSuccess,
	}
	ctx := &TodosToolRenderContext{}
	out := ansi.Strip(ctx.RenderTool(&sty, 100, opts))

	require.Contains(t, out, "1/3")
	require.Contains(t, out, "Running tests")
}

func TestTodosToolRenderContext_MetadataVariants(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	ctx := &TodosToolRenderContext{}

	baseInput, err := json.Marshal(tools.TodosParams{Todos: []tools.TodoItem{
		{Content: "a", Status: "pending"},
	}})
	require.NoError(t, err)

	newOpts := func(meta tools.TodosResponseMetadata) *ToolRenderOpts {
		metaJSON, merr := json.Marshal(meta)
		require.NoError(t, merr)
		return &ToolRenderOpts{
			ToolCall: message.ToolCall{ID: "todo-1", Name: toolnames.Todos, Input: string(baseInput), Finished: true},
			Result:   &message.ToolResult{ToolCallID: "todo-1", Content: "updated", Metadata: string(metaJSON)},
			Status:   ToolStatusSuccess,
		}
	}

	t.Run("new list with a first task starting", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			IsNew: true, Total: 3, JustStarted: "write code",
			Todos: []session.Todo{{Content: "write code", Status: session.TodoStatusInProgress, ActiveForm: "Writing code"}},
		})))
		require.Contains(t, out, "created 3 todos, starting first")
	})

	t.Run("new list with nothing started yet", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			IsNew: true, Total: 2,
			Todos: []session.Todo{{Content: "a", Status: session.TodoStatusPending}},
		})))
		require.Contains(t, out, "created 2 todos")
		require.NotContains(t, out, "starting first")
	})

	t.Run("completed one and started the next", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			Completed: 1, Total: 3,
			JustCompleted: []string{"write code"},
			JustStarted:   "run tests",
			Todos: []session.Todo{
				{Content: "write code", Status: session.TodoStatusCompleted},
				{Content: "run tests", Status: session.TodoStatusInProgress},
			},
		})))
		require.Contains(t, out, "1/3")
		require.Contains(t, out, "completed 1, starting next")
	})

	t.Run("completed all", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			Completed: 2, Total: 2,
			JustCompleted: []string{"run tests"},
			Todos: []session.Todo{
				{Content: "write code", Status: session.TodoStatusCompleted},
				{Content: "run tests", Status: session.TodoStatusCompleted},
			},
		})))
		require.Contains(t, out, "completed all")
	})

	t.Run("completed some but not all, none started", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			Completed: 1, Total: 3,
			JustCompleted: []string{"write code"},
			Todos: []session.Todo{
				{Content: "write code", Status: session.TodoStatusCompleted},
				{Content: "run tests", Status: session.TodoStatusPending},
			},
		})))
		require.Contains(t, out, "completed 1")
		require.NotContains(t, out, "starting")
	})

	t.Run("started with nothing completed", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			Completed: 0, Total: 2,
			JustStarted: "run tests",
			Todos: []session.Todo{
				{Content: "run tests", Status: session.TodoStatusInProgress},
			},
		})))
		require.Contains(t, out, "starting task")
	})

	t.Run("no change at all falls back to bare ratio", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(ctx.RenderTool(&sty, 100, newOpts(tools.TodosResponseMetadata{
			Completed: 1, Total: 2,
			Todos: []session.Todo{
				{Content: "write code", Status: session.TodoStatusCompleted},
				{Content: "run tests", Status: session.TodoStatusPending},
			},
		})))
		require.Contains(t, out, "1/2")
	})
}

func TestFormatTodosList(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, FormatTodosList(&sty, nil, styles.ArrowRightIcon, 80))
	})

	t.Run("sorts completed, then in-progress, then pending", func(t *testing.T) {
		t.Parallel()
		todos := []session.Todo{
			{Content: "pending task", Status: session.TodoStatusPending},
			{Content: "write code", Status: session.TodoStatusCompleted},
			{Content: "run tests", Status: session.TodoStatusInProgress, ActiveForm: "Running tests"},
		}
		out := ansi.Strip(FormatTodosList(&sty, todos, styles.ArrowRightIcon, 80))

		completedIdx := strings.Index(out, "write code")
		inProgressIdx := strings.Index(out, "Running tests")
		pendingIdx := strings.Index(out, "pending task")
		require.True(t, completedIdx >= 0 && inProgressIdx >= 0 && pendingIdx >= 0)
		require.Less(t, completedIdx, inProgressIdx, "completed todos must render before in-progress ones")
		require.Less(t, inProgressIdx, pendingIdx, "in-progress todos must render before pending ones")
	})

	t.Run("original slice is left untouched", func(t *testing.T) {
		t.Parallel()
		todos := []session.Todo{
			{Content: "b", Status: session.TodoStatusPending},
			{Content: "a", Status: session.TodoStatusCompleted},
		}
		_ = FormatTodosList(&sty, todos, styles.ArrowRightIcon, 80)
		require.Equal(t, "b", todos[0].Content, "FormatTodosList must sort a copy, not the caller's slice")
	})

	t.Run("truncates long lines to width", func(t *testing.T) {
		t.Parallel()
		todos := []session.Todo{
			{Content: "a very long todo description that will not fit in a narrow column", Status: session.TodoStatusPending},
		}
		out := FormatTodosList(&sty, todos, styles.ArrowRightIcon, 15)
		require.LessOrEqual(t, ansi.StringWidth(ansi.Strip(out)), 15)
	})
}
