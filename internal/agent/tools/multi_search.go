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

//go:embed multi_search.md.tpl
var multiSearchDescriptionTmpl []byte

var multiSearchDescriptionTpl = template.Must(
	template.New("multiSearchDescription").
		Parse(string(multiSearchDescriptionTmpl)),
)

// NewMultiSearchTool creates a tool that queries several search
// engines concurrently through router and merges/deduplicates their
// results, ranking sources multiple engines agree on above
// single-source ones. It consumes more engine quota than WebSearch, so
// it is meant for on-demand cross-source validation rather than
// routine lookups.
func NewMultiSearchTool(router *websearch.Router) fantasy.AgentTool {
	return NewParallelTool(
		toolnames.MultiSearch,
		renderToolDescription(multiSearchDescriptionTpl),
		func(ctx context.Context, params MultiSearchParams, call fantasy.ToolCall) Result {
			if params.Query == "" {
				return Fail("query is required")
			}

			maxResults := params.MaxResults
			if maxResults <= 0 {
				maxResults = 8
			}
			if maxResults > 20 {
				maxResults = 20
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for multi-engine search")
			}

			engines, unknown := filterKnownEngines(router, params.Engines)
			if len(params.Engines) > 0 && len(engines) == 0 {
				return Failf("no valid engines selected; unknown engine ids: %s", strings.Join(unknown, ", "))
			}

			result, err := router.SearchMany(ctx, websearch.Request{
				Query:      params.Query,
				MaxResults: maxResults,
			}, engines)
			slog.Debug("Multi search completed", "query", params.Query, "used", result.Used, "failed", result.Failed, "skipped", result.Skipped, "err", err)
			if err != nil {
				return Fail("Failed to search: " + err.Error())
			}

			return Ok(formatMultiSearchResult(result))
		},
	)
}

// filterKnownEngines drops any requested engine id the router doesn't
// recognize, returning the accepted ids alongside the rejected ones
// (for an error message when nothing valid remains).
func filterKnownEngines(router *websearch.Router, requested []string) (known, unknown []string) {
	if len(requested) == 0 {
		return nil, nil
	}
	valid := make(map[string]bool)
	for _, id := range router.EngineIDs() {
		valid[id] = true
	}
	for _, id := range requested {
		if valid[id] {
			known = append(known, id)
		} else {
			unknown = append(unknown, id)
		}
	}
	return known, unknown
}

// formatMultiSearchResult renders a websearch.MultiResult as the
// model-facing text: the merged, ranked source list, each annotated
// with which engines returned it, followed by a note on which engines
// contributed, failed, or were skipped.
func formatMultiSearchResult(result websearch.MultiResult) string {
	var sb strings.Builder

	if len(result.Sources) == 0 {
		sb.WriteString("No results found. Try rephrasing your search.")
	} else {
		for i, s := range result.Sources {
			fmt.Fprintf(&sb, "%d. %s [seen in: %s]\n", i+1, s.Title, strings.Join(s.SeenIn, ", "))
			fmt.Fprintf(&sb, "   URL: %s\n", s.URL)
			if s.Snippet != "" {
				fmt.Fprintf(&sb, "   Summary: %s\n", s.Snippet)
			}
			if s.PublishedAt != "" {
				fmt.Fprintf(&sb, "   Published: %s\n", s.PublishedAt)
			}
			sb.WriteString("\n")
		}
	}

	var notes []string
	if len(result.Used) > 0 {
		notes = append(notes, fmt.Sprintf("used: [%s]", strings.Join(result.Used, ", ")))
	}
	if len(result.Failed) > 0 {
		notes = append(notes, fmt.Sprintf("failed: [%s]", strings.Join(result.Failed, ", ")))
	}
	if len(result.Skipped) > 0 {
		notes = append(notes, fmt.Sprintf("skipped (no key): [%s]", strings.Join(result.Skipped, ", ")))
	}
	if len(notes) > 0 {
		fmt.Fprintf(&sb, "Note: %s", strings.Join(notes, "; "))
	}

	return strings.TrimRight(sb.String(), "\n")
}
