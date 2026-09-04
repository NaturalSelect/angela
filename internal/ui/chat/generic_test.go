package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestGenericToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{"foo":"bar"}`}
	item := NewGenericToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Custom Tool")
}

func TestGenericToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `not-json`, Finished: true}
	item := NewGenericToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestGenericToolHeaderShowsParamsJSON(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{"foo":"bar"}`, Finished: true}
	item := NewGenericToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "foo")
	require.Contains(t, out, "bar")
}

func TestGenericToolNoParamsOmitsParamList(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{}`, Finished: true}
	item := NewGenericToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Custom Tool")
}

func TestGenericToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "g1", Content: "distinctive generic output"}
	item := NewGenericToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive generic output")
}

func TestGenericToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "g1", Content: ""}
	item := NewGenericToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Custom Tool")
}

func TestGenericToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "g1", Content: "distinctive generic output"}
	item := NewGenericToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive generic output")
}

func TestGenericToolExpandedShowsTextBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "g1", Content: "distinctive generic output"}
	item := NewGenericToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive generic output")
}

func TestGenericToolExpandedShowsImageBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "g1", Name: "custom_tool", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "g1", Content: "img", Data: "aGVsbG8=", MIMEType: "image/png"}
	item := NewGenericToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := item.Render(100)
	require.NotEmpty(t, ansi.Strip(out))
}
