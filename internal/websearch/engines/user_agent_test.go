package engines

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/browserhttp"
	"github.com/NaturalSelect/angela/internal/websearch"
)

func TestBrowserEnginesSendTheClientHelloUserAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		search func(endpoint string) error
	}{
		{"bing", func(endpoint string) error {
			e := &bingEngine{client: http.DefaultClient, market: defaultBingMarket, endpoint: endpoint}
			_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
			return err
		}},
		{"ddg html", func(endpoint string) error {
			e := &ddgHTMLEngine{client: http.DefaultClient, endpoint: endpoint}
			_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
			return err
		}},
		{"ddg lite", func(endpoint string) error {
			e := newDDGLiteTestEngine(endpoint + "/lite/?q=")
			_, err := e.Search(context.Background(), websearch.Request{Query: "golang"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotUserAgents := make(chan string, 8)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotUserAgents <- r.Header.Get("User-Agent")
				_, _ = w.Write([]byte(`<html><body></body></html>`))
			}))
			t.Cleanup(srv.Close)

			// NOTE: Results are not asserted; an empty page may legitimately
			// come back as an error, and only the request headers matter here.
			_ = tt.search(srv.URL)

			require.Equal(t, browserhttp.ChromeUserAgent, <-gotUserAgents)
		})
	}
}
