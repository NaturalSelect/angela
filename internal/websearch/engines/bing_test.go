package engines

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestBingEngine_ParsesResultsAndUsesMarket(t *testing.T) {
	t.Parallel()
	var gotMarket, gotAcceptLanguage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMarket = r.URL.Query().Get("mkt")
		gotAcceptLanguage = r.Header.Get("Accept-Language")
		_, _ = w.Write([]byte(`<html><body><ol id="b_results">
<li class="b_algo">
<h2><a href="https://example.com/golang">Learn Go Programming</a></h2>
<p>Go is an open source programming language.</p>
</li>
</ol></body></html>`))
	}))
	defer srv.Close()

	e := &bingEngine{client: http.DefaultClient, market: "en-US", endpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.NoError(t, err)
	require.Equal(t, "en-US", gotMarket)
	require.Equal(t, "en-US,en;q=0.9", gotAcceptLanguage)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/golang", sources[0].URL)
	require.Equal(t, "Learn Go Programming", sources[0].Title)
	require.Equal(t, "Go is an open source programming language.", sources[0].Snippet)
}

func TestBingEngine_RespectsMaxResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><ol>
<li class="b_algo"><h2><a href="https://example.com/go1">Go one</a></h2><p>go snippet one</p></li>
<li class="b_algo"><h2><a href="https://example.com/go2">Go two</a></h2><p>go snippet two</p></li>
</ol></body></html>`))
	}))
	defer srv.Close()

	e := &bingEngine{client: http.DefaultClient, market: "en-US", endpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "go", MaxResults: 1})
	require.NoError(t, err)
	require.Len(t, sources, 1)
}

func TestBingEngine_FiltersNoResultsPageWithNoTokenOverlap(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><ol>
<li class="b_algo">
<h2><a href="https://foo.example/bar">Something else entirely</a></h2>
<p>Nothing matches here.</p>
</li>
</ol></body></html>`))
	}))
	defer srv.Close()

	e := &bingEngine{client: http.DefaultClient, market: "en-US", endpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "unrelated xyz123query"})
	require.NoError(t, err)
	require.Empty(t, sources)
}

func TestBingEngine_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := &bingEngine{client: http.DefaultClient, market: "en-US", endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
	require.Error(t, err)
}

func TestBingEngine_SupportsTimeRangeIsFalse(t *testing.T) {
	t.Parallel()
	e := newBingEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "bing", e.ID())
	require.False(t, e.SupportsTimeRange())
}
