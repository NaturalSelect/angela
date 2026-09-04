package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// sourcegraphRoundTripFunc adapts a function to http.RoundTripper, letting
// tests intercept requests without a real network round trip.
type sourcegraphRoundTripFunc func(*http.Request) (*http.Response, error)

func (f sourcegraphRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newSourcegraphTestClient returns an *http.Client that transparently
// redirects any request to target, regardless of the scheme/host baked
// into the request URL. NewSourcegraphTool always posts to a hardcoded
// "https://sourcegraph.com/.api/graphql" URL, so tests cannot point it at
// an httptest server through a caller-supplied base URL; redirecting at
// the transport layer is the only way to exercise it against a local
// fixture.
func newSourcegraphTestClient(t *testing.T, target *httptest.Server) *http.Client {
	t.Helper()
	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)

	return &http.Client{
		Transport: sourcegraphRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			redirected := req.Clone(req.Context())
			redirected.URL.Scheme = targetURL.Scheme
			redirected.URL.Host = targetURL.Host
			redirected.Host = ""
			return target.Client().Transport.RoundTrip(redirected)
		}),
	}
}

func TestFormatSourcegraphResults(t *testing.T) {
	t.Parallel()

	result := map[string]any{
		"data": map[string]any{
			"search": map[string]any{
				"results": map[string]any{
					"matchCount":  float64(2),
					"resultCount": float64(1),
					"limitHit":    true,
					"results": []any{
						map[string]any{
							"__typename": "FileMatch",
							"repository": map[string]any{"name": "owner/repo"},
							"file": map[string]any{
								"path":    "main.go",
								"url":     "https://example.com/owner/repo/main.go",
								"content": "package main\n\nfunc main() {\n\tprintln(\"match\")\n}\n",
							},
							"lineMatches": []any{
								map[string]any{
									"lineNumber": float64(4),
									"preview":    "\tprintln(\"match\")",
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := formatSourcegraphResults(result, 1, 10)
	require.NoError(t, err)
	require.Contains(t, got, "# Sourcegraph Search Results")
	require.Contains(t, got, "Found 2 matches across 1 results")
	require.Contains(t, got, "(Result limit reached, try a more specific query)")
	require.Contains(t, got, "## Result 1: owner/repo/main.go")
	require.Contains(t, got, "URL: https://example.com/owner/repo/main.go")
	require.Contains(t, got, "3| func main() {")
	require.Contains(t, got, "4|  \tprintln(\"match\")")
	require.Contains(t, got, "5| }")
}

func TestFormatSourcegraphResultsRespectsCount(t *testing.T) {
	t.Parallel()

	result := map[string]any{
		"data": map[string]any{
			"search": map[string]any{
				"results": map[string]any{
					"results": []any{
						map[string]any{
							"__typename": "FileMatch",
							"repository": map[string]any{"name": "owner/repo"},
							"file":       map[string]any{"path": "first.go"},
						},
						map[string]any{
							"__typename": "FileMatch",
							"repository": map[string]any{"name": "owner/repo"},
							"file":       map[string]any{"path": "second.go"},
						},
					},
				},
			},
		},
	}

	got, err := formatSourcegraphResults(result, 1, 1)
	require.NoError(t, err)
	require.Contains(t, got, "owner/repo/first.go")
	require.NotContains(t, got, "owner/repo/second.go")
}

func TestFormatSourcegraphResultsErrorsAndNoResults(t *testing.T) {
	t.Parallel()

	errorResult := map[string]any{
		"errors": []any{
			map[string]any{"message": "bad query"},
			map[string]any{"message": "timeout"},
		},
	}
	got, err := formatSourcegraphResults(errorResult, 1, 10)
	require.NoError(t, err)
	require.Equal(t, "## Sourcegraph API Error\n\n- bad query\n- timeout\n", got)

	noResult := map[string]any{
		"data": map[string]any{
			"search": map[string]any{
				"results": map[string]any{
					"matchCount":  float64(0),
					"resultCount": float64(0),
					"results":     []any{},
				},
			},
		},
	}
	got, err = formatSourcegraphResults(noResult, 1, 10)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(got, "No results found. Try a different query.\n"))
}

func TestSourcegraphSearchResultsMalformedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result map[string]any
		errMsg string
	}{
		{
			name:   "missing data field",
			result: map[string]any{},
			errMsg: "missing data field",
		},
		{
			name:   "missing search field",
			result: map[string]any{"data": map[string]any{}},
			errMsg: "missing search field",
		},
		{
			name:   "missing results field",
			result: map[string]any{"data": map[string]any{"search": map[string]any{}}},
			errMsg: "missing results field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := sourcegraphSearchResults(tt.result)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestFormatSourcegraphResultsPropagatesMalformedResponseError(t *testing.T) {
	t.Parallel()

	_, err := formatSourcegraphResults(map[string]any{"data": "not-a-map"}, 1, 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing data field")
}

func TestWriteSourcegraphErrorsEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("no errors field returns false and writes nothing", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		require.False(t, writeSourcegraphErrors(&buf, map[string]any{}))
		require.Empty(t, buf.String())
	})

	t.Run("empty errors slice returns false", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		require.False(t, writeSourcegraphErrors(&buf, map[string]any{"errors": []any{}}))
		require.Empty(t, buf.String())
	})

	t.Run("non-map and message-less entries are skipped", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		got := writeSourcegraphErrors(&buf, map[string]any{
			"errors": []any{
				"not-a-map",
				map[string]any{"noMessageField": true},
				map[string]any{"message": "real error"},
			},
		})
		require.True(t, got)
		require.Equal(t, "## Sourcegraph API Error\n\n- real error\n", buf.String())
	})
}

func TestFormatSourcegraphResultEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("non-map entry produces no output", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphResult(&buf, 0, "not-a-map", 5)
		require.Empty(t, buf.String())
	})

	t.Run("non-FileMatch typename produces no output", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphResult(&buf, 0, map[string]any{"__typename": "CommitMatch"}, 5)
		require.Empty(t, buf.String())
	})

	t.Run("missing repository produces no output", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphResult(&buf, 0, map[string]any{
			"__typename": "FileMatch",
			"file":       map[string]any{"path": "a.go"},
		}, 5)
		require.Empty(t, buf.String())
	})

	t.Run("missing file produces no output", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphResult(&buf, 0, map[string]any{
			"__typename": "FileMatch",
			"repository": map[string]any{"name": "acme/widgets"},
		}, 5)
		require.Empty(t, buf.String())
	})

	t.Run("missing URL omits the URL line", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphResult(&buf, 0, map[string]any{
			"__typename": "FileMatch",
			"repository": map[string]any{"name": "acme/widgets"},
			"file":       map[string]any{"path": "a.go"},
		}, 5)
		require.Contains(t, buf.String(), "## Result 1: acme/widgets/a.go")
		require.NotContains(t, buf.String(), "URL:")
	})
}

func TestFormatSourcegraphLineMatchEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("empty file content prints only the preview line", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphLineMatch(&buf, map[string]any{
			"lineNumber": float64(7),
			"preview":    "the matching line",
		}, "", 3)
		require.Equal(t, "```\n7| the matching line\n```\n\n", buf.String())
	})

	t.Run("match near the top of the file clamps the start line", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		fileContent := "l1\nl2\nl3\nl4\nl5\n"
		formatSourcegraphLineMatch(&buf, map[string]any{
			"lineNumber": float64(1),
			"preview":    "l1",
		}, fileContent, 3)
		got := buf.String()
		require.Contains(t, got, "1|  l1")
		require.Contains(t, got, "2| l2")
		require.NotContains(t, got, "0|")
	})

	t.Run("match near the bottom of the file clamps the end line", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		fileContent := "l1\nl2\nl3"
		formatSourcegraphLineMatch(&buf, map[string]any{
			"lineNumber": float64(3),
			"preview":    "l3",
		}, fileContent, 5)
		got := buf.String()
		require.Contains(t, got, "3|  l3")
		require.Contains(t, got, "2| l2")
		require.NotContains(t, got, "4|")
	})

	t.Run("non-map entries in lineMatches are skipped", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		formatSourcegraphLineMatches(&buf, []any{
			"not-a-map",
			map[string]any{"lineNumber": float64(1), "preview": "ok"},
		}, "", 1)
		require.Equal(t, "```\n1| ok\n```\n\n", buf.String())
	})
}

