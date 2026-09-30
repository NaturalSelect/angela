// Package engines provides concrete websearch.Engine implementations,
// ported from DDDMUC/dsh-free-search (MIT License), a deepseek-harness
// plugin providing free/keyless web search fallback chains.
package engines

import (
	"net/http"
	"time"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// defaultHTTPTimeout bounds outbound requests made by every engine that
// doesn't set a tighter per-request deadline of its own (SearXNG dials
// each instance with its own shorter timeout instead).
const defaultHTTPTimeout = 30 * time.Second

// defaultIdleTimeout bounds how long an idle keep-alive connection stays
// open before the transport closes it.
const defaultIdleTimeout = 90 * time.Second

// newDefaultHTTPClient returns an HTTP client tuned the same way as
// Angela's internal/agent/tools default client. Duplicated here rather
// than imported because internal/agent/tools will depend on this
// package, not the other way around.
func newDefaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = defaultIdleTimeout

	return &http.Client{
		Timeout:   defaultHTTPTimeout,
		Transport: transport,
	}
}

// Options configures the engines Default builds.
type Options struct {
	// HTTPClient is used for all outbound requests; nil builds a
	// package-default client (see newDefaultHTTPClient).
	HTTPClient *http.Client
	// APIKeys maps an engine id to its already-resolved credential
	// ($VAR expansion and env-var fallback both happen upstream of
	// this package). Absent/empty means "no key" — most engines below
	// have a keyless mode for that case.
	APIKeys map[string]string
	// BingMarket is the market/locale for Bing results, e.g. "en-US".
	// Empty uses "en-US".
	BingMarket string
	// SearxngInstances overrides the built-in public instance list.
	SearxngInstances []string
}

// Default returns the built-in engines in a fixed order: bing, ddg, exa,
// ddg-lite, anysearch, tavily, firecrawl, searxng.
//
// NOTE: The order is load-bearing. It is the fallback chain for a
// single search and its first three entries are the multi-engine
// default, so those three must not share a backend (ddg-lite is a
// second DuckDuckGo frontend and therefore sits after them).
func Default(opts Options) []websearch.Engine {
	if opts.HTTPClient == nil {
		opts.HTTPClient = newDefaultHTTPClient()
	}
	return []websearch.Engine{
		newBingEngine(opts),
		newDDGHTMLEngine(opts),
		newExaEngine(opts),
		newDDGLiteEngine(opts),
		newAnysearchEngine(opts),
		newTavilyEngine(opts),
		newFirecrawlEngine(opts),
		newSearxngEngine(opts),
	}
}

// namedTierLetter maps websearch.ApproximateTier's output to the
// single-letter day/week/month/year code DuckDuckGo's df parameter
// expects (shared by the ddg and ddg-lite engines).
var namedTierLetter = map[string]string{
	"day":   "d",
	"week":  "w",
	"month": "m",
	"year":  "y",
}

// tierDays returns the day count used to approximate a TimeRange to a
// named day/week/month/year tier, for engines that only support fixed
// tiers (DDG, Tavily, SearXNG). An absolute After date is converted to
// the number of days between it and now.
func tierDays(tr *websearch.TimeRange) float64 {
	if tr == nil {
		return 0
	}
	if tr.After.IsZero() {
		return tr.Days
	}
	return time.Since(tr.After).Hours() / 24
}
