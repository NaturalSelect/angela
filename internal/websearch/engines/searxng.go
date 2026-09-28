package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// defaultSearxngInstances is a small snapshot of public SearXNG
// instances used when Options.SearxngInstances is empty. Public
// instances rotate and die often (operators shut down, get
// overloaded, or start requiring a login); this list was current as
// of writing and is entirely overridable via Options.
var defaultSearxngInstances = []string{
	"https://searx.be",
	"https://priv.au",
	"https://search.inetol.net",
}

// searxngInstanceTimeout bounds a single instance's request; SearXNG
// aggregates several instances per Search call, so a slow or dead one
// shouldn't stall the rest.
const searxngInstanceTimeout = 8 * time.Second

type searxngEngine struct {
	client    *http.Client
	instances []string
}

func newSearxngEngine(opts Options) *searxngEngine {
	instances := opts.SearxngInstances
	if len(instances) == 0 {
		instances = defaultSearxngInstances
	}
	return &searxngEngine{client: opts.HTTPClient, instances: instances}
}

func (e *searxngEngine) ID() string { return "searxng" }

func (e *searxngEngine) SupportsTimeRange() bool { return true }

type searxngResponse struct {
	Results []struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"results"`
}

// Search tries each configured instance in turn and returns the first
// one to answer with at least one result, aggregating every
// instance's failure reason so a total outage doesn't just report the
// last instance tried.
func (e *searxngEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	var failures []string
	for _, instance := range e.instances {
		sources, err := e.searchInstance(ctx, instance, req)
		switch {
		case err != nil:
			failures = append(failures, fmt.Sprintf("%s: %s", instance, err))
		case len(sources) > 0:
			return sources, nil
		default:
			failures = append(failures, fmt.Sprintf("%s: 0 results", instance))
		}
	}

	detail := "no instances configured"
	if len(failures) > 0 {
		detail = strings.Join(failures, ", ")
	}
	return nil, fmt.Errorf("searxng: all instances failed: %s", detail)
}

func (e *searxngEngine) searchInstance(ctx context.Context, instance string, req websearch.Request) ([]websearch.Source, error) {
	ctx, cancel := context.WithTimeout(ctx, searxngInstanceTimeout)
	defer cancel()

	params := url.Values{}
	params.Set("q", req.Query)
	params.Set("format", "json")
	if req.TimeRange != nil {
		params.Set("time_range", websearch.ApproximateTier(tierDays(req.TimeRange)))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, instance+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var data searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
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
