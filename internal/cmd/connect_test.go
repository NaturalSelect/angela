package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newConnectToServerTestCmd builds a standalone command carrying only
// the flags connectToServer reads directly.
func newConnectToServerTestCmd(t *testing.T, cwd, dataDir string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("cwd", cwd, "")
	cmd.Flags().String("data-dir", dataDir, "")
	cmd.Flags().Bool("debug", false, "")
	cmd.Flags().Bool("yolo", false, "")
	cmd.Flags().StringSlice("channels", nil, "")
	return cmd
}

// pointClientHostAtServer redirects the package-level --host value at
// a plain TCP test server for the duration of the test. ensureServer's
// stale-socket handling only applies to the "unix"/"npipe" schemes; a
// "tcp" host falls through as a no-op, which makes the rest of
// connectToServer (workspace creation, metrics setup, cleanup)
// reachable against an httptest server standing in for the daemon.
func pointClientHostAtServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	orig := clientHost
	clientHost = "tcp://" + u.Host
	t.Cleanup(func() { clientHost = orig })
}

// TestConnectToServer_CreatesWorkspaceAndCleansUp covers the full
// happy path against a live (fake) server: the workspace is created,
// the returned client/workspace/cleanup are usable, and cleanup
// retires the client rather than deleting the workspace outright.
func TestConnectToServer_CreatesWorkspaceAndCleansUp(t *testing.T) {
	// t.Setenv/t.Chdir rule out t.Parallel() in this package's convention.
	t.Setenv("ANGELA_DISABLE_METRICS", "true")
	t.Chdir(t.TempDir())

	var retireCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workspaces"):
			require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{
				ID:     "ws1",
				Config: &config.Config{Options: &config.Options{}},
			}))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/clients/"):
			atomic.AddInt32(&retireCalls, 1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	pointClientHostAtServer(t, srv)

	// cwd is left empty so ResolveCwd reads the process cwd (already
	// pinned above via t.Chdir) instead of Chdir'ing again into a
	// directory whose lifetime t.Chdir isn't managing.
	cmd := newConnectToServerTestCmd(t, "", t.TempDir())

	c, ws, cleanup, err := connectToServer(cmd)
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Equal(t, "ws1", ws.ID)
	require.NotNil(t, cleanup)

	cleanup()
	require.EqualValues(t, 1, atomic.LoadInt32(&retireCalls),
		"cleanup must retire the client rather than leaving it dangling")
}

// TestConnectToServer_InvalidHostErrorPropagates covers the
// ParseHostURL error branch: a malformed --host value must fail
// before any server contact is attempted.
func TestConnectToServer_InvalidHostErrorPropagates(t *testing.T) {
	t.Chdir(t.TempDir())

	orig := clientHost
	clientHost = "not-a-valid-host-format"
	t.Cleanup(func() { clientHost = orig })

	cmd := newConnectToServerTestCmd(t, "", t.TempDir())

	_, _, _, err := connectToServer(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid host URL")
}

// TestConnectToServer_CreateWorkspaceErrorPropagates covers a live
// server that refuses workspace creation outright (not a shutdown
// race): the error must propagate without retrying.
func TestConnectToServer_CreateWorkspaceErrorPropagates(t *testing.T) {
	t.Setenv("ANGELA_DISABLE_METRICS", "true")
	t.Chdir(t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	pointClientHostAtServer(t, srv)

	cmd := newConnectToServerTestCmd(t, "", t.TempDir())

	_, _, _, err := connectToServer(cmd)
	require.Error(t, err)
}

// TestConnectToServer_YoloFlagSetsPermissionMode covers the --yolo
// branch: the workspace creation request must carry the yolo
// permission mode instead of the manual default.
func TestConnectToServer_YoloFlagSetsPermissionMode(t *testing.T) {
	t.Setenv("ANGELA_DISABLE_METRICS", "true")
	t.Chdir(t.TempDir())

	var gotMode atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workspaces") {
			var req proto.Workspace
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			gotMode.Store(req.PermissionMode)
			require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{
				ID:     "ws1",
				Config: &config.Config{Options: &config.Options{}},
			}))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	pointClientHostAtServer(t, srv)

	cmd := newConnectToServerTestCmd(t, "", t.TempDir())
	require.NoError(t, cmd.Flags().Set("yolo", "true"))

	_, _, cleanup, err := connectToServer(cmd)
	require.NoError(t, err)
	cleanup()

	require.Equal(t, permission.ModeYolo.String(), gotMode.Load())
}

// TestConnectToServer_CleanupFallsBackToDeleteWorkspace covers the
// cleanup closure's error path: when retiring the client fails, cleanup
// must fall back to deleting the workspace outright rather than leaving
// it dangling on the server.
func TestConnectToServer_CleanupFallsBackToDeleteWorkspace(t *testing.T) {
	t.Setenv("ANGELA_DISABLE_METRICS", "true")
	t.Chdir(t.TempDir())

	var deleteCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workspaces"):
			require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{
				ID:     "ws1",
				Config: &config.Config{Options: &config.Options{}},
			}))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/clients/"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/workspaces/"):
			atomic.AddInt32(&deleteCalls, 1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	pointClientHostAtServer(t, srv)

	cmd := newConnectToServerTestCmd(t, "", t.TempDir())

	_, ws, cleanup, err := connectToServer(cmd)
	require.NoError(t, err)
	require.Equal(t, "ws1", ws.ID)

	cleanup()
	require.EqualValues(t, 1, atomic.LoadInt32(&deleteCalls),
		"cleanup must delete the workspace when retiring the client fails")
}
