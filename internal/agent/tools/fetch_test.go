package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func fetchSessionCtx() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "fetch-session")
}

func TestFetchToolValidatesParamsBeforeNetworkAccess(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(t.TempDir(), http.DefaultClient)

	tests := []struct {
		name    string
		params  FetchParams
		wantMsg string
	}{
		{
			name:    "missing URL",
			params:  FetchParams{Format: "text"},
			wantMsg: "URL parameter is required",
		},
		{
			name:    "invalid format",
			params:  FetchParams{URL: "http://example.com", Format: "xml"},
			wantMsg: "Format must be one of: text, markdown, html",
		},
		{
			name:    "non-http(s) URL",
			params:  FetchParams{URL: "ftp://example.com/file", Format: "text"},
			wantMsg: "URL must start with http:// or https://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(tt.params)
			require.NoError(t, err)
			resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, tt.wantMsg)
		})
	}
}

func TestFetchToolRequiresSessionID(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(srv.Close)

	tool := NewFetchTool(t.TempDir(), srv.Client())
	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
	require.NoError(t, err)

	_, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
}

func TestFetchToolNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	tool := NewFetchTool(t.TempDir(), srv.Client())
	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
	require.NoError(t, err)

	resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "status code: 404")
}

func TestFetchToolInvalidUTF8(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte{0xff, 0xfe, 0xfd})
	}))
	t.Cleanup(srv.Close)

	tool := NewFetchTool(t.TempDir(), srv.Client())
	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
	require.NoError(t, err)

	resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not valid UTF-8")
}

func TestFetchToolTextFormat(t *testing.T) {
	t.Parallel()

	t.Run("plain text content is returned verbatim", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello there"))
		}))
		t.Cleanup(srv.Close)

		tool := NewFetchTool(t.TempDir(), srv.Client())
		input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
		require.NoError(t, err)

		resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, "hello there", resp.Content)
	})

	t.Run("html content is reduced to visible text", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><h1>Title</h1><p>Some paragraph text.</p></body></html>`))
		}))
		t.Cleanup(srv.Close)

		tool := NewFetchTool(t.TempDir(), srv.Client())
		input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
		require.NoError(t, err)

		resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Contains(t, resp.Content, "Title")
		require.Contains(t, resp.Content, "Some paragraph text.")
		require.NotContains(t, resp.Content, "<h1>")
	})
}

func TestFetchToolMarkdownFormat(t *testing.T) {
	t.Parallel()

	t.Run("html is converted and fenced", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><h1>Title</h1></body></html>`))
		}))
		t.Cleanup(srv.Close)

		tool := NewFetchTool(t.TempDir(), srv.Client())
		input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "markdown"})
		require.NoError(t, err)

		resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.True(t, strings.HasPrefix(resp.Content, "```\n"))
		require.True(t, strings.HasSuffix(resp.Content, "\n```"))
		require.Contains(t, resp.Content, "Title")
	})

	t.Run("non-html content is fenced as-is", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("raw content"))
		}))
		t.Cleanup(srv.Close)

		tool := NewFetchTool(t.TempDir(), srv.Client())
		input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "markdown"})
		require.NoError(t, err)

		resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, "```\nraw content\n```", resp.Content)
	})
}

func TestFetchToolHTMLFormat(t *testing.T) {
	t.Parallel()

	t.Run("body is extracted and wrapped", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>x</title></head><body><p>hi</p></body></html>`))
		}))
		t.Cleanup(srv.Close)

		tool := NewFetchTool(t.TempDir(), srv.Client())
		input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "html"})
		require.NoError(t, err)

		resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.True(t, strings.HasPrefix(resp.Content, "<html>\n<body>\n"))
		require.True(t, strings.HasSuffix(resp.Content, "\n</body>\n</html>"))
		require.Contains(t, resp.Content, "<p>hi</p>")
		require.NotContains(t, resp.Content, "<title>")
	})

	t.Run("empty body produces an error response", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head></head><body></body></html>`))
		}))
		t.Cleanup(srv.Close)

		tool := NewFetchTool(t.TempDir(), srv.Client())
		input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "html"})
		require.NoError(t, err)

		resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "No body content found in HTML")
	})
}

func TestFetchToolTruncatesOversizedContent(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", MaxFetchSize+500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	tool := NewFetchTool(t.TempDir(), srv.Client())
	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
	require.NoError(t, err)

	resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "[Content truncated to 102400 bytes]")
}

func TestFetchToolClampsExcessiveTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	tool := NewFetchTool(t.TempDir(), srv.Client())
	// A timeout above the 120s cap must be clamped rather than rejected;
	// the server responds instantly so the clamp itself is never waited out.
	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text", Timeout: 99999})
	require.NoError(t, err)

	resp, err := tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "ok", resp.Content)
}

func TestFetchToolTimeoutCancelsSlowRequest(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	tool := NewFetchTool(t.TempDir(), srv.Client())
	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text", Timeout: 1})
	require.NoError(t, err)

	start := time.Now()
	_, err = tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch URL")
	require.Less(t, time.Since(start), 10*time.Second)
}

func TestFetchToolRequestCreationError(t *testing.T) {
	t.Parallel()

	tool := NewFetchTool(t.TempDir(), http.DefaultClient)
	// A control character in the URL fails url.Parse inside
	// http.NewRequestWithContext, before any network I/O happens.
	input, err := json.Marshal(FetchParams{URL: "http://example.com/\x7f", Format: "text"})
	require.NoError(t, err)

	_, err = tool.Run(fetchSessionCtx(), fantasy.ToolCall{ID: "1", Name: toolnames.Fetch, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create request")
}

func TestExtractTextFromHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		html        string
		wantContain []string
		wantExact   string
	}{
		{
			name:      "empty input",
			html:      "",
			wantExact: "",
		},
		{
			name:        "headings, links and lists collapse to visible text",
			html:        `<html><body><h1>Heading</h1><a href="/x">Link</a><ul><li>One</li><li>Two</li></ul></body></html>`,
			wantContain: []string{"Heading", "Link", "One", "Two"},
		},
		{
			name:        "malformed tags do not error and still extract text",
			html:        `<html><body><p>Broken<div>content</p></body>`,
			wantContain: []string{"Broken", "content"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extractTextFromHTML(tt.html)
			require.NoError(t, err)
			if tt.wantExact != "" || tt.html == "" {
				require.Equal(t, tt.wantExact, got)
			}
			for _, want := range tt.wantContain {
				require.Contains(t, got, want)
			}
			require.NotContains(t, got, "<")
		})
	}
}

func TestConvertHTMLToMarkdownFetch(t *testing.T) {
	t.Parallel()

	got, err := convertHTMLToMarkdown(`<html><body><h1>Title</h1><p>Body text.</p></body></html>`)
	require.NoError(t, err)
	require.Contains(t, got, "Title")
	require.Contains(t, got, "Body text.")
}
