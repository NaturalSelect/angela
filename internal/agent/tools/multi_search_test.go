package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/websearch"
	"github.com/stretchr/testify/require"
)

func newMultiTestRouter(t *testing.T, engines ...*stubEngine) *websearch.Router {
	t.Helper()
	all := make([]websearch.Engine, len(engines))
	for i, e := range engines {
		all[i] = e
	}
	router, err := websearch.NewRouter(all, engines[0].id, 0)
	require.NoError(t, err)
	return router
}

func runMultiSearch(t *testing.T, router *websearch.Router, params MultiSearchParams) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-1")
	resp, err := NewMultiSearchTool(router).Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.MultiSearch, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func numberedSources(n int) []websearch.Source {
	sources := make([]websearch.Source, n)
	for i := range sources {
		sources[i] = websearch.Source{URL: fmt.Sprintf("https://example.com/%d", i), Title: fmt.Sprintf("Result %d", i)}
	}
	return sources
}

func TestNewMultiSearchToolRequiresQuery(t *testing.T) {
	t.Parallel()

	resp := runMultiSearch(t, newMultiTestRouter(t, &stubEngine{id: "bing"}), MultiSearchParams{})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "query is required")
}

func TestNewMultiSearchToolRequiresSessionID(t *testing.T) {
	t.Parallel()

	tool := NewMultiSearchTool(newMultiTestRouter(t, &stubEngine{id: "bing"}))
	input, err := json.Marshal(MultiSearchParams{Query: "golang"})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.MultiSearch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "session ID is required")
}

// TestNewMultiSearchToolMergesAndAnnotatesSources drives the wrapper
// end to end: a source two engines agree on is ranked first and lists
// both engines, and the trailing note names the engines that
// contributed.
func TestNewMultiSearchToolMergesAndAnnotatesSources(t *testing.T) {
	t.Parallel()

	router := newMultiTestRouter(t,
		&stubEngine{id: "bing", sources: []websearch.Source{
			{URL: "https://only-bing.example", Title: "Only Bing"},
			{URL: "https://shared.example", Title: "Shared", Snippet: "Agreed on by both."},
		}},
		&stubEngine{id: "ddg", sources: []websearch.Source{
			{URL: "https://shared.example", Title: "Shared"},
		}},
	)

	resp := runMultiSearch(t, router, MultiSearchParams{Query: "example"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "1. Shared [seen in: bing, ddg]")
	require.Contains(t, resp.Content, "2. Only Bing [seen in: bing]")
	require.Contains(t, resp.Content, "Summary: Agreed on by both.")
	require.Contains(t, resp.Content, "Note: used: [bing, ddg]")
}

func TestNewMultiSearchToolReportsFailedAndSkippedEngines(t *testing.T) {
	t.Parallel()

	router := newMultiTestRouter(t,
		&stubEngine{id: "bing", sources: numberedSources(1)},
		&stubEngine{id: "ddg", err: errors.New("rate limited")},
		&stubEngine{id: "exa", err: fmt.Errorf("exa: %w", websearch.ErrNotConfigured)},
	)

	resp := runMultiSearch(t, router, MultiSearchParams{Query: "example"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "used: [bing]")
	require.Contains(t, resp.Content, "failed: [ddg (rate limited)]")
	require.Contains(t, resp.Content, "skipped (no key): [exa]")
}

// TestNewMultiSearchToolAllEnginesFailingIsNotAToolError pins that
// SearchMany reports per-engine failures in the result rather than
// erroring, so the model sees which engines failed and why.
func TestNewMultiSearchToolAllEnginesFailingIsNotAToolError(t *testing.T) {
	t.Parallel()

	router := newMultiTestRouter(t, &stubEngine{id: "bing", err: errors.New("blocked")})

	resp := runMultiSearch(t, router, MultiSearchParams{Query: "example"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "No results found")
	require.Contains(t, resp.Content, "failed: [bing (blocked)]")
}

func TestNewMultiSearchToolRejectsAllUnknownEngines(t *testing.T) {
	t.Parallel()

	router := newMultiTestRouter(t, &stubEngine{id: "bing", sources: numberedSources(1)})

	resp := runMultiSearch(t, router, MultiSearchParams{Query: "example", Engines: []string{"nope", "also-nope"}})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no valid engines selected")
	require.Contains(t, resp.Content, "nope, also-nope")
}

// TestNewMultiSearchToolDropsUnknownEnginesWhenSomeAreValid pins that a
// partly valid engine list is not an error: unknown ids are dropped
// and only the known engines are queried.
func TestNewMultiSearchToolDropsUnknownEnginesWhenSomeAreValid(t *testing.T) {
	t.Parallel()

	router := newMultiTestRouter(t,
		&stubEngine{id: "bing", sources: []websearch.Source{{URL: "https://bing.example", Title: "Bing"}}},
		&stubEngine{id: "ddg", sources: []websearch.Source{{URL: "https://ddg.example", Title: "DDG"}}},
	)

	resp := runMultiSearch(t, router, MultiSearchParams{Query: "example", Engines: []string{"ddg", "nope"}})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "DDG")
	require.NotContains(t, resp.Content, "Bing")
	require.Contains(t, resp.Content, "used: [ddg]")
}

func TestNewMultiSearchToolClampsMaxResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		maxResults int
		want       int
	}{
		{name: "unset defaults to 8", maxResults: 0, want: 8},
		{name: "negative defaults to 8", maxResults: -3, want: 8},
		{name: "within range is kept", maxResults: 5, want: 5},
		{name: "above the cap is clamped to 20", maxResults: 100, want: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newMultiTestRouter(t, &stubEngine{id: "bing", sources: numberedSources(30)})

			resp := runMultiSearch(t, router, MultiSearchParams{Query: "example", MaxResults: tt.maxResults})
			require.False(t, resp.IsError, resp.Content)
			require.Equal(t, tt.want, strings.Count(resp.Content, "   URL: "))
		})
	}
}

