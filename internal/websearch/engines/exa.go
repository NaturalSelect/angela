package engines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// exaMCPEndpoint and exaRESTEndpoint are package vars so tests can
// point them at a local httptest server.
var (
	exaMCPEndpoint  = "https://mcp.exa.ai/mcp"
	exaRESTEndpoint = "https://api.exa.ai/search"
)

type exaEngine struct {
	client       *http.Client
	apiKey       string
	mcpEndpoint  string
	restEndpoint string
}

func newExaEngine(opts Options) *exaEngine {
	return &exaEngine{
		client:       opts.HTTPClient,
		apiKey:       opts.APIKeys["exa"],
		mcpEndpoint:  exaMCPEndpoint,
		restEndpoint: exaRESTEndpoint,
	}
}

func (e *exaEngine) ID() string { return "exa" }

// SupportsTimeRange is true only with an API key: the keyless MCP
// endpoint has no date filter.
func (e *exaEngine) SupportsTimeRange() bool { return e.apiKey != "" }

func (e *exaEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	if e.apiKey == "" {
		return e.searchMCP(ctx, req)
	}
	return e.searchREST(ctx, req)
}

// mcpToolCallResponse is the JSON-RPC envelope Exa's MCP endpoint
// replies with for a tools/call request.
type mcpToolCallResponse struct {
	Result *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *exaEngine) searchMCP(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	numResults := req.MaxResults
	if numResults <= 0 {
		numResults = 5
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web_search_exa",
			"arguments": map[string]any{
				"query":      req.Query,
				"type":       "auto",
				"numResults": numResults,
				"livecrawl":  "fallback",
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.mcpEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exa: MCP error (HTTP %d)", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("exa: reading response: %w", err)
	}

	var envelope mcpToolCallResponse
	if err := parseSSEData(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("exa: MCP error: %s", envelope.Error.Message)
	}
	if envelope.Result == nil {
		return nil, fmt.Errorf("exa: MCP error: no data")
	}

	var textBlocks []string
	for _, c := range envelope.Result.Content {
		if c.Type == "text" {
			textBlocks = append(textBlocks, c.Text)
		}
	}
	return parseExaMCPText(strings.Join(textBlocks, "\n")), nil
}

var (
	exaMCPTitleRe     = regexp.MustCompile(`(?m)^Title: (.+)$`)
	exaMCPURLRe       = regexp.MustCompile(`(?m)^URL: (\S+)$`)
	exaMCPPublishedRe = regexp.MustCompile(`(?m)^Published: (.+)$`)
	exaMCPHighlightRe = regexp.MustCompile(`(?m)^Highlights:$`)
	exaMCPDateRe      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
)

// splitExaMCPBlocks splits Exa MCP's concatenated text response into
// per-result blocks. Each block after the first starts right after a
// newline immediately followed by "Title:"; RE2 (Go's regexp engine)
// has no lookahead, so this splits on the "\nTitle:" delimiter and
// glues "Title:" back onto every piece but the leading one.
func splitExaMCPBlocks(text string) []string {
	pieces := strings.Split(text, "\nTitle:")
	blocks := make([]string, len(pieces))
	for i, p := range pieces {
		if i == 0 {
			blocks[i] = p
		} else {
			blocks[i] = "Title:" + p
		}
	}
	return blocks
}

// parseExaMCPText parses the "Title: X\nURL: Y\nPublished:
// Z\nHighlights:\n..." formatted text blocks the Exa MCP tool returns
// into one Source per block.
func parseExaMCPText(text string) []websearch.Source {
	var sources []websearch.Source
	for _, block := range splitExaMCPBlocks(text) {
		urlMatch := exaMCPURLRe.FindStringSubmatch(block)
		if urlMatch == nil {
			continue
		}
		src := websearch.Source{URL: urlMatch[1]}
		if m := exaMCPTitleRe.FindStringSubmatch(block); m != nil {
			src.Title = m[1]
		}
		if m := exaMCPPublishedRe.FindStringSubmatch(block); m != nil && exaMCPDateRe.MatchString(m[1]) {
			src.PublishedAt = m[1]
		}
		if parts := exaMCPHighlightRe.Split(block, 2); len(parts) == 2 {
			var lines []string
			for line := range strings.SplitSeq(parts[1], "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "...") {
					continue
				}
				lines = append(lines, trimmed)
				if len(lines) == 3 {
					break
				}
			}
			src.Snippet = strings.Join(lines, " ")
		}
		sources = append(sources, src)
	}
	return sources
}

type exaRESTResponse struct {
	Results []struct {
		URL           string   `json:"url"`
		Title         string   `json:"title"`
		Highlights    []string `json:"highlights"`
		PublishedDate string   `json:"publishedDate"`
	} `json:"results"`
}

func (e *exaEngine) searchREST(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	body := map[string]any{
		"query":    req.Query,
		"type":     "auto",
		"contents": map[string]any{"highlights": map[string]any{"highlightsPerUrl": 1}},
	}
	if req.MaxResults > 0 {
		body["numResults"] = req.MaxResults
	}
	if req.TimeRange != nil {
		if !req.TimeRange.After.IsZero() {
			body["startPublishedDate"] = req.TimeRange.After.Format("2006-01-02")
		} else {
			body["startPublishedDate"] = isoDaysAgo(req.TimeRange.Days)
		}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.restEndpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	httpReq.Header.Set("x-api-key", e.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exa: API error (HTTP %d)", resp.StatusCode)
	}

	var data exaRESTResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("exa: invalid response: %w", err)
	}

	var sources []websearch.Source
	for _, r := range data.Results {
		var snippet string
		for _, h := range r.Highlights {
			if strings.TrimSpace(h) != "" {
				snippet = h
				break
			}
		}
		if snippet == "" {
			continue
		}
		sources = append(sources, websearch.Source{
			URL: r.URL, Title: r.Title, Snippet: snippet, PublishedAt: r.PublishedDate,
		})
	}
	return sources, nil
}

// isoDaysAgo renders "days ago from now" the way Exa's
// startPublishedDate parameter expects: a millisecond-precision UTC
// timestamp.
func isoDaysAgo(days float64) string {
	return time.Now().UTC().Add(-time.Duration(days * float64(24*time.Hour))).Format("2006-01-02T15:04:05.000Z")
}
