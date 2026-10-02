package engines

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"

	"github.com/NaturalSelect/angela/internal/browserhttp"
	"github.com/NaturalSelect/angela/internal/websearch"
)

// bingEndpoint is a package var so tests can point it at a local
// httptest server.
var bingEndpoint = "https://www.bing.com/search"

// defaultBingMarket is used when Options.BingMarket is empty.
const defaultBingMarket = "en-US"

// bingMarketAcceptLanguage maps a Bing market to the Accept-Language
// header that best matches it, so results and page chrome come back
// in the expected language. Falls back to defaultBingAcceptLanguage
// for markets not listed here.
var bingMarketAcceptLanguage = map[string]string{
	"zh-CN": "zh-CN,zh;q=0.9,en;q=0.8",
	"zh-TW": "zh-TW,zh;q=0.9,en;q=0.8",
	"en-US": "en-US,en;q=0.9",
	"en-GB": "en-GB,en;q=0.9",
	"ru-RU": "ru-RU,ru;q=0.9,en;q=0.8",
	"ja-JP": "ja-JP,ja;q=0.9,en;q=0.8",
	"de-DE": "de-DE,de;q=0.9,en;q=0.8",
	"fr-FR": "fr-FR,fr;q=0.9,en;q=0.8",
	"es-ES": "es-ES,es;q=0.9,en;q=0.8",
	"ko-KR": "ko-KR,ko;q=0.9,en;q=0.8",
}

// defaultBingAcceptLanguage is used for markets absent from
// bingMarketAcceptLanguage. English rather than the upstream
// reference's Chinese-centric default, since Angela's own default
// market is en-US.
const defaultBingAcceptLanguage = "en-US,en;q=0.9"

type bingEngine struct {
	client   *http.Client
	market   string
	endpoint string
}

func newBingEngine(opts Options) *bingEngine {
	market := opts.BingMarket
	if market == "" {
		market = defaultBingMarket
	}
	return &bingEngine{client: opts.HTTPClient, market: market, endpoint: bingEndpoint}
}

func (e *bingEngine) ID() string { return "bing" }

func (e *bingEngine) SupportsTimeRange() bool { return false }

func (e *bingEngine) Search(ctx context.Context, req websearch.Request) ([]websearch.Source, error) {
	params := url.Values{}
	params.Set("q", req.Query)
	params.Set("mkt", e.market)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, e.endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("bing: %w", err)
	}
	// NOTE: Bing serves a degraded page, or blocks outright, for clients it
	// does not recognize as a browser.
	httpReq.Header.Set("User-Agent", browserhttp.ChromeUserAgent)
	acceptLanguage, ok := bingMarketAcceptLanguage[e.market]
	if !ok {
		acceptLanguage = defaultBingAcceptLanguage
	}
	httpReq.Header.Set("Accept-Language", acceptLanguage)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bing: HTTP %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bing: parsing response: %w", err)
	}

	var sources []websearch.Source
	doc.Find("li.b_algo").Each(func(_ int, s *goquery.Selection) {
		link := s.Find("h2 a").First()
		href, hasHref := link.Attr("href")
		if !hasHref || href == "" {
			return
		}
		title := strings.TrimSpace(link.Text())
		snippet := strings.TrimSpace(s.Find("p").First().Text())
		sources = append(sources, websearch.Source{URL: decodeBingRedirect(href), Title: title, Snippet: snippet})
	})

	// Bing serves a fully unrelated cached results page rather than an
	// empty one when a query has no hits; a page whose results share
	// no token with the query at all is treated as that noise page
	// and dropped so the Router falls back to the next engine.
	if len(sources) > 0 && !looksRelevant(req.Query, sources) {
		return nil, nil
	}

	if req.MaxResults > 0 && len(sources) > req.MaxResults {
		sources = sources[:req.MaxResults]
	}
	return sources, nil
}

// NOTE: Bing wraps organic links as /ck/a?u=a1<base64url(target)>.
func decodeBingRedirect(href string) string {
	parsed, err := url.Parse(href)
	if err != nil || parsed.Path != "/ck/a" || !isBingHost(parsed.Hostname()) {
		return href
	}
	encoded, ok := strings.CutPrefix(parsed.Query().Get("u"), "a1")
	if !ok {
		return href
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return href
	}
	target, err := url.Parse(string(decoded))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return href
	}
	return string(decoded)
}

func isBingHost(host string) bool {
	host = strings.ToLower(host)
	return host == "bing.com" || strings.HasSuffix(host, ".bing.com")
}

var cjkRunRe = regexp.MustCompile(`[\p{Han}]+`)

// queryOverlapTokens tokenizes a query for the relevance check below:
// CJK runs become overlapping bigrams (plus the run itself when it's
// two characters or shorter), and Latin/digit runs of length >= 2
// become whole-word tokens.
func queryOverlapTokens(query string) []string {
	var tokens []string
	seen := make(map[string]bool)
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			tokens = append(tokens, t)
		}
	}

	for _, run := range cjkRunRe.FindAllString(query, -1) {
		runes := []rune(run)
		if len(runes) <= 2 {
			add(run)
		}
		for i := 0; i+1 < len(runes); i++ {
			add(string(runes[i : i+2]))
		}
	}

	lower := strings.ToLower(query)
	for _, word := range strings.FieldsFunc(lower, func(r rune) bool {
		return (!unicode.IsLower(r) || r > unicode.MaxASCII) && !unicode.IsDigit(r)
	}) {
		if len([]rune(word)) >= 2 {
			add(word)
		}
	}
	return tokens
}

// looksRelevant reports whether at least one source shares a query
// token with its title, snippet, or URL. A query that tokenizes to
// nothing (pure punctuation) can't be judged, so it's treated as
// relevant rather than filtering everything out.
func looksRelevant(query string, sources []websearch.Source) bool {
	tokens := queryOverlapTokens(query)
	if len(tokens) == 0 {
		return true
	}
	for _, s := range sources {
		hay := strings.ToLower(s.Title + " " + s.Snippet + " " + s.URL)
		for _, t := range tokens {
			if strings.Contains(hay, strings.ToLower(t)) {
				return true
			}
		}
	}
	return false
}
