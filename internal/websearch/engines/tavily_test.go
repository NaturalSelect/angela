package engines

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestTavilyEngine_ParsesResultsWithKey(t *testing.T) {
	t.Parallel()
	var gotAuth, gotKeylessHeader string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKeylessHeader = r.Header.Get("x-tavily-access-mode")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/a","title":"A","content":"about a"}]}`))
	}))
	defer srv.Close()

	e := &tavilyEngine{client: http.DefaultClient, endpoint: srv.URL, apiKey: "test-key"}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Equal(t, "Bearer test-key", gotAuth)
	require.Empty(t, gotKeylessHeader)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/a", sources[0].URL)
	require.Equal(t, "A", sources[0].Title)
	require.Equal(t, "about a", sources[0].Snippet)
}

func TestTavilyEngine_KeylessModeSetsHeader(t *testing.T) {
	t.Parallel()
	var gotKeylessHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeylessHeader = r.Header.Get("x-tavily-access-mode")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	e := &tavilyEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Equal(t, "keyless", gotKeylessHeader)
}

func TestTavilyEngine_SetsTimeRangeTier(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	e := &tavilyEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{
		Query:     "golang",
		TimeRange: &websearch.TimeRange{Days: 30},
	})
	require.NoError(t, err)
	require.Equal(t, "month", gotBody["time_range"])
}

func TestTavilyEngine_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := &tavilyEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
}

func TestTavilyEngine_SupportsTimeRangeIsTrue(t *testing.T) {
	t.Parallel()
	e := newTavilyEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "tavily", e.ID())
	require.True(t, e.SupportsTimeRange())
}
