package engines

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestBingEngine_DecodesRedirectLinks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><ol>
<li class="b_algo">
<h2><a href="https://www.bing.com/ck/a?!&amp;&amp;p=abc123&amp;u=a1aHR0cHM6Ly9nby5kZXYv&amp;ntb=1">The Go Programming Language</a></h2>
<p>Go is an open source programming language.</p>
</li>
</ol></body></html>`))
	}))
	defer srv.Close()

	e := &bingEngine{client: http.DefaultClient, market: "en-US", endpoint: srv.URL}
	sources, err := e.Search(context.Background(), websearch.Request{Query: "go programming language"})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, "https://go.dev/", sources[0].URL)

	// NOTE: Identical strings normalize identically, so Bing and DuckDuckGo
	// results for one page merge into a single source.
	ddgURL := decodeDDGRedirect("//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F&rut=abc")
	require.Equal(t, ddgURL, sources[0].URL)
}

func TestDecodeBingRedirect(t *testing.T) {
	t.Parallel()
	wrap := func(target string) string {
		return "https://www.bing.com/ck/a?!&&p=abc&u=a1" + base64.RawURLEncoding.EncodeToString([]byte(target)) + "&ntb=1"
	}
	paddedTarget := base64.URLEncoding.EncodeToString([]byte("https://go.dev/a"))

	tests := []struct {
		name string
		href string
		want string
	}{
		{"decodes target", "https://www.bing.com/ck/a?!&&p=abc&u=a1aHR0cHM6Ly9nby5kZXYv&ntb=1", "https://go.dev/"},
		{"keeps target query string", wrap("https://pkg.go.dev/search?q=context&m=symbol"), "https://pkg.go.dev/search?q=context&m=symbol"},
		{"accepts padded base64", "https://www.bing.com/ck/a?u=a1" + paddedTarget, "https://go.dev/a"},
		{"accepts regional host", strings.Replace(wrap("https://go.dev/"), "www.bing.com", "cn.bing.com", 1), "https://go.dev/"},
		{"direct link unchanged", "https://example.com/golang", "https://example.com/golang"},
		{"other bing path unchanged", "https://www.bing.com/search?q=go&u=a1aHR0cHM6Ly9nby5kZXYv", "https://www.bing.com/search?q=go&u=a1aHR0cHM6Ly9nby5kZXYv"},
		{"missing u unchanged", "https://www.bing.com/ck/a?p=abc", "https://www.bing.com/ck/a?p=abc"},
		{"missing version prefix unchanged", "https://www.bing.com/ck/a?u=aHR0cHM6Ly9nby5kZXYv", "https://www.bing.com/ck/a?u=aHR0cHM6Ly9nby5kZXYv"},
		{"invalid base64 unchanged", "https://www.bing.com/ck/a?u=a1***", "https://www.bing.com/ck/a?u=a1***"},
		{"non-http target unchanged", wrap("javascript:alert(1)"), wrap("javascript:alert(1)")},
		{"other host unchanged", "https://evil.example/ck/a?u=a1aHR0cHM6Ly9nby5kZXYv", "https://evil.example/ck/a?u=a1aHR0cHM6Ly9nby5kZXYv"},
		{"lookalike host unchanged", "https://notbing.com/ck/a?u=a1aHR0cHM6Ly9nby5kZXYv", "https://notbing.com/ck/a?u=a1aHR0cHM6Ly9nby5kZXYv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, decodeBingRedirect(tt.href))
		})
	}
}