func TestNewSourcegraphToolEndToEnd(t *testing.T) {
	t.Parallel()

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Query string `json:"query"`
			} `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotQuery = body.Variables.Query

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {"search": {"results": {
				"matchCount": 1, "resultCount": 1, "limitHit": false,
				"results": [{
					"__typename": "FileMatch",
					"repository": {"name": "acme/widgets"},
					"file": {"path": "main.go", "url": "https://sourcegraph.com/acme/widgets/-/blob/main.go", "content": "line1\nline2\n"},
					"lineMatches": [{"lineNumber": 1, "preview": "line1"}]
				}]
			}}}
		}`))
	}))
	t.Cleanup(srv.Close)

	tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
	input, err := json.Marshal(SourcegraphParams{Query: "repo:acme/widgets needle"})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "repo:acme/widgets needle", gotQuery)
	require.Contains(t, resp.Content, "acme/widgets/main.go")
	require.Contains(t, resp.Content, "https://sourcegraph.com/acme/widgets/-/blob/main.go")
}

func TestNewSourcegraphToolRequiresQuery(t *testing.T) {
	t.Parallel()

	tool := NewSourcegraphTool(http.DefaultClient)
	input, err := json.Marshal(SourcegraphParams{})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Query parameter is required")
}

func TestNewSourcegraphToolClampsCountAboveMax(t *testing.T) {
	t.Parallel()

	var results []string
	for i := range 25 {
		results = append(results, `{
			"__typename": "FileMatch",
			"repository": {"name": "acme/widgets"},
			"file": {"path": "f`+itoa(i)+`.go"}
		}`)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"search": {"results": {
			"matchCount": 25, "resultCount": 25, "limitHit": true,
			"results": [` + strings.Join(results, ",") + `]
		}}}}`))
	}))
	t.Cleanup(srv.Close)

	tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
	input, err := json.Marshal(SourcegraphParams{Query: "needle", Count: 999})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, 20, strings.Count(resp.Content, "## Result"), "count above 20 must clamp to 20")
}

func TestNewSourcegraphToolNonOKStatus(t *testing.T) {
	t.Parallel()

	t.Run("with a response body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		t.Cleanup(srv.Close)

		tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
		input, err := json.Marshal(SourcegraphParams{Query: "needle"})
		require.NoError(t, err)

		resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "status code: 500, response: boom")
	})

	t.Run("with an empty response body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		t.Cleanup(srv.Close)

		tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
		input, err := json.Marshal(SourcegraphParams{Query: "needle"})
		require.NoError(t, err)

		resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Equal(t, "Request failed with status code: 502", resp.Content)
	})
}

func TestNewSourcegraphToolMalformedJSONResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	t.Cleanup(srv.Close)

	tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
	input, err := json.Marshal(SourcegraphParams{Query: "needle"})
	require.NoError(t, err)

	_, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal response")
}

func TestNewSourcegraphToolFormatErrorFromMalformedButValidJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unexpected": true}`))
	}))
	t.Cleanup(srv.Close)

	tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
	input, err := json.Marshal(SourcegraphParams{Query: "needle"})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Failed to format results:")
}

func TestNewSourcegraphToolClampsExcessiveTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"search": {"results": {"matchCount": 0, "resultCount": 0, "results": []}}}}`))
	}))
	t.Cleanup(srv.Close)

	tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
	// A timeout above the 120s cap must be clamped rather than rejected;
	// the server responds instantly so the clamp is never waited out.
	input, err := json.Marshal(SourcegraphParams{Query: "needle", Timeout: 99999})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
}

func TestNewSourcegraphToolTimeoutCancelsSlowRequest(t *testing.T) {
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

	tool := NewSourcegraphTool(newSourcegraphTestClient(t, srv))
	input, err := json.Marshal(SourcegraphParams{Query: "needle", Timeout: 1})
	require.NoError(t, err)

	start := time.Now()
	_, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Sourcegraph, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch URL")
	require.Less(t, time.Since(start), 10*time.Second)
}

// itoa avoids pulling in strconv just for a handful of test fixture ids.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
