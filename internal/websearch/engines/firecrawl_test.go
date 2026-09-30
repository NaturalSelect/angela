package engines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestFirecrawlEngine_ParsesResults(t *testing.T) {
	t.Parallel()
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"web":[{"url":"https://example.com/a","title":"A","description":"about a"}]}}`))
	}))
	defer srv.Close()

	e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL, apiKey: "test-key"}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Equal(t, "Bearer test-key", gotAuth)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/a", sources[0].URL)
	require.Equal(t, "A", sources[0].Title)
	require.Equal(t, "about a", sources[0].Snippet)
}

func TestFirecrawlEngine_SetsFixedTierTBS(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"web":[]}}`))
	}))
	defer srv.Close()

	e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{
		Query:     "golang",
		TimeRange: &websearch.TimeRange{Days: 1},
	})
	require.NoError(t, err)
	require.Equal(t, "qdr:d", gotBody["tbs"])
}

func TestFirecrawlEngine_SetsAbsoluteDateTBS(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"web":[]}}`))
	}))
	defer srv.Close()

	e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL}
	after := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	_, err := e.Search(context.Background(), websearch.Request{
		Query:     "golang",
		TimeRange: &websearch.TimeRange{After: after},
	})
	require.NoError(t, err)
	require.Equal(t, "cdr:1,cd_min:3/4/2026", gotBody["tbs"])
}

func TestFirecrawlEngine_ClampsLimit(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"web":[]}}`))
	}))
	defer srv.Close()

	e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang", MaxResults: 100})
	require.NoError(t, err)
	require.Equal(t, float64(10), gotBody["limit"])
}

func TestFirecrawlEngine_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
}

func TestFirecrawlEngine_RejectedWithoutKeyIsNotConfigured(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		defer srv.Close()

		e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL}
		_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
		require.ErrorIs(t, err, websearch.ErrNotConfigured)
	}
}

func TestFirecrawlEngine_RejectedWithKeyIsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := &firecrawlEngine{client: http.DefaultClient, endpoint: srv.URL, apiKey: "bad-key"}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
	require.NotErrorIs(t, err, websearch.ErrNotConfigured)
}

func TestFirecrawlEngine_SupportsTimeRangeIsTrue(t *testing.T) {
	t.Parallel()
	e := newFirecrawlEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "firecrawl", e.ID())
	require.True(t, e.SupportsTimeRange())
}
