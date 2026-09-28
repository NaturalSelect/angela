package engines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// firecrawlEndpoint is a package var so tests can point it at a local
// httptest server.
var firecrawlEndpoint = "https://api.firecrawl.dev/v2/search"

// firecrawlTierTBS maps a named tier to Firecrawl's Google-style tbs
// query-time filter for a fixed recent window.
var firecrawlTierTBS = map[string]string{
	"day":   "qdr:d",
	"week":  "qdr:w",
	"month": "qdr:m",
	"year":  "qdr:y",
}

type firecrawlEngine struct {
	client   *http.Client
	apiKey   string
	endpoint string
}

func newFirecrawlEngine(opts Options) *firecrawlEngine {
	return &firecrawlEngine{client: opts.HTTPClient, apiKey: opts.APIKeys["firecrawl"], endpoint: firecrawlEndpoint}
}

func (e *firecrawlEngine) ID() string { return "firecrawl" }

func (e *firecrawlEngine) SupportsTimeRange() bool { return true }

type firecrawlResponse struct {
	Data struct {
		Web []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"web"`
	} `json:"data"`
}

func (e *firecrawlEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	limit := req.MaxResults
	if limit <= 0 {
		limit = 5
	}
	limit = min(max(limit, 1), 10)

	body := map[string]any{"query": req.Query, "limit": limit}
	if tbs := firecrawlTBS(req.TimeRange); tbs != "" {
		body["tbs"] = tbs
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("firecrawl: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("firecrawl: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if e.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("firecrawl: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("firecrawl: API error (HTTP %d)", resp.StatusCode)
	}

	var data firecrawlResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("firecrawl: invalid response: %w", err)
	}

	var sources []websearch.Source
	for _, r := range data.Data.Web {
		if r.URL == "" {
			continue
		}
		sources = append(sources, websearch.Source{URL: r.URL, Title: r.Title, Snippet: r.Description})
	}
	return sources, nil
}

// firecrawlTBS renders a TimeRange as Firecrawl's Google-style tbs
// parameter: a fixed named tier (qdr:d/w/m/y) or, for an absolute
// After date, an open-ended range starting at that date
// (cdr:1,cd_min:M/D/YYYY). Returns "" for a nil range or a tier
// Firecrawl doesn't recognize.
func firecrawlTBS(tr *websearch.TimeRange) string {
	if tr == nil {
		return ""
	}
	if !tr.After.IsZero() {
		return fmt.Sprintf("cdr:1,cd_min:%s", formatFirecrawlDate(tr.After))
	}
	return firecrawlTierTBS[websearch.ApproximateTier(tr.Days)]
}

// formatFirecrawlDate renders a date the way Google's (and so
// Firecrawl's) cd_min parameter expects: M/D/YYYY, no zero-padding.
func formatFirecrawlDate(t time.Time) string {
	return fmt.Sprintf("%d/%d/%d", t.Month(), t.Day(), t.Year())
}
