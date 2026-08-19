package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

// failingServer answers every request with the given status and a
// proto.Error body, standing in for a server that refused the call.
func failingServer(t *testing.T, status int, message string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		require.NoError(t, json.NewEncoder(w).Encode(proto.Error{Message: message}))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRecordRecentModelSurfacesTheServersReason is A8. The call used to
// compare the raw status code, so the server's explanation was dropped
// and a 404 arrived as an anonymous failure that no caller could tell
// from a transport fault.
func TestRecordRecentModelSurfacesTheServersReason(t *testing.T) {
	t.Parallel()

	srv := failingServer(t, http.StatusBadRequest, "unknown model slot")
	c := captureClient(t, srv)

	err := c.RecordRecentModel(context.Background(), "ws1", config.ScopeGlobal,
		config.ModelMain, config.SelectedModel{Provider: "mock", Model: "m"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown model slot",
		"the server said why; the caller must be told")
}

// TestRecordRecentModelReportsAMissingWorkspace pins the other half:
// 404 has to arrive as ErrNotFound, because that is the signal callers
// act on by re-registering rather than retrying an ID the server will
// never know again.
func TestRecordRecentModelReportsAMissingWorkspace(t *testing.T) {
	t.Parallel()

	srv := failingServer(t, http.StatusNotFound, "workspace not found")
	c := captureClient(t, srv)

	err := c.RecordRecentModel(context.Background(), "ws1", config.ScopeGlobal,
		config.ModelMain, config.SelectedModel{Provider: "mock", Model: "m"})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestAgentEditSessionActiveSurfacesTheServersReason covers the same
// gap on the edit call, where the refusal is the whole point: an
// unknown agent or preset now comes back as 400 with a reason, and
// throwing it away would leave the user with a silent no-op.
func TestAgentEditSessionActiveSurfacesTheServersReason(t *testing.T) {
	t.Parallel()

	srv := failingServer(t, http.StatusBadRequest, `agent not available: "nope"`)
	c := captureClient(t, srv)

	_, err := c.AgentEditSessionActive(context.Background(), "ws1", "s1",
		proto.ActiveAgentEditRequest{Agent: "nope"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent not available")
}

// TestAgentEditSessionActiveReportsAMissingSession keeps the 404 the
// server now returns for an unknown session distinguishable from any
// other failure.
func TestAgentEditSessionActiveReportsAMissingSession(t *testing.T) {
	t.Parallel()

	srv := failingServer(t, http.StatusNotFound, "session not found: s1")
	c := captureClient(t, srv)

	_, err := c.AgentEditSessionActive(context.Background(), "ws1", "s1",
		proto.ActiveAgentEditRequest{ToggleThink: true})
	require.ErrorIs(t, err, ErrNotFound)
}
