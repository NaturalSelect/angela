package engines

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/websearch"
)

// loadDDGAnomalyPage reads the real bot-check payload captured from
// lite.duckduckgo.com (HTTP 202, captcha modal), ported alongside the
// engine that needs it.
func loadDDGAnomalyPage(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/ddg_anomaly_202.html")
	require.NoError(t, err)
	return string(data)
}

func newDDGLiteTestEngine(endpoint string) *ddgLiteEngine {
	return &ddgLiteEngine{client: http.DefaultClient, endpoint: endpoint}
}

func TestDDGLiteEngine_ParsesResults(t *testing.T) {
	t.Parallel()
	page := `<html><body><table>
<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpost">Example Post</a></td></tr>
<tr><td class="result-snippet">A snippet about the example post.</td></tr>
</table></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	e := newDDGLiteTestEngine(srv.URL + "/lite/?q=")
	sources, err := e.Search(context.Background(), websearch.Request{Query: "example"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://example.com/post", sources[0].URL)
	require.Equal(t, "Example Post", sources[0].Title)
	require.Equal(t, "A snippet about the example post.", sources[0].Snippet)
}

func TestDDGLiteEngine_SetsDfForTimeRange(t *testing.T) {
	t.Parallel()
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.RawQuery
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer srv.Close()

	e := newDDGLiteTestEngine(srv.URL + "/lite/?q=")
	_, err := e.Search(context.Background(), websearch.Request{
		Query:     "example",
		TimeRange: &websearch.TimeRange{Days: 1},
	})
	require.NoError(t, err)
	require.Contains(t, gotURL, "df=d")
}

func TestDDGLiteEngine_RateLimitedOn202(t *testing.T) {
	t.Parallel()
	page := loadDDGAnomalyPage(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	e := newDDGLiteTestEngine(srv.URL + "/lite/?q=")
	_, err := e.Search(context.Background(), websearch.Request{Query: "anything"})
	require.ErrorIs(t, err, errDDGRateLimited)
}

func TestDDGLiteEngine_RateLimitedOnAnomalyPageWithHTTP200(t *testing.T) {
	t.Parallel()
	page := loadDDGAnomalyPage(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	e := newDDGLiteTestEngine(srv.URL + "/lite/?q=")
	_, err := e.Search(context.Background(), websearch.Request{Query: "anything"})
	require.ErrorIs(t, err, errDDGRateLimited)
}

func TestDDGLiteEngine_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := newDDGLiteTestEngine(srv.URL + "/lite/?q=")
	_, err := e.Search(context.Background(), websearch.Request{Query: "anything"})
	require.Error(t, err)
}

func TestDDGLiteEngine_SupportsTimeRangeIsTrue(t *testing.T) {
	t.Parallel()
	e := newDDGLiteEngine(Options{HTTPClient: http.DefaultClient})
	require.Equal(t, "ddg-lite", e.ID())
	require.True(t, e.SupportsTimeRange())
}
