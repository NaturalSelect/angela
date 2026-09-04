package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewDefaultHTTPClient(t *testing.T) {
	t.Parallel()

	client := newDefaultHTTPClient(42 * time.Second)
	require.Equal(t, 42*time.Second, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "expected an *http.Transport")
	require.Equal(t, 100, transport.MaxIdleConns)
	require.Equal(t, 10, transport.MaxIdleConnsPerHost)
	require.Equal(t, 90*time.Second, transport.IdleConnTimeout)
}

func TestFetchURLAndConvertNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	_, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code: 503")
}

func TestFetchURLAndConvertInvalidUTF8(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte{0xff, 0xfe})
	}))
	t.Cleanup(srv.Close)

	_, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not valid UTF-8")
}

func TestFetchURLAndConvertRequestCreationError(t *testing.T) {
	t.Parallel()

	_, err := FetchURLAndConvert(context.Background(), http.DefaultClient, "http://example.com/\x7f")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create request")
}

func TestFetchURLAndConvertHTMLStripsNoiseAndConverts(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>
			<nav>Nav links</nav>
			<script>var x = "hidden-script-text";</script>
			<h1>Real Title</h1>
			<p>Real paragraph.</p>
			<footer>Footer text</footer>
		</body></html>`))
	}))
	t.Cleanup(srv.Close)

	got, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)
	require.Contains(t, got, "Real Title")
	require.Contains(t, got, "Real paragraph.")
	require.NotContains(t, got, "Nav links")
	require.NotContains(t, got, "hidden-script-text")
	require.NotContains(t, got, "Footer text")
}

func TestFetchURLAndConvertFormatsJSON(t *testing.T) {
	t.Parallel()

	t.Run("valid JSON is pretty-printed", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"a":1,"b":[2,3]}`))
		}))
		t.Cleanup(srv.Close)

		got, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
		require.NoError(t, err)
		require.Contains(t, got, "\"a\": 1")
		require.Contains(t, got, "\n")
	})

	t.Run("text/json content type is also formatted", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/json")
			_, _ = w.Write([]byte(`{"x":true}`))
		}))
		t.Cleanup(srv.Close)

		got, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
		require.NoError(t, err)
		require.Contains(t, got, "\"x\": true")
	})

	t.Run("invalid JSON keeps original content", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not json`))
		}))
		t.Cleanup(srv.Close)

		got, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
		require.NoError(t, err)
		require.Equal(t, "not json", got)
	})
}

func TestFetchURLAndConvertPlainTextPassesThrough(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain body"))
	}))
	t.Cleanup(srv.Close)

	got, err := FetchURLAndConvert(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, "plain body", got)
}

func TestRemoveNoisyElements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		html      string
		wantGone  []string
		wantKeeps []string
	}{
		{
			name: "strips script style nav header footer aside noscript iframe svg",
			html: `<html><body>
				<script>bad1</script>
				<style>bad2{}</style>
				<nav>bad3</nav>
				<header>bad4</header>
				<footer>bad5</footer>
				<aside>bad6</aside>
				<noscript>bad7</noscript>
				<iframe>bad8</iframe>
				<svg><text>bad9</text></svg>
				<main>good content</main>
			</body></html>`,
			wantGone:  []string{"bad1", "bad2", "bad3", "bad4", "bad5", "bad6", "bad7", "bad8", "bad9"},
			wantKeeps: []string{"good content"},
		},
		{
			name:      "noisy elements nested inside other elements are still removed",
			html:      `<html><body><div><section><script>nested-bad</script><p>nested-good</p></section></div></body></html>`,
			wantGone:  []string{"nested-bad"},
			wantKeeps: []string{"nested-good"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := removeNoisyElements(tt.html)
			for _, gone := range tt.wantGone {
				require.NotContains(t, got, gone)
			}
			for _, keep := range tt.wantKeeps {
				require.Contains(t, got, keep)
			}
		})
	}
}

func TestCleanupMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "collapses three or more newlines to two",
			in:   "a\n\n\n\n\nb",
			want: "a\n\nb",
		},
		{
			name: "trims trailing whitespace per line",
			in:   "a   \nb\t\t\nc",
			want: "a\nb\nc",
		},
		{
			name: "trims leading and trailing whitespace of the whole content",
			in:   "\n\n  content  \n\n",
			want: "content",
		},
		{
			name: "leaves single blank lines alone",
			in:   "a\n\nb",
			want: "a\n\nb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, cleanupMarkdown(tt.in))
		})
	}
}

func TestConvertHTMLToMarkdownHelper(t *testing.T) {
	t.Parallel()

	got, err := ConvertHTMLToMarkdown(`<html><body><h2>Sub</h2><ul><li>item</li></ul></body></html>`)
	require.NoError(t, err)
	require.Contains(t, got, "Sub")
	require.Contains(t, got, "item")
}

func TestFormatJSON(t *testing.T) {
	t.Parallel()

	t.Run("valid JSON is indented", func(t *testing.T) {
		t.Parallel()
		got, err := FormatJSON(`{"nested":{"a":1},"list":[1,2,3]}`)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(got, "{\n"))
		require.Contains(t, got, "  \"nested\": {")
	})

	t.Run("invalid JSON returns an error", func(t *testing.T) {
		t.Parallel()
		_, err := FormatJSON(`{not valid`)
		require.Error(t, err)
	})

	t.Run("empty string is invalid JSON", func(t *testing.T) {
		t.Parallel()
		_, err := FormatJSON("")
		require.Error(t, err)
	})
}
