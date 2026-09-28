package engines

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// ddgLiteEndpoint is a package var so tests can point the search at a
// local httptest server.
var ddgLiteEndpoint = "https://lite.duckduckgo.com/lite/?q="

var ddgLiteUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
}

var ddgLiteAcceptLanguages = []string{
	"en-US,en;q=0.9",
	"en-US,en;q=0.9,es;q=0.8",
	"en-GB,en;q=0.9,en-US;q=0.8",
	"en-US,en;q=0.5",
	"en-CA,en;q=0.9,en-US;q=0.8",
}

// errDDGRateLimited reports that DuckDuckGo served a bot-check page
// instead of results. Shared with ddg_html.go, since both endpoints
// serve the same anti-bot interstitial.
var errDDGRateLimited = errors.New(
	"DuckDuckGo is rate-limiting this machine. " +
		"Do not retry or rephrase; wait a few minutes or fetch known URLs directly",
)

// ddgAnomalyMarkers are substrings of the bot-detection page
// DuckDuckGo Lite serves (with HTTP 200) instead of results once a
// client trips its rate limiter. Parsing that page yields zero
// results, which would otherwise masquerade as "your query found
// nothing".
var ddgAnomalyMarkers = []string{
	"anomaly-modal",
	"/anomaly.js",
	"Unfortunately, bots use DuckDuckGo too",
}

type ddgLiteEngine struct {
	client   *http.Client
	endpoint string
}

func newDDGLiteEngine(opts Options) *ddgLiteEngine {
	return &ddgLiteEngine{client: opts.HTTPClient, endpoint: ddgLiteEndpoint}
}

func (e *ddgLiteEngine) ID() string { return "ddg-lite" }

func (e *ddgLiteEngine) SupportsTimeRange() bool { return true }

func (e *ddgLiteEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}

	maybeDelaySearch()

	searchURL := e.endpoint + url.QueryEscape(req.Query)
	if req.TimeRange != nil {
		if df, ok := namedTierLetter[websearch.ApproximateTier(tierDays(req.TimeRange))]; ok {
			searchURL += "&df=" + df
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ddg-lite: %w", err)
	}
	setDDGLiteHeaders(httpReq)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ddg-lite: %w", err)
	}
	defer resp.Body.Close()

	// A 202 from DuckDuckGo is the anomaly-challenge interstitial, not
	// a result page; report throttling rather than parsing it into an
	// empty result set.
	if resp.StatusCode == http.StatusAccepted {
		return nil, errDDGRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ddg-lite: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ddg-lite: reading response: %w", err)
	}

	content := string(body)
	for _, marker := range ddgAnomalyMarkers {
		if strings.Contains(content, marker) {
			return nil, errDDGRateLimited
		}
	}

	return parseDDGLiteResults(content, maxResults)
}

func setDDGLiteHeaders(req *http.Request) {
	req.Header.Set("User-Agent", ddgLiteUserAgents[rand.IntN(len(ddgLiteUserAgents))])
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", ddgLiteAcceptLanguages[rand.IntN(len(ddgLiteAcceptLanguages))])
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")
	if rand.IntN(2) == 0 {
		req.Header.Set("DNT", "1")
	}
}

func parseDDGLiteResults(htmlContent string, maxResults int) ([]websearch.Source, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("ddg-lite: parsing response: %w", err)
	}

	var sources []websearch.Source
	var current *websearch.Source

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "a" && hasHTMLClass(n, "result-link") {
				if current != nil && current.URL != "" {
					sources = append(sources, *current)
					if len(sources) >= maxResults {
						return
					}
				}
				current = &websearch.Source{Title: htmlTextContent(n)}
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						current.URL = decodeDDGRedirect(attr.Val)
						break
					}
				}
			}
			if n.Data == "td" && hasHTMLClass(n, "result-snippet") && current != nil {
				current.Snippet = htmlTextContent(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if len(sources) >= maxResults {
				return
			}
			traverse(c)
		}
	}
	traverse(doc)

	if current != nil && current.URL != "" && len(sources) < maxResults {
		sources = append(sources, *current)
	}

	return sources, nil
}

func hasHTMLClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" && slices.Contains(strings.Fields(attr.Val), class) {
			return true
		}
	}
	return false
}

func htmlTextContent(n *html.Node) string {
	var text strings.Builder
	var traverse func(*html.Node)
	traverse = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(n)
	return strings.TrimSpace(text.String())
}

var (
	lastDDGLiteSearchMu   sync.Mutex
	lastDDGLiteSearchTime time.Time
)

// maybeDelaySearch adds a small random delay if the last DDG Lite
// search was recent, spreading out requests so a burst of calls
// doesn't read as automated scraping.
func maybeDelaySearch() {
	lastDDGLiteSearchMu.Lock()
	defer lastDDGLiteSearchMu.Unlock()

	minGap := time.Duration(500+rand.IntN(1500)) * time.Millisecond
	elapsed := time.Since(lastDDGLiteSearchTime)
	if elapsed < minGap {
		time.Sleep(minGap - elapsed)
	}
	lastDDGLiteSearchTime = time.Now()
}
