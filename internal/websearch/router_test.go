package websearch

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeEngine struct {
	id         string
	supportsTR bool
	searchFn   func(ctx context.Context, req Request) ([]Source, error)
}

func (e *fakeEngine) ID() string              { return e.id }
func (e *fakeEngine) SupportsTimeRange() bool { return e.supportsTR }
func (e *fakeEngine) Search(ctx context.Context, req Request) ([]Source, error) {
	return e.searchFn(ctx, req)
}

func okEngine(id string, sources ...Source) *fakeEngine {
	return &fakeEngine{id: id, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
		return sources, nil
	}}
}

func errEngine(id string, err error) *fakeEngine {
	return &fakeEngine{id: id, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
		return nil, err
	}}
}

func emptyEngine(id string) *fakeEngine {
	return &fakeEngine{id: id, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
		return nil, nil
	}}
}

func TestRouter_PreferredSucceeds(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing", Source{URL: "https://a.example"}),
		okEngine("ddg", Source{URL: "https://b.example"}),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go"})
	require.NoError(t, err)
	require.Equal(t, "bing", res.Engine)
	require.Empty(t, res.Note)
	require.Len(t, res.Sources, 1)
}

func TestRouter_FallsBackAndNotesFailure(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		errEngine("bing", errors.New("rate limited")),
		okEngine("ddg", Source{URL: "https://b.example"}),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go"})
	require.NoError(t, err)
	require.Equal(t, "ddg", res.Engine)
	require.Contains(t, res.Note, "bing unavailable or failed (rate limited), using ddg")
}

func TestRouter_ZeroResultsFallsBack(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		emptyEngine("bing"),
		okEngine("ddg", Source{URL: "https://b.example"}),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go"})
	require.NoError(t, err)
	require.Equal(t, "ddg", res.Engine)
	require.Contains(t, res.Note, "returned 0 results")
}

func TestRouter_NotConfiguredSkipped(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		errEngine("exa", fmt.Errorf("exa: %w", ErrNotConfigured)),
		okEngine("bing", Source{URL: "https://b.example"}),
	}, "exa", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go"})
	require.NoError(t, err)
	require.Equal(t, "bing", res.Engine)
}

func TestRouter_TimeRangeReordersChain(t *testing.T) {
	t.Parallel()
	var attempted []string
	trackedEngine := func(id string, supportsTR bool) *fakeEngine {
		return &fakeEngine{id: id, supportsTR: supportsTR, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
			attempted = append(attempted, id)
			return []Source{{URL: "https://" + id + ".example"}}, nil
		}}
	}
	r, err := NewRouter([]Engine{
		trackedEngine("bing", false),  // preferred, no time support
		trackedEngine("tavily", true), // supports time range
	}, "bing", 0)
	require.NoError(t, err)

	tr := &TimeRange{Days: 7}
	res, err := r.Search(context.Background(), Request{Query: "go", TimeRange: tr})
	require.NoError(t, err)
	require.Equal(t, "tavily", res.Engine)
	require.Contains(t, res.Note, "bing does not support time filtering")
	require.Equal(t, []string{"tavily"}, attempted) // bing never attempted
}

// TestRouter_TimeRangeFallsBackToUnfilteredEngineWithNote pins the
// last-resort path: when every time-aware engine fails, a time-scoped
// request may still be answered by a non-preferred engine that ignores
// the filter, and the Note must say so.
func TestRouter_TimeRangeFallsBackToUnfilteredEngineWithNote(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		&fakeEngine{id: "tavily", supportsTR: true, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
			return nil, errors.New("rate limited")
		}},
		&fakeEngine{id: "bing", supportsTR: false, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
			return []Source{{URL: "https://bing.example"}}, nil
		}},
	}, "tavily", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go", TimeRange: &TimeRange{Days: 7}})
	require.NoError(t, err)
	require.Equal(t, "bing", res.Engine)
	require.Contains(t, res.Note, "bing does not support time filtering")
	require.Contains(t, res.Note, "NOT applied")
}

// TestRouter_TimeRangeNoNoteWhenPreferredHonorsFilter pins that a
// time-scoped request answered by the preferred, filter-capable engine
// produces no note.
func TestRouter_TimeRangeNoNoteWhenPreferredHonorsFilter(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		&fakeEngine{id: "tavily", supportsTR: true, searchFn: func(ctx context.Context, req Request) ([]Source, error) {
			return []Source{{URL: "https://tavily.example"}}, nil
		}},
	}, "tavily", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go", TimeRange: &TimeRange{Days: 7}})
	require.NoError(t, err)
	require.Equal(t, "tavily", res.Engine)
	require.Empty(t, res.Note)
}

func TestRouter_AllEnginesFailReportsEveryFailure(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		errEngine("bing", errors.New("bing: HTTP 429")),
		errEngine("ddg", errors.New("DuckDuckGo is rate-limiting this machine. Do not retry or rephrase")),
		errEngine("exa", errors.New("exa: MCP error (HTTP 503)")),
		emptyEngine("searxng"),
	}, "bing", 0)
	require.NoError(t, err)

	_, err = r.Search(context.Background(), Request{Query: "go"})
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "all search engines failed")
	require.Contains(t, msg, "bing: HTTP 429")
	require.Contains(t, msg, "ddg: DuckDuckGo is rate-limiting this machine. Do not retry or rephrase")
	require.Contains(t, msg, "exa: MCP error (HTTP 503)")
	require.Contains(t, msg, "searxng: returned 0 results")
	require.NotContains(t, msg, "bing: bing:")
}

func TestRouter_BudgetExhausted(t *testing.T) {
	t.Parallel()
	slow := &fakeEngine{id: "bing", searchFn: func(ctx context.Context, req Request) ([]Source, error) {
		select {
		case <-time.After(50 * time.Millisecond):
			return []Source{{URL: "https://a.example"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	fast := okEngine("ddg", Source{URL: "https://b.example"})
	r, err := NewRouter([]Engine{slow, fast}, "bing", 10*time.Millisecond)
	require.NoError(t, err)

	_, err = r.Search(context.Background(), Request{Query: "go"})
	require.Error(t, err)
}

func TestRouter_DedupsAndTruncates(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing",
			Source{URL: "https://a.example/x", Title: "one"},
			Source{URL: "https://a.example/x/", Title: "dup"},
			Source{URL: "https://a.example/y", Title: "two"},
		),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.Search(context.Background(), Request{Query: "go", MaxResults: 1})
	require.NoError(t, err)
	require.Len(t, res.Sources, 1)
	require.Equal(t, "one", res.Sources[0].Title)
}

func TestNewRouter_RejectsDuplicateIDs(t *testing.T) {
	t.Parallel()
	_, err := NewRouter([]Engine{okEngine("bing"), okEngine("bing")}, "bing", 0)
	require.Error(t, err)
}

func TestNewRouter_RejectsUnknownPreferred(t *testing.T) {
	t.Parallel()
	_, err := NewRouter([]Engine{okEngine("bing")}, "ddg", 0)
	require.Error(t, err)
}

func TestNewRouter_RejectsEmptyEngines(t *testing.T) {
	t.Parallel()
	_, err := NewRouter(nil, "bing", 0)
	require.Error(t, err)
}
