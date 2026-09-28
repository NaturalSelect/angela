package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/websearch"
	"github.com/stretchr/testify/require"
)

// stubEngine is a minimal websearch.Engine for driving NewWebSearchTool
// in tests without a network round trip.
type stubEngine struct {
	id         string
	sources    []websearch.Source
	err        error
	supportsTR bool
}

func (e *stubEngine) ID() string              { return e.id }
func (e *stubEngine) SupportsTimeRange() bool { return e.supportsTR }
func (e *stubEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.sources, nil
}

func newTestRouter(t *testing.T, engine *stubEngine) *websearch.Router {
	t.Helper()
	router, err := websearch.NewRouter([]websearch.Engine{engine}, engine.id, 0)
	require.NoError(t, err)
	return router
}

func TestNewWebSearchToolRequiresQuery(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(newTestRouter(t, &stubEngine{id: "bing"}))
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

	tool := NewWebSearchTool(newTestRouter(t, &stubEngine{id: "bing"}))
	input, err := json.Marshal(WebSearchParams{Query: "golang"})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "session ID is required")
}

// TestNewWebSearchToolReturnsFormattedResults drives the tool wrapper
// end-to-end against a stub engine, confirming the wrapper wires
// params, session validation, and result formatting together
// correctly.
func TestNewWebSearchToolReturnsFormattedResults(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(newTestRouter(t, &stubEngine{
		id: "bing",
		sources: []websearch.Source{
			{URL: "https://example.com/post", Title: "Example Post", Snippet: "A snippet about the example post."},
		},
	}))
	input, err := json.Marshal(WebSearchParams{Query: "example", MaxResults: 5})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Example Post")
	require.Contains(t, resp.Content, "https://example.com/post")
	require.Contains(t, resp.Content, "Engine: bing")
}

func TestNewWebSearchToolReportsEngineFailureAsToolError(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(newTestRouter(t, &stubEngine{id: "bing", err: errors.New("rate limited")}))
	input, err := json.Marshal(WebSearchParams{Query: "example"})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Failed to search")
}

func TestNewWebSearchToolRejectsInvalidTimeRange(t *testing.T) {
	t.Parallel()

	tool := NewWebSearchTool(newTestRouter(t, &stubEngine{id: "bing"}))
	input, err := json.Marshal(WebSearchParams{Query: "example", TimeRange: "not a range"})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.WebSearch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "time_range")
}
