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

func TestAnysearchEngine_ParsesResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "golang", body["query"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"results":[
			{"url":"https://example.com/a","title":"A","snippet":"about a"}
		]}}`))
	}))
	defer srv.Close()

	e := &anysearchEngine{client: http.DefaultClient, endpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/a", sources[0].URL)
	require.Equal(t, "A", sources[0].Title)
	require.Equal(t, "about a", sources[0].Snippet)
}

func TestAnysearchEngine_RetriesAnonymouslyAfter401(t *testing.T) {
	t.Parallel()
	var authHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"results":[{"url":"https://example.com/anon","title":"Anon"}]}}`))
	}))
	defer srv.Close()

	e := &anysearchEngine{client: http.DefaultClient, endpoint: srv.URL, apiKey: "bad-key"}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/anon", sources[0].URL)
	require.Len(t, authHeaders, 2)
	require.Equal(t, "Bearer bad-key", authHeaders[0])
	require.Empty(t, authHeaders[1])
}

func TestAnysearchEngine_ErrorsOnNonZeroCode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":42,"message":"boom"}`))
	}))
	defer srv.Close()

	e := &anysearchEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.ErrorContains(t, err, "boom")
}

func TestAnysearchEngine_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := &anysearchEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
}

func TestAnysearchEngine_SupportsTimeRangeIsFalse(t *testing.T) {
	t.Parallel()
	e := newAnysearchEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "anysearch", e.ID())
	require.False(t, e.SupportsTimeRange())
}
