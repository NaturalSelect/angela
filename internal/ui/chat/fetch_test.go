package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Fetch Tool
// -----------------------------------------------------------------------------

func TestFetchToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "f1", Name: toolnames.Fetch, Input: `{"url":"https://example.com","format":"markdown"}`}
	item := NewFetchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Fetch)
}

func TestFetchToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "f1", Name: toolnames.Fetch, Input: `not-json`, Finished: true}
	item := NewFetchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestFetchToolHeaderShowsURLFormatAndTimeout(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "f1",
		Name:     toolnames.Fetch,
		Input:    `{"url":"https://example.com","format":"text","timeout":30}`,
		Finished: true,
	}
	item := NewFetchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "https://example.com")
	require.Contains(t, out, "format")
	require.Contains(t, out, "text")
	require.Contains(t, out, "timeout")
	require.Contains(t, out, "30s")
}

func TestFetchToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "f1", Name: toolnames.Fetch, Input: `{"url":"https://example.com","format":"markdown"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "f1", Content: "# distinctive fetched markdown"}
	item := NewFetchToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive fetched markdown")
}

func TestFetchToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "f1", Name: toolnames.Fetch, Input: `{"url":"https://example.com","format":"markdown"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "f1", Content: ""}
	item := NewFetchToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Fetch)
}

func TestFetchToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "f1", Name: toolnames.Fetch, Input: `{"url":"https://example.com","format":"markdown"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "f1", Content: "# distinctive fetched markdown"}
	item := NewFetchToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive fetched markdown")
}

func TestFetchToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "f1", Name: toolnames.Fetch, Input: `{"url":"https://example.com","format":"markdown"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "f1", Content: "distinctive fetched markdown"}
	item := NewFetchToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive fetched markdown")
}

func TestGetFileExtensionForFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format string
		want   string
	}{
		{format: "text", want: "fetch.txt"},
		{format: "html", want: "fetch.html"},
		{format: "markdown", want: "fetch.md"},
		{format: "", want: "fetch.md"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, getFileExtensionForFormat(tt.format))
		})
	}
}

// -----------------------------------------------------------------------------
// WebFetch Tool
// -----------------------------------------------------------------------------

func TestWebFetchToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "wf1", Name: toolnames.WebFetch, Input: `{"url":"https://example.com"}`}
	item := NewWebFetchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Fetch)
}

func TestWebFetchToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "wf1", Name: toolnames.WebFetch, Input: `not-json`, Finished: true}
	item := NewWebFetchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestWebFetchToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "wf1", Name: toolnames.WebFetch, Input: `{"url":"https://example.com"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "wf1", Content: "distinctive web fetch body"}
	item := NewWebFetchToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive web fetch body")
}

func TestWebFetchToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "wf1", Name: toolnames.WebFetch, Input: `{"url":"https://example.com"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "wf1", Content: ""}
	item := NewWebFetchToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "https://example.com")
}

func TestWebFetchToolExpandedShowsMarkdownBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "wf1", Name: toolnames.WebFetch, Input: `{"url":"https://example.com"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "wf1", Content: "distinctive web fetch body"}
	item := NewWebFetchToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive web fetch body")
}

// -----------------------------------------------------------------------------
// WebSearch Tool
// -----------------------------------------------------------------------------

func TestWebSearchToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `{"query":"golang"}`}
	item := NewWebSearchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Search")
}

func TestWebSearchToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `not-json`, Finished: true}
	item := NewWebSearchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestWebSearchToolHeaderShowsQuery(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `{"query":"golang generics"}`, Finished: true}
	item := NewWebSearchToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "golang generics")
}

func TestWebSearchToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `{"query":"golang"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ws1", Content: "distinctive search results"}
	item := NewWebSearchToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive search results")
}

func TestWebSearchToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `{"query":"golang"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ws1", Content: ""}
	item := NewWebSearchToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "golang")
}

func TestWebSearchToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `{"query":"golang"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ws1", Content: "distinctive search results"}
	item := NewWebSearchToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive search results")
}

func TestWebSearchToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ws1", Name: toolnames.WebSearch, Input: `{"query":"golang"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ws1", Content: "distinctive search results"}
	item := NewWebSearchToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive search results")
}
