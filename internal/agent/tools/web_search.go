package tools

import (
	"context"
	_ "embed"
	"log/slog"
	"net/http"
	"text/template"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed web_search.md.tpl
var webSearchDescriptionTmpl []byte

var webSearchDescriptionTpl = template.Must(
	template.New("webSearchDescription").
		Parse(string(webSearchDescriptionTmpl)),
)

// NewWebSearchTool creates a web search tool for sub-agents.
func NewWebSearchTool(workingDir string, client *http.Client) fantasy.AgentTool {
	if client == nil {
		client = newDefaultHTTPClient(defaultToolHTTPTimeout)
	}

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

			maybeDelaySearch()
			results, err := searchDuckDuckGo(ctx, client, params.Query, maxResults)
			slog.Debug("Web search completed", "query", params.Query, "results", len(results), "err", err)
			if err != nil {
				return Fail("Failed to search: " + err.Error())
			}

			return Ok(formatSearchResults(results))
		},
	)
}
