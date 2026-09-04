package cmd

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/client"
	"github.com/stretchr/testify/require"
)

// closedTCPListener opens a real TCP listener and immediately closes it,
// returning an address guaranteed to refuse connections (unlike a
// made-up address, this cannot collide with another process's socket).
func closedTCPListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func TestReadinessHTTPClient_TCP(t *testing.T) {
	t.Parallel()

	hc, reqURL, err := readinessHTTPClient(&url.URL{Scheme: "tcp", Host: "127.0.0.1:9999"})
	require.NoError(t, err)
	require.NotNil(t, hc)
	require.Equal(t, "http://127.0.0.1:9999/v1/health", reqURL)
	require.False(t, hc.Transport.(*http.Transport).DisableCompression)
}

// TestReadinessHTTPClient_Unix covers the unix/npipe branch: the socket
// path itself is not a valid HTTP host, so the request URL must use the
// dummy host while the transport still dials the real socket.
func TestReadinessHTTPClient_Unix(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "server.sock")
	hc, reqURL, err := readinessHTTPClient(&url.URL{Scheme: "unix", Host: sockPath})
	require.NoError(t, err)
	require.NotNil(t, hc)
	require.Equal(t, "http://"+client.DummyHost+"/v1/health", reqURL)
	require.True(t, hc.Transport.(*http.Transport).DisableCompression)
}

func tcpHostURL(t *testing.T, srv *httptest.Server) *url.URL {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return &url.URL{Scheme: "tcp", Host: u.Host}
}

func TestProbeHealth_SuccessOn2xx(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	hostURL := tcpHostURL(t, srv)

	hc, reqURL, err := readinessHTTPClient(hostURL)
	require.NoError(t, err)

	require.NoError(t, probeHealth(t.Context(), hc, reqURL, hostURL))
}

func TestProbeHealth_NonSuccessStatusIsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	hostURL := tcpHostURL(t, srv)

	hc, reqURL, err := readinessHTTPClient(hostURL)
	require.NoError(t, err)

	err = probeHealth(t.Context(), hc, reqURL, hostURL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "server health check failed")
}

func TestQuickHealthProbe_RespondingServerSucceeds(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	require.NoError(t, quickHealthProbe(t.Context(), tcpHostURL(t, srv)))
}

// TestQuickHealthProbe_UnreachableServerFails covers a closed listener:
// the connection itself must fail fast without hanging the probe.
func TestQuickHealthProbe_UnreachableServerFails(t *testing.T) {
	t.Parallel()

	closedHost := closedTCPListener(t)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err := quickHealthProbe(ctx, &url.URL{Scheme: "tcp", Host: closedHost})
	require.Error(t, err)
}

func TestWaitForServerReady_BecomesReadyImmediately(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	require.NoError(t, waitForServerReady(t.Context(), tcpHostURL(t, srv)))
}

// TestWaitForServerReady_TimesOutOnUnreachableServer covers the timeout
// path. ANGELA_SERVER_READY_TIMEOUT is set very short so the test does
// not wait out the 10s default.
func TestWaitForServerReady_TimesOutOnUnreachableServer(t *testing.T) {
	t.Setenv("ANGELA_SERVER_READY_TIMEOUT", "150ms")

	closedHost := closedTCPListener(t)

	err := waitForServerReady(t.Context(), &url.URL{Scheme: "tcp", Host: closedHost})
	require.Error(t, err)
}

// TestWaitForServerReady_ContextCancelStopsWaiting covers the ctx.Done
// branch: an already-cancelled context must return immediately rather
// than waiting for the timeout.
func TestWaitForServerReady_ContextCancelStopsWaiting(t *testing.T) {
	t.Setenv("ANGELA_SERVER_READY_TIMEOUT", "5s")

	closedHost := closedTCPListener(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()
	err := waitForServerReady(ctx, &url.URL{Scheme: "tcp", Host: closedHost})
	require.Error(t, err)
	require.Less(t, time.Since(start), 4*time.Second, "a cancelled context must not wait out the readiness timeout")
}

func TestAwaitSocketGone_AlreadyGoneReturnsImmediately(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "server.sock")
	require.NoError(t, awaitSocketGone(t.Context(), &url.URL{Host: sockPath}))
}

// TestAwaitSocketGone_WaitsForRemoval covers the polling loop: the
// socket file exists when the call starts and disappears shortly after,
// which the poll must observe.
func TestAwaitSocketGone_WaitsForRemoval(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "server.sock")
	require.NoError(t, os.WriteFile(sockPath, []byte("x"), 0o644))

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.Remove(sockPath)
	}()

	start := time.Now()
	require.NoError(t, awaitSocketGone(t.Context(), &url.URL{Host: sockPath}))
	require.Less(t, time.Since(start), 2*time.Second)

	_, statErr := os.Stat(sockPath)
	require.True(t, os.IsNotExist(statErr))
}

// TestAwaitSocketGone_ContextCancelledPropagatesError covers a context
// that expires before the socket is ever removed.
func TestAwaitSocketGone_ContextCancelledPropagatesError(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "server.sock")
	require.NoError(t, os.WriteFile(sockPath, []byte("x"), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := awaitSocketGone(ctx, &url.URL{Host: sockPath})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestProbeHealth_UnixSocketUsesDummyHost covers the unix/npipe branch
// of probeHealth: the request must be sent with the dummy Host header
// while the transport dials the real socket path, and a real unix
// listener must accept and answer it.
func TestProbeHealth_UnixSocketUsesDummyHost(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "server.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/health" && r.Host == client.DummyHost {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		}))
	}()

	hostURL := &url.URL{Scheme: "unix", Host: sockPath}
	require.NoError(t, quickHealthProbe(t.Context(), hostURL))
}

// TestAwaitSocketGone_ForceRemovesAfterExhaustingRetries covers the
// fallback: a socket that never disappears on its own (the previous
// server crashed without cleanup) must be force-removed once the
// polling budget (20 * 100ms) is exhausted, rather than waiting on it
// forever.
func TestAwaitSocketGone_ForceRemovesAfterExhaustingRetries(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "server.sock")
	require.NoError(t, os.WriteFile(sockPath, []byte("x"), 0o644))

	require.NoError(t, awaitSocketGone(t.Context(), &url.URL{Host: sockPath}))

	_, statErr := os.Stat(sockPath)
	require.True(t, os.IsNotExist(statErr), "the socket must be force-removed once polling is exhausted")
}
