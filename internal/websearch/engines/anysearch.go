package engines

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// anysearchEndpoint is a package var so tests can point it at a local
// httptest server.
var anysearchEndpoint = "https://api.anysearch.com/v1/search"

type anysearchEngine struct {
	client   *http.Client
	apiKey   string
	endpoint string
}

func newAnysearchEngine(opts Options) *anysearchEngine {
	return &anysearchEngine{client: opts.HTTPClient, apiKey: opts.APIKeys["anysearch"], endpoint: anysearchEndpoint}
}

func (e *anysearchEngine) ID() string { return "anysearch" }

func (e *anysearchEngine) SupportsTimeRange() bool { return false }

type anysearchResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	} `json:"data"`
}

func (e *anysearchEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}

	payload, status, err := e.attempt(ctx, req.Query, maxResults, e.apiKey)
	if err != nil {
		return nil, fmt.Errorf("anysearch: %w", err)
	}
	// A key AnySearch rejects is retried once anonymously rather than
	// failing the whole engine, matching AnySearch's free anonymous
	// tier existing as a fallback for a bad or revoked key.
	if (status == http.StatusUnauthorized || status == http.StatusForbidden) && e.apiKey != "" {
		payload, status, err = e.attempt(ctx, req.Query, maxResults, "")
		if err != nil {
			return nil, fmt.Errorf("anysearch: %w", err)
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("anysearch: API error (HTTP %d)", status)
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("anysearch: API error: %s", cmp.Or(payload.Message, strconv.Itoa(payload.Code)))
	}

	var sources []websearch.Source
	for _, r := range payload.Data.Results {
		if r.URL == "" {
			continue
		}
		sources = append(sources, websearch.Source{URL: r.URL, Title: r.Title, Snippet: r.Snippet})
	}
	return sources, nil
}

// attempt issues one AnySearch request and returns the decoded body
// alongside the HTTP status, even for a non-2xx response, so Search
// can decide whether to retry anonymously before turning a bad status
// into an error.
func (e *anysearchEngine) attempt(ctx context.Context, query string, maxResults int, key string) (anysearchResponse, int, error) {
	var out anysearchResponse

	reqBody, err := json.Marshal(map[string]any{"query": query, "max_results": maxResults})
	if err != nil {
		return out, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return out, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return out, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}
