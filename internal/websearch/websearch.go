// Package websearch provides a pluggable web-search engine abstraction.
// An Engine wraps one search backend (a scraped HTML search page, a
// REST API, an MCP tool call); a Router tries a preferred Engine first
// and falls back through the rest of a fixed order until one returns
// results, so a single engine going down or rate-limiting never breaks
// search outright.
package websearch

import (
	"context"
	"errors"
)

// Source is one search result returned by an Engine. Title, Snippet,
// and PublishedAt are optional: not every engine returns all three, and
// leaving a field empty is more honest than inventing a value.
type Source struct {
	URL         string
	Title       string
	Snippet     string
	PublishedAt string
}

// Request is one search query issued to an Engine, a Router, or
// SearchMany.
type Request struct {
	Query      string
	MaxResults int
	// TimeRange narrows results to a recent window or a date; nil means
	// no filter. Only engines whose SupportsTimeRange returns true
	// apply it.
	TimeRange *TimeRange
}

// Engine is one search backend a Router can fall back across.
type Engine interface {
	// ID is the stable identifier used in configuration, in Router's
	// fallback order, and in result output.
	ID() string
	// SupportsTimeRange reports whether Search honors Request.TimeRange.
	SupportsTimeRange() bool
	// Search runs one query. Returning ErrNotConfigured (wrapped or
	// bare) tells the Router this engine is missing a required
	// credential rather than that the request itself failed, so it is
	// skipped rather than counted as the engine "failing".
	Search(ctx context.Context, req Request) ([]Source, error)
}

// ErrNotConfigured is returned by Engine.Search when the engine needs a
// credential that isn't set. Wrap it with context (e.g. which
// environment variable is missing) rather than returning it bare.
var ErrNotConfigured = errors.New("engine is not configured")

// Result is what Router.Search returns: the sources from whichever
// engine produced them, and — when that wasn't the preferred engine —
// a human-readable Note explaining the fallback.
type Result struct {
	Sources []Source
	Engine  string
	Note    string
}
