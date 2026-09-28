package engines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// tavilyEndpoint is a package var so tests can point it at a local
// httptest server.
var tavilyEndpoint = "https://api.tavily.com/search"

type tavilyEngine struct {
	client   *http.Client
	apiKey   string
	endpoint string
}

func newTavilyEngine(opts Options) *tavilyEngine {
	return &tavilyEngine{client: opts.HTTPClient, apiKey: opts.APIKeys["tavily"], endpoint: tavilyEndpoint}
}

func (e *tavilyEngine) ID() string { return "tavily" }

func (e *tavilyEngine) SupportsTimeRange() bool { return true }

type tavilyResponse struct {
	Results []struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"results"`
}

func (e *tavilyEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	maxResults = min(maxResults, 20)

	body := map[string]any{
		"query":        req.Query,
		"max_results":  maxResults,
		"search_depth": "basic",
	}
	if req.TimeRange != nil {
		body["time_range"] = websearch.ApproximateTier(tierDays(req.TimeRange))
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if e.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)
	} else {
		httpReq.Header.Set("x-tavily-access-mode", "keyless")
	}

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily: API error (HTTP %d)", resp.StatusCode)
	}

	var data tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("tavily: invalid response: %w", err)
	}

	var sources []websearch.Source
	for _, r := range data.Results {
		if r.URL == "" {
			continue
		}
		sources = append(sources, websearch.Source{URL: r.URL, Title: r.Title, Snippet: r.Content})
	}
	return sources, nil
}
