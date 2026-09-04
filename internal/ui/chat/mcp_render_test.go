package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestMCPToolInvalidNameShowsError(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "onlyoneseg", Input: `{}`, Finished: true}
	item := NewMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid tool name")
}

func TestMCPToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `{"query":"bug"}`}
	item := NewMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Github")
	require.Contains(t, out, "Search Issues")
}

func TestMCPToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `not-json`, Finished: true}
	item := NewMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestMCPToolHeaderShowsMCPAndToolNames(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `{"query":"bug"}`, Finished: true}
	item := NewMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Github")
	require.Contains(t, out, "Search Issues")
	require.Contains(t, out, "query")
	require.Contains(t, out, "bug")
}

func TestMCPToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "m1", Content: "distinctive mcp result"}
	item := NewMCPToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive mcp result")
}

func TestMCPToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "m1", Content: ""}
	item := NewMCPToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Search Issues")
}

func TestMCPToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "m1", Content: "distinctive mcp result"}
	item := NewMCPToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive mcp result")
}

func TestMCPToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "m1", Name: toolnames.MCPPrefix + "github_search_issues", Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "m1", Content: "distinctive mcp result"}
	item := NewMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive mcp result")
}
