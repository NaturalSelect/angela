package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/websearch"
)

//go:embed web_search.md.tpl
var webSearchDescriptionTmpl []byte

var webSearchDescriptionTpl = template.Must(
	template.New("webSearchDescription").
		Parse(string(webSearchDescriptionTmpl)),
)

// NewWebSearchTool creates a web search tool backed by router: a
// preferred engine tried first, falling back through the rest of its
// configured engines until one returns results.
func NewWebSearchTool(router *websearch.Router) fantasy.AgentTool {
	return NewParallelTool(
		toolnames.WebSearch,
		renderToolDescription(webSearchDescriptionTpl),
		func(ctx context.Context, params WebSearchParams, call fantasy.ToolCall) Result {
			if params.Query == "" {
				return Fail("query is required")
			}

			maxResults := params.MaxResults
			if maxResults <= 0 {
				maxResults = 10
			}
			if maxResults > 20 {
				maxResults = 20
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for creating a new file")
			}

			timeRange, err := websearch.ParseTimeRange(params.TimeRange)
			if err != nil {
				return Fail(err.Error())
			}

			result, err := router.Search(ctx, websearch.Request{
				Query:      params.Query,
				MaxResults: maxResults,
				TimeRange:  timeRange,
			})
			slog.Debug("Web search completed", "query", params.Query, "engine", result.Engine, "results", len(result.Sources), "err", err)
			if err != nil {
				return Fail("Failed to search: " + err.Error())
			}

			return Ok(formatWebSearchResult(result))
		},
	)
}

// formatWebSearchResult renders a websearch.Result as the model-facing
// text: which engine answered, an explanatory note when it wasn't the
// preferred one, then the numbered source list.
func formatWebSearchResult(result websearch.Result) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Engine: %s\n", result.Engine)
	if result.Note != "" {
		sb.WriteString(result.Note)
		sb.WriteString("\n")
	}

	if len(result.Sources) == 0 {
		sb.WriteString("\nNo results found. Try rephrasing your search.")
		return sb.String()
	}

	sb.WriteString("\n")
	for i, s := range result.Sources {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, s.Title)
		fmt.Fprintf(&sb, "   URL: %s\n", s.URL)
		if s.Snippet != "" {
			fmt.Fprintf(&sb, "   Summary: %s\n", s.Snippet)
		}
		if s.PublishedAt != "" {
			fmt.Fprintf(&sb, "   Published: %s\n", s.PublishedAt)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
