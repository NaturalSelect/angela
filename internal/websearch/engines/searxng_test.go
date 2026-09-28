package engines

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestSearxngEngine_ParsesResultsFromFirstInstance(t *testing.T) {
	t.Parallel()
	var gotTimeRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTimeRange = r.URL.Query().Get("time_range")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/a","title":"A","content":"about a"}]}`))
	}))
	defer srv.Close()

	e := &searxngEngine{client: http.DefaultClient, instances: []string{srv.URL}}
	sources, err := e.Search(context.Background(), websearch.Request{
		Query:     "golang",
		TimeRange: &websearch.TimeRange{Days: 1},
	})
	require.NoError(t, err)
	require.Equal(t, "day", gotTimeRange)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/a", sources[0].URL)
	require.Equal(t, "A", sources[0].Title)
	require.Equal(t, "about a", sources[0].Snippet)
}

func TestSearxngEngine_FallsBackToNextInstanceOnFailure(t *testing.T) {
	t.Parallel()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/b","title":"B","content":"about b"}]}`))
	}))
	defer alive.Close()

	e := &searxngEngine{client: http.DefaultClient, instances: []string{dead.URL, alive.URL}}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/b", sources[0].URL)
}

func TestSearxngEngine_ErrorsWithAggregatedFailuresWhenAllFail(t *testing.T) {
	t.Parallel()
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv2.Close()

	e := &searxngEngine{client: http.DefaultClient, instances: []string{srv1.URL, srv2.URL}}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
	require.ErrorContains(t, err, srv1.URL)
	require.ErrorContains(t, err, srv2.URL)
}

func TestSearxngEngine_SupportsTimeRangeIsTrue(t *testing.T) {
	t.Parallel()
	e := newSearxngEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "searxng", e.ID())
	require.True(t, e.SupportsTimeRange())
	require.Equal(t, defaultSearxngInstances, e.instances)
}
