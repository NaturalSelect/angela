package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/NaturalSelect/angela/internal/browserhttp"
	"github.com/NaturalSelect/angela/internal/websearch"
)

// ddgHTMLEndpoint is a package var so tests can point it at a local
// httptest server.
var ddgHTMLEndpoint = "https://html.duckduckgo.com/html/"

const ddgHTMLAcceptLanguage = "en-US,en;q=0.9"

type ddgHTMLEngine struct {
	client   *http.Client
	endpoint string
}

func newDDGHTMLEngine(opts Options) *ddgHTMLEngine {
	return &ddgHTMLEngine{client: opts.HTTPClient, endpoint: ddgHTMLEndpoint}
}

func (e *ddgHTMLEngine) ID() string { return "ddg" }

func (e *ddgHTMLEngine) SupportsTimeRange() bool { return true }

func (e *ddgHTMLEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	params := url.Values{}
	params.Set("q", req.Query)
	// Safe search off; Options has no per-request knob for this yet.
	params.Set("adlt", "-1")
	if req.TimeRange != nil {
		if df, ok := namedTierLetter[websearch.ApproximateTier(tierDays(req.TimeRange))]; ok {
			params.Set("df", df)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, e.endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("ddg: %w", err)
	}
	httpReq.Header.Set("User-Agent", browserhttp.ChromeUserAgent)
	httpReq.Header.Set("Accept-Language", ddgHTMLAcceptLanguage)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ddg: %w", err)
	}
	defer resp.Body.Close()

	// DuckDuckGo serves the anti-bot interstitial as HTTP 202.
	if resp.StatusCode == http.StatusAccepted {
		return nil, errDDGRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ddg: HTTP %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ddg: parsing response: %w", err)
	}

	var sources []websearch.Source
	doc.Find(".result.results_links").Each(func(_ int, s *goquery.Selection) {
		link := s.Find("a.result__a").First()
		href, hasHref := link.Attr("href")
		if !hasHref || href == "" {
			return
		}
		realURL := decodeDDGRedirect(href)
		if realURL == "" {
			return
		}
		title := strings.TrimSpace(link.Text())
		snippet := strings.TrimSpace(s.Find("a.result__snippet").First().Text())
		sources = append(sources, websearch.Source{URL: realURL, Title: title, Snippet: snippet})
	})

	if req.MaxResults > 0 && len(sources) > req.MaxResults {
		sources = sources[:req.MaxResults]
	}
	return sources, nil
}

// decodeDDGRedirect extracts the real destination from a DuckDuckGo
// result link, which is either a same-origin redirect
// (//duckduckgo.com/l/?uddg=<percent-encoded-url>) or, rarely, already
// a bare URL.
func decodeDDGRedirect(href string) string {
	if _, after, ok := strings.Cut(href, "uddg="); ok {
		encoded := after
		if amp := strings.IndexByte(encoded, '&'); amp != -1 {
			encoded = encoded[:amp]
		}
		decoded, err := url.QueryUnescape(encoded)
		if err != nil {
			return encoded
		}
		return decoded
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}
