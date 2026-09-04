package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestNewWebSearchToolRequiresQuery(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(WebSearchParams{})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "query is required")
}

func TestNewWebSearchToolRequiresSessionID(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(WebSearchParams{Query: "golang"})
	require.NoError(t, err)

	_, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
}

// TestNewWebSearchToolReturnsFormattedResults drives the tool wrapper
// end-to-end against the same DuckDuckGo Lite stub search_test.go uses
// for searchDuckDuckGo directly, confirming the wrapper wires params,
// session validation, and result formatting together correctly.
func TestNewWebSearchToolReturnsFormattedResults(t *testing.T) {
	page := `<html><body><table>
<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpost">Example Post</a></td></tr>
<tr><td class="result-snippet">A snippet about the example post.</td></tr>
</table></body></html>`
	serveSearchStub(t, http.StatusOK, page)

	tool := NewWebSearchTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(WebSearchParams{Query: "example", MaxResults: 5})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Example Post")
	require.Contains(t, resp.Content, "https://example.com/post")
}

func TestNewWebSearchToolReportsRateLimitAsToolError(t *testing.T) {
	serveSearchStub(t, http.StatusAccepted, loadAnomalyPage(t))

	tool := NewWebSearchTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(WebSearchParams{Query: "example"})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Failed to search")
}
