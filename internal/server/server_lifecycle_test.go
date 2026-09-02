package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseHostURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		host       string
		wantScheme string
		wantHost   string
		wantPath   string
		wantErr    bool
	}{
		{name: "unix socket", host: "unix:///tmp/angela.sock", wantScheme: "unix", wantHost: "/tmp/angela.sock"},
		{name: "tcp address", host: "tcp://127.0.0.1:8080", wantScheme: "tcp", wantHost: "127.0.0.1:8080"},
		{name: "tcp address with path", host: "tcp://127.0.0.1:8080/base", wantScheme: "tcp", wantHost: "127.0.0.1:8080", wantPath: "/base"},
		{name: "missing scheme separator", host: "not-a-url", wantErr: true},
		{name: "invalid tcp address", host: "tcp://%zz", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseHostURL(tt.host)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantScheme, got.Scheme)
			require.Equal(t, tt.wantHost, got.Host)
			require.Equal(t, tt.wantPath, got.Path)
		})
	}
}

func TestNewServer_TCP(t *testing.T) {
	t.Parallel()

	s := NewServer(nil, "tcp", "127.0.0.1:0")
	require.Equal(t, "tcp", s.network)
	require.Equal(t, "127.0.0.1:0", s.Addr)
	require.Equal(t, "127.0.0.1:0", s.h.Addr, "h.Addr must be set for tcp servers")
	require.NotNil(t, s.Backend())
	require.NotNil(t, s.Handler())
}

// shortSocketDir returns a fresh temp directory suitable as the base
// for a Unix socket path. Unlike t.TempDir(), it does not embed the
// test name, so paths built under it stay well below the 104-byte
// macOS sun_path limit (see maxUnixSocketPathLen) regardless of how
// long the test name is.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "angela-sock")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestNewServer_Unix(t *testing.T) {
	t.Parallel()

	path := filepath.Join(shortSocketDir(t), "s.sock")
	s := NewServer(nil, "unix", path)
	require.Equal(t, "unix", s.network)
	require.Equal(t, path, s.Addr)
	require.Empty(t, s.h.Addr, "h.Addr is only set for tcp servers")
	require.NotNil(t, s.Backend())
}

func TestDefaultServer(t *testing.T) {
	t.Parallel()

	s := DefaultServer(nil)
	require.NotNil(t, s)
	require.NotEmpty(t, s.Addr)
	require.NotNil(t, s.Backend())
}

func TestServer_SetLogger(t *testing.T) {
	t.Parallel()

	s := &Server{}
	require.Nil(t, s.logger)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.SetLogger(logger)
	require.Same(t, logger, s.logger)
}

// TestServer_ListenAndServe_AlreadyStarted verifies that a Server whose
// ln is already set refuses a second ListenAndServe call rather than
// silently binding a second listener.
func TestServer_ListenAndServe_AlreadyStarted(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	s := &Server{ln: ln}
	err = s.ListenAndServe()
	require.ErrorContains(t, err, "server already started")
}

// TestServer_ListenAndServe_ListenError verifies that a bind failure
// (here: a unix socket path whose parent directory does not exist) is
// wrapped and returned rather than panicking. The path must not
// resolve via [os.Stat] or the stale-socket self-heal path in listen
// would remove it and bind successfully instead of failing.
func TestServer_ListenAndServe_ListenError(t *testing.T) {
	t.Parallel()

	s := NewServer(nil, "unix", filepath.Join(shortSocketDir(t), "missing-dir", "s.sock"))
	err := s.ListenAndServe()
	require.ErrorContains(t, err, "failed to listen")
}

// dialUnixClient builds an *http.Client that dials the given unix
// socket path for every request, regardless of the request URL host.
func dialUnixClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}
}

// TestServer_ListenAndServeAndShutdown drives a real Server through
// ListenAndServe over a Unix socket, confirms it answers HTTP
// requests, then verifies Shutdown drains it gracefully and unblocks
// the ListenAndServe goroutine with ErrServerClosed.
func TestServer_ListenAndServeAndShutdown(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "lifecycle.sock")
	s := NewServer(nil, "unix", path)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe()
	}()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", path) //nolint:noctx
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond, "server never started accepting connections")

	client := dialUnixClient(path)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://unix/v1/health", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, s.Shutdown(t.Context()))

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, ErrServerClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return after Shutdown")
	}
}

// TestServer_Close verifies that Close force-closes a running listener
// and unblocks ListenAndServe with ErrServerClosed.
func TestServer_Close(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "close.sock")
	s := NewServer(nil, "unix", path)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe()
	}()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", path) //nolint:noctx
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond, "server never started accepting connections")

	require.NoError(t, s.Close())

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, ErrServerClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return after Close")
	}
}

// TestServer_ListenAndServe_RemovesStaleSocket drives the self-heal
// path documented on [listen]: a socket file left behind by a dead
// process (dial fails, but the path exists) is removed and rebound
// rather than surfacing "address already in use".
func TestServer_ListenAndServe_RemovesStaleSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix-socket stale-file recovery; listen (net_windows.go) never removes a stale socket file")
	}
	path := filepath.Join(shortSocketDir(t), "stale.sock")

	ln, err := net.Listen("unix", path) //nolint:noctx
	require.NoError(t, err)
	unixLn, ok := ln.(*net.UnixListener)
	require.True(t, ok)
	unixLn.SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())

	var buf bytes.Buffer
	s := NewServer(nil, "unix", path)
	s.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe()
	}()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", path) //nolint:noctx
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond, "server never started accepting connections")

	require.NoError(t, s.Shutdown(t.Context()))

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, ErrServerClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return after Shutdown")
	}

	require.Contains(t, buf.String(), "Removed stale socket")
}

// TestServer_CloseListener verifies that closeListener closes a set
// listener and clears the field, independent of whichever caller
// populated s.ln.
func TestServer_CloseListener(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx
	require.NoError(t, err)
	s := &Server{ln: ln}

	s.closeListener()

	require.Nil(t, s.ln)
	_, err = ln.Accept()
	require.Error(t, err, "listener should be closed")
}

// TestServer_LogDebug verifies logDebug enriches the message with
// request fields and forwards it to the configured logger.
func TestServer_LogDebug(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	s := &Server{logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/health", nil)

	s.logDebug(req, "debug message", "extra", "value")

	out := buf.String()
	require.Contains(t, out, "debug message")
	require.Contains(t, out, "method=GET")
	require.Contains(t, out, "extra=value")
}

// TestServer_LogError verifies logError enriches the message with
// request fields and forwards it to the configured logger.
func TestServer_LogError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	s := &Server{logger: slog.New(slog.NewTextHandler(&buf, nil))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/control", nil)

	s.logError(req, "error message", "reason", "boom")

	out := buf.String()
	require.Contains(t, out, "error message")
	require.Contains(t, out, "method=POST")
	require.Contains(t, out, "reason=boom")
}

// TestDefaultHost_FallsBackWhenPathTooLong verifies that a socket path
// exceeding maxUnixSocketPathLen falls back to /tmp rather than
// producing an unbindable path. Not parallel: t.Setenv forbids it.
func TestDefaultHost_FallsBackWhenPathTooLong(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DefaultHost returns a named pipe URL on Windows without consulting maxUnixSocketPathLen")
	}
	t.Setenv("XDG_RUNTIME_DIR", "/"+strings.Repeat("x", 200))

	host := DefaultHost()

	require.True(t, strings.HasPrefix(host, "unix:///tmp/angela"), "expected /tmp fallback, got %s", host)
	require.NotContains(t, host, "xxxxxxxxxx")
}
