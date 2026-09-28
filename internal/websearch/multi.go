package websearch

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

// MergedSource is one Source cross-referenced against the engines that
// returned it.
type MergedSource struct {
	Source
	SeenIn []string
}

// MultiResult is the outcome of SearchMany: the merged, ranked sources
// plus which engines contributed, failed, or were skipped (missing a
// credential).
type MultiResult struct {
	Sources []MergedSource
	Used    []string
	Failed  []string
	Skipped []string
}

// searchManyDefaultCount is how many engines SearchMany queries when
// engineIDs is empty: the front of the Router's fallback order.
const searchManyDefaultCount = 3

// SearchMany queries engineIDs concurrently and merges their sources by
// normalized URL, ranking sources multiple engines agree on above
// single-source ones. An empty engineIDs uses the first three ids of
// the fallback chain (EngineIDs). Unknown ids are silently dropped;
// SearchMany fails only when none remain.
func (r *Router) SearchMany(ctx context.Context, req Request, engineIDs []string) (MultiResult, error) {
	if strings.TrimSpace(req.Query) == "" {
		return MultiResult{}, errors.New("websearch: query is required")
	}

	var targets []Engine
	if len(engineIDs) == 0 {
		for _, id := range r.EngineIDs() {
			if len(targets) == searchManyDefaultCount {
				break
			}
			targets = append(targets, r.byID[id])
		}
	} else {
		for _, id := range engineIDs {
			if e, ok := r.byID[id]; ok {
				targets = append(targets, e)
			}
		}
	}
	if len(targets) == 0 {
		return MultiResult{}, errors.New("websearch: no valid engines selected")
	}

	type outcome struct {
		id      string
		sources []Source
		err     error
	}
	outcomes := make([]outcome, len(targets))
	var wg sync.WaitGroup
	for i, engine := range targets {
		wg.Add(1)
		go func(i int, engine Engine) {
			defer wg.Done()
			attemptCtx, cancel := context.WithTimeout(ctx, perEngineCap)
			defer cancel()
			sources, err := engine.Search(attemptCtx, req)
			outcomes[i] = outcome{id: engine.ID(), sources: sources, err: err}
		}(i, engine)
	}
	wg.Wait()

	type merged struct {
		source Source
		seenIn []string
		order  int
	}
	byURL := make(map[string]*merged)
	var order []string
	var used, failed, skipped []string

	for _, o := range outcomes {
		if o.err != nil {
			if errors.Is(o.err, ErrNotConfigured) {
				skipped = append(skipped, o.id)
			} else {
				failed = append(failed, o.id+" ("+o.err.Error()+")")
			}
			continue
		}
		if len(o.sources) == 0 {
			continue
		}
		used = append(used, o.id)
		for _, s := range o.sources {
			if s.URL == "" {
				continue
			}
			key := normalizeURL(s.URL)
			if m, ok := byURL[key]; ok {
				m.seenIn = append(m.seenIn, o.id)
				if m.source.Title == "" {
					m.source.Title = s.Title
				}
				if m.source.Snippet == "" {
					m.source.Snippet = s.Snippet
				}
				if m.source.PublishedAt == "" {
					m.source.PublishedAt = s.PublishedAt
				}
				continue
			}
			byURL[key] = &merged{source: s, seenIn: []string{o.id}, order: len(order)}
			order = append(order, key)
		}
	}

	list := make([]*merged, 0, len(order))
	for _, key := range order {
		list = append(list, byURL[key])
	}
	sort.SliceStable(list, func(i, j int) bool {
		if len(list[i].seenIn) != len(list[j].seenIn) {
			return len(list[i].seenIn) > len(list[j].seenIn)
		}
		return list[i].order < list[j].order
	})

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 8
	}
	if len(list) > maxResults {
		list = list[:maxResults]
	}

	sources := make([]MergedSource, len(list))
	for i, m := range list {
		src := m.source
		if src.Snippet != "" {
			src.Snippet = cleanSnippet(src.Snippet)
		}
		sources[i] = MergedSource{Source: src, SeenIn: m.seenIn}
	}

	return MultiResult{Sources: sources, Used: used, Failed: failed, Skipped: skipped}, nil
}
