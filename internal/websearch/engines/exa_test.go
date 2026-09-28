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

func TestExaEngine_MCPPathParsesResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json, text/event-stream", r.Header.Get("Accept"))
		var rpc struct {
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Query      string `json:"query"`
					Type       string `json:"type"`
					NumResults int    `json:"numResults"`
					Livecrawl  string `json:"livecrawl"`
				} `json:"arguments"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&rpc))
		require.Equal(t, "web_search_exa", rpc.Params.Name)
		require.Equal(t, "golang", rpc.Params.Arguments.Query)
		require.Equal(t, "auto", rpc.Params.Arguments.Type)
		require.Equal(t, "fallback", rpc.Params.Arguments.Livecrawl)

		text := "Title: Example\nURL: https://example.com/a\nPublished: 2026-01-02\nHighlights:\nfirst highlight\nsecond highlight"
		payload := map[string]any{
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			},
		}
		encoded, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: " + string(encoded) + "\n\n"))
	}))
	defer srv.Close()

	e := &exaEngine{client: http.DefaultClient, mcpEndpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/a", sources[0].URL)
	require.Equal(t, "Example", sources[0].Title)
	require.Equal(t, "2026-01-02", sources[0].PublishedAt)
	require.Equal(t, "first highlight second highlight", sources[0].Snippet)
	require.False(t, e.SupportsTimeRange())
}

func TestExaEngine_MCPPathErrorsOnMissingDataLine(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not sse at all"))
	}))
	defer srv.Close()

	e := &exaEngine{client: http.DefaultClient, mcpEndpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
}

func TestExaEngine_RESTPathUsesKeyAndTimeRange(t *testing.T) {
	t.Parallel()
	var gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/b","title":"B","highlights":["a highlight"],"publishedDate":"2026-02-01"}]}`))
	}))
	defer srv.Close()

	e := &exaEngine{client: http.DefaultClient, restEndpoint: srv.URL, apiKey: "test-key"}
	require.True(t, e.SupportsTimeRange())

	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sources, err := e.Search(context.Background(), websearch.Request{
		Query:     "golang",
		TimeRange: &websearch.TimeRange{After: after},
	})
	require.NoError(t, err)
	require.Equal(t, "test-key", gotKey)
	require.Equal(t, "2026-01-01", gotBody["startPublishedDate"])
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/b", sources[0].URL)
	require.Equal(t, "a highlight", sources[0].Snippet)
	require.Equal(t, "2026-02-01", sources[0].PublishedAt)
}

func TestExaEngine_RESTPathHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := &exaEngine{client: http.DefaultClient, restEndpoint: srv.URL, apiKey: "bad-key"}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
}

func TestExaEngine_ID(t *testing.T) {
	t.Parallel()
	e := newExaEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "exa", e.ID())
}
