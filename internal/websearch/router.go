package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// defaultBudget bounds the total wall-clock time a Router.Search call
// may spend walking the fallback chain, so per-engine timeouts can't
// add up to minutes.
const defaultBudget = 30 * time.Second

// perEngineCap bounds how much of the remaining budget a single engine
// attempt gets, so one slow engine near the end of the chain can't eat
// the whole remaining budget on its own.
const perEngineCap = 15 * time.Second

// Router tries engines in a fixed fallback order until one returns
// results.
type Router struct {
	engines   []Engine
	byID      map[string]Engine
	preferred string
	budget    time.Duration
}

// NewRouter builds a Router that tries preferred first, then the rest
// of engines in the order given. budget <= 0 uses a 30s default.
func NewRouter(engines []Engine, preferred string, budget time.Duration) (*Router, error) {
	if len(engines) == 0 {
		return nil, errors.New("websearch: at least one engine is required")
	}
	byID := make(map[string]Engine, len(engines))
	for _, e := range engines {
		if _, dup := byID[e.ID()]; dup {
			return nil, fmt.Errorf("websearch: duplicate engine id %q", e.ID())
		}
		byID[e.ID()] = e
	}
	if preferred == "" {
		preferred = engines[0].ID()
	} else if _, ok := byID[preferred]; !ok {
		return nil, fmt.Errorf("websearch: preferred engine %q is not registered", preferred)
	}
	if budget <= 0 {
		budget = defaultBudget
	}
	return &Router{engines: engines, byID: byID, preferred: preferred, budget: budget}, nil
}

// EngineIDs returns the registered engine ids in fallback order.
func (r *Router) EngineIDs() []string {
	ids := make([]string, len(r.engines))
	for i, e := range r.engines {
		ids[i] = e.ID()
	}
	return ids
}

// chain returns the engines to try, in order, for req, plus the reason
// the preferred engine isn't first when req.TimeRange excludes it.
// Without a time range: preferred first, then the rest in registration
// order. With one: engines that honor it move to the front (preferred
// among them, if it qualifies). A preferred engine that doesn't honor it
// is dropped for this request, while other engines that don't are kept
// at the very end as a last resort so a time-scoped search still
// answers when every time-aware engine fails. Search flags that case in
// the result Note, since those engines ignore the time filter.
func (r *Router) chain(req Request) (ordered []Engine, skippedReason string) {
	if req.TimeRange == nil {
		ordered = make([]Engine, 0, len(r.engines))
		ordered = append(ordered, r.byID[r.preferred])
		for _, e := range r.engines {
			if e.ID() != r.preferred {
				ordered = append(ordered, e)
			}
		}
		return ordered, ""
	}

	preferredEngine := r.byID[r.preferred]
	var withRange, withoutRange []Engine
	if preferredEngine.SupportsTimeRange() {
		withRange = append(withRange, preferredEngine)
	} else {
		skippedReason = fmt.Sprintf("%s does not support time filtering (time_range=%s)", r.preferred, req.TimeRange.String())
	}
	for _, e := range r.engines {
		if e.ID() == r.preferred {
			continue
		}
		if e.SupportsTimeRange() {
			withRange = append(withRange, e)
		} else {
			withoutRange = append(withoutRange, e)
		}
	}
	return append(withRange, withoutRange...), skippedReason
}

// Search runs req against the fallback chain, returning the first
// engine's sources. An error, an empty result, or ErrNotConfigured all
// advance to the next engine; the chain stops once the Router's budget
// is spent.
func (r *Router) Search(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Query) == "" {
		return Result{}, errors.New("websearch: query is required")
	}

	chain, skippedReason := r.chain(req)
	deadline := time.Now().Add(r.budget)

	var failures []error
	var preferredFailure string
	for _, engine := range chain {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Result{}, fmt.Errorf("websearch: search timed out after %s", r.budget)
		}
		timeout := min(remaining, perEngineCap)
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		sources, err := engine.Search(attemptCtx, req)
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			failures = append(failures, engineFailure(engine.ID(), err))
			if engine.ID() == r.preferred {
				preferredFailure = err.Error()
			}
			continue
		}
		if len(sources) == 0 {
			failures = append(failures, engineFailure(engine.ID(), errors.New("returned 0 results")))
			if engine.ID() == r.preferred {
				preferredFailure = "returned 0 results"
			}
			continue
		}

		sources = dedupAndClean(sources)
		if req.MaxResults > 0 && len(sources) > req.MaxResults {
			sources = sources[:req.MaxResults]
		}

		var note string
		switch {
		case engine.ID() == r.preferred && (req.TimeRange == nil || engine.SupportsTimeRange()):
			// Preferred engine succeeded with the filter honored; nothing
			// to explain.
		case req.TimeRange != nil && !engine.SupportsTimeRange():
			note = fmt.Sprintf("Note: %s does not support time filtering, so time_range=%s was NOT applied; results may include older content.", engine.ID(), req.TimeRange.String())
		case skippedReason != "":
			note = fmt.Sprintf("Note: %s, using %s.", skippedReason, engine.ID())
		case preferredFailure != "":
			note = fmt.Sprintf("Note: %s unavailable or failed (%s), using %s.", r.preferred, preferredFailure, engine.ID())
		default:
			note = fmt.Sprintf("Note: %s unavailable or failed, using %s.", r.preferred, engine.ID())
		}
		return Result{Sources: sources, Engine: engine.ID(), Note: note}, nil
	}

	if len(failures) == 0 {
		return Result{}, errors.New("websearch: all search engines failed")
	}
	return Result{}, fmt.Errorf("websearch: all search engines failed:\n%w", errors.Join(failures...))
}

// engineFailure attributes err to the engine that produced it. Engine
// errors usually carry their own "id: " prefix already; the rest (such
// as the shared DuckDuckGo rate-limit error) would otherwise be
// anonymous in a multi-engine failure list.
func engineFailure(id string, err error) error {
	if strings.HasPrefix(err.Error(), id+":") {
		return err
	}
	return fmt.Errorf("%s: %w", id, err)
}

// dedupAndClean cleans each source's snippet and drops later sources
// whose URL normalizes to one already seen, preserving first-seen
// order.
func dedupAndClean(sources []Source) []Source {
	seen := make(map[string]bool, len(sources))
	out := make([]Source, 0, len(sources))
	for _, s := range sources {
		if s.Snippet != "" {
			s.Snippet = cleanSnippet(s.Snippet)
		}
		key := normalizeURL(s.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