func TestFilterKnownEngines(t *testing.T) {
	t.Parallel()

	router := newMultiTestRouter(t, &stubEngine{id: "bing"}, &stubEngine{id: "ddg"})

	tests := []struct {
		name        string
		requested   []string
		wantKnown   []string
		wantUnknown []string
	}{
		{name: "nothing requested", requested: nil},
		{name: "all known", requested: []string{"ddg", "bing"}, wantKnown: []string{"ddg", "bing"}},
		{name: "all unknown", requested: []string{"x", "y"}, wantUnknown: []string{"x", "y"}},
		{name: "mixed", requested: []string{"bing", "x"}, wantKnown: []string{"bing"}, wantUnknown: []string{"x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			known, unknown := filterKnownEngines(router, tt.requested)
			require.Equal(t, tt.wantKnown, known)
			require.Equal(t, tt.wantUnknown, unknown)
		})
	}
}

func TestFormatMultiSearchResult(t *testing.T) {
	t.Parallel()

	t.Run("no sources and no notes", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "No results found. Try rephrasing your search.", formatMultiSearchResult(websearch.MultiResult{}))
	})

	t.Run("optional fields are omitted when empty", func(t *testing.T) {
		t.Parallel()
		got := formatMultiSearchResult(websearch.MultiResult{
			Sources: []websearch.MergedSource{
				{Source: websearch.Source{URL: "https://a.example", Title: "A"}, SeenIn: []string{"bing"}},
			},
		})
		require.Equal(t, "1. A [seen in: bing]\n   URL: https://a.example", got)
	})

	t.Run("summary and published date are rendered", func(t *testing.T) {
		t.Parallel()
		got := formatMultiSearchResult(websearch.MultiResult{
			Sources: []websearch.MergedSource{
				{
					Source: websearch.Source{URL: "https://a.example", Title: "A", Snippet: "About A.", PublishedAt: "2026-07-01"},
					SeenIn: []string{"bing", "ddg"},
				},
			},
		})
		require.Contains(t, got, "1. A [seen in: bing, ddg]")
		require.Contains(t, got, "   Summary: About A.")
		require.Contains(t, got, "   Published: 2026-07-01")
	})

	t.Run("used, failed, and skipped notes are joined", func(t *testing.T) {
		t.Parallel()
		got := formatMultiSearchResult(websearch.MultiResult{
			Used:    []string{"bing", "ddg"},
			Failed:  []string{"tavily (timeout)"},
			Skipped: []string{"exa"},
		})
		require.Equal(t,
			"No results found. Try rephrasing your search.Note: used: [bing, ddg]; failed: [tavily (timeout)]; skipped (no key): [exa]",
			got)
	})
}
