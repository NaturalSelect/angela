package engines

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestDDGHTMLEngine_ParsesResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirect := "//duckduckgo.com/l/?uddg=" + url.QueryEscape("https://example.com/post") + "&rut=1"
		_, _ = w.Write([]byte(`<html><body>
<div class="result results_links results_links_deep">
  <div class="links_main links_deep">
    <a rel="nofollow" class="result__a" href="` + redirect + `">Example Post</a>
    <a class="result__snippet" href="` + redirect + `">A snippet about the example post.</a>
  </div>
</div>
</body></html>`))
	}))
	defer srv.Close()

	e := &ddgHTMLEngine{client: http.DefaultClient, endpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "example"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/post", sources[0].URL)
	require.Equal(t, "Example Post", sources[0].Title)
	require.Equal(t, "A snippet about the example post.", sources[0].Snippet)
}

func TestDDGHTMLEngine_SetsDfForTimeRange(t *testing.T) {
	t.Parallel()
	var gotDf string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDf = r.URL.Query().Get("df")
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer srv.Close()

	e := &ddgHTMLEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "example", TimeRange: &websearch.TimeRange{Days: 7}})
	require.NoError(t, err)
	require.Equal(t, "w", gotDf)
}

func TestDDGHTMLEngine_RateLimitedOn202(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	e := &ddgHTMLEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "example"})
	require.ErrorIs(t, err, errDDGRateLimited)
}

func TestDDGHTMLEngine_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := &ddgHTMLEngine{client: http.DefaultClient, endpoint: srv.URL}
	_, err := e.Search(context.Background(), websearch.Request{Query: "example"})
	require.Error(t, err)
}

func TestDDGHTMLEngine_SupportsTimeRangeIsTrue(t *testing.T) {
	t.Parallel()
	e := newDDGHTMLEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "ddg", e.ID())
	require.True(t, e.SupportsTimeRange())
}
