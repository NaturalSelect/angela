package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestDefaultClient(t *testing.T) {
	t.Parallel()

	c, err := DefaultClient(t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, c.Path())
	require.NotEmpty(t, c.ClientID())
}

func TestNewClientTCP(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "tcp", "127.0.0.1:1234")
	require.NoError(t, err)
	require.Equal(t, "tcp", c.network)
	require.Equal(t, "127.0.0.1:1234", c.addr)
}

func TestNewClientUnix(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "unix", "/tmp/angela-test.sock")
	require.NoError(t, err)
	require.Equal(t, "unix", c.network)
}

func TestNewClientEmptyNetwork(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "", "")
	require.NoError(t, err)
	require.Equal(t, "", c.network)
}

func TestNewClientPathIsCleaned(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c, err := NewClient(dir+"/sub/..", "tcp", "127.0.0.1:1234")
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir+"/sub/.."), c.Path())
}

func TestNewClientUniqueClientID(t *testing.T) {
	t.Parallel()

	c1, err := NewClient(t.TempDir(), "tcp", "127.0.0.1:1234")
	require.NoError(t, err)
	c2, err := NewClient(t.TempDir(), "tcp", "127.0.0.1:1234")
	require.NoError(t, err)
	require.NotEmpty(t, c1.ClientID())
	require.NotEqual(t, c1.ClientID(), c2.ClientID())
}

func TestGetGlobalConfigSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/config", r.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	cfg, err := c.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestGetGlobalConfigMalformedBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.GetGlobalConfig(context.Background())
	require.Error(t, err)
}

func TestGetGlobalConfigTransportError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	c := captureClient(t, srv)
	srv.Close()

	_, err := c.GetGlobalConfig(context.Background())
	require.Error(t, err)
}

func TestHealthSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.Health(context.Background()))
}

func TestHealthFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	err := c.Health(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "server health check failed")
}

func TestVersionInfoSuccess(t *testing.T) {
	t.Parallel()

	want := proto.VersionInfo{Version: "1.2.3", Commit: "abc123", BuildID: "b1", GoVersion: "go1.26", Platform: "linux/amd64"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/version", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	got, err := c.VersionInfo(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, *got)
}

func TestVersionInfoMalformedBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.VersionInfo(context.Background())
	require.Error(t, err)
}

func TestShutdownServerIfIdle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "ok", status: http.StatusOK},
		{name: "busy", status: http.StatusConflict, wantErr: ErrServerBusy},
		{name: "unsupported", status: http.StatusBadRequest, wantErr: ErrUnsupported},
		{name: "other failure", status: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/control", r.URL.Path)
				var body proto.ServerControl
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, proto.ServerControlShutdownIfIdle, body.Command)
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := captureClient(t, srv)
			err := c.ShutdownServerIfIdle(context.Background())
			if tc.status == http.StatusOK {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestShutdownServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "ok", status: http.StatusOK},
		{name: "busy", status: http.StatusConflict, wantErr: ErrServerBusy},
		{name: "other failure", status: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				var body proto.ServerControl
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, proto.ServerControlShutdown, body.Command)
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := captureClient(t, srv)
			err := c.ShutdownServer(context.Background())
			if tc.status == http.StatusOK {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestRetireClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "ok", status: http.StatusOK},
		{name: "predates endpoint", status: http.StatusNotFound, wantErr: ErrUnsupported},
		{name: "other failure", status: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				gotPath = r.URL.Path
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			c := captureClient(t, srv)
			err := c.RetireClient(context.Background())
			require.Equal(t, "/v1/clients/"+c.ClientID(), gotPath)
			if tc.status == http.StatusOK {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestDialerTCP(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	c, err := NewClient(t.TempDir(), "tcp", ln.Addr().String())
	require.NoError(t, err)
	conn, err := c.Dial(context.Background(), "tcp", ln.Addr().String())
	require.NoError(t, err)
	conn.Close()
}

func TestDialerTCPUnreachable(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	c, err := NewClient(t.TempDir(), "tcp", addr)
	require.NoError(t, err)
	_, err = c.Dial(context.Background(), "tcp", addr)
	require.Error(t, err)
}

func TestDialerUnix(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	c, err := NewClient(t.TempDir(), "unix", sockPath)
	require.NoError(t, err)
	conn, err := c.Dial(context.Background(), "unix", sockPath)
	require.NoError(t, err)
	conn.Close()
}

func TestDialerUnixMissingSocket(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(t.TempDir(), "missing.sock")
	c, err := NewClient(t.TempDir(), "unix", sockPath)
	require.NoError(t, err)
	_, err = c.Dial(context.Background(), "unix", sockPath)
	require.Error(t, err)
}

func TestDialerNpipeUnsupportedOnThisPlatform(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "npipe", `\\.\pipe\test`)
	require.NoError(t, err)
	_, err = c.Dial(context.Background(), "npipe", `\\.\pipe\test`)
	require.Error(t, err)
}

func TestBuildReqInvalidURL(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "tcp", "127.0.0.1:1234")
	require.NoError(t, err)
	_, err = c.buildReq(context.Background(), http.MethodGet, "http://example.com/%zz", nil, nil)
	require.Error(t, err)
}

func TestBuildReqSetsSchemeAndHost(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "tcp", "127.0.0.1:1234")
	require.NoError(t, err)
	req, err := c.buildReq(context.Background(), http.MethodGet, "/v1/health", nil, http.Header{"X-Test": []string{"1"}})
	require.NoError(t, err)
	require.Equal(t, "http", req.URL.Scheme)
	require.Equal(t, "127.0.0.1:1234", req.URL.Host)
	require.Equal(t, "1", req.Header.Get("X-Test"))
}

func TestBuildReqUnixHostHeader(t *testing.T) {
	t.Parallel()

	c, err := NewClient(t.TempDir(), "unix", "/tmp/angela-test.sock")
	require.NoError(t, err)
	req, err := c.buildReq(context.Background(), http.MethodGet, "/v1/health", nil, nil)
	require.NoError(t, err)
	require.Equal(t, DummyHost, req.Host)
}

func TestSendReqContextAlreadyCanceled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.get(ctx, "/health", nil, nil)
	require.Error(t, err)
}

func TestSendReqServerUnreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	c := captureClient(t, srv)
	srv.Close()

	_, err := c.get(context.Background(), "/health", nil, nil)
	require.Error(t, err)
}
