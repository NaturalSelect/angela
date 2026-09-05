package log

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPRoundTripLogger(t *testing.T) {
	// Create a test server that returns a 500 error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "Internal server error", "code": 500}`))
	}))
	defer server.Close()

	// Create HTTP client with logging
	client := NewHTTPClient()

	// Make a request
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL,
		strings.NewReader(`{"test": "data"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Verify response
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status code 500, got %d", resp.StatusCode)
	}
}

func TestIdleTimeoutConn_TimesOutWhenNoBytesArrive(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	conn := &idleTimeoutConn{Conn: clientConn, timeout: 50 * time.Millisecond}

	start := time.Now()
	_, err := conn.Read(make([]byte, 16))
	elapsed := time.Since(start)

	require.Error(t, err)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout(), "expected a timeout error, got %v", err)
	require.Less(t, elapsed, time.Second, "an idle read must time out quickly, not hang")
}

func TestIdleTimeoutConn_PingResetsDeadline(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	conn := &idleTimeoutConn{Conn: clientConn, timeout: 80 * time.Millisecond}

	// Simulate an upstream that sends a keep-alive ping every 40ms, shorter
	// than the idle timeout, so surviving the loop below requires the
	// deadline to actually reset on each read rather than staying fixed at
	// the first call's start time.
	stopPings := make(chan struct{})
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := serverConn.Write([]byte("x")); err != nil {
					return
				}
			case <-stopPings:
				return
			}
		}
	}()

	buf := make([]byte, 16)
	deadline := time.Now().Add(300 * time.Millisecond)
	reads := 0
	for time.Now().Before(deadline) {
		_, err := conn.Read(buf)
		require.NoError(t, err, "a ping arriving before the deadline must reset it, not time it out")
		reads++
	}
	close(stopPings)
	require.Greater(t, reads, 1, "expected multiple pings to be read without a timeout")

	// Once pings stop, the connection must still be recognized as dead.
	_, err := conn.Read(buf)
	require.Error(t, err)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout(), "expected a timeout error, got %v", err)
}

func TestFormatHeaders(t *testing.T) {
	headers := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer secret-token"},
		"X-API-Key":     []string{"api-key-123"},
		"User-Agent":    []string{"test-agent"},
	}

	formatted := formatHeaders(headers)

	// Check that sensitive headers are redacted
	if formatted["Authorization"][0] != "[REDACTED]" {
		t.Error("Authorization header should be redacted")
	}
	if formatted["X-API-Key"][0] != "[REDACTED]" {
		t.Error("X-API-Key header should be redacted")
	}

	// Check that non-sensitive headers are preserved
	if formatted["Content-Type"][0] != "application/json" {
		t.Error("Content-Type header should be preserved")
	}
	if formatted["User-Agent"][0] != "test-agent" {
		t.Error("User-Agent header should be preserved")
	}
}
