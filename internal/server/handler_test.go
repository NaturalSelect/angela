package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/backend"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newController(t *testing.T) *controllerV1 {
	t.Helper()
	b := backend.New(context.Background(), nil, nil)
	return &controllerV1{backend: b, server: &Server{backend: b}}
}

func newBusyController(t *testing.T) *controllerV1 {
	t.Helper()
	b := backend.New(context.Background(), nil, nil)
	backend.InsertWorkspaceForTest(b, &backend.Workspace{ID: uuid.New().String(), Path: t.TempDir()})
	return &controllerV1{backend: b, server: &Server{backend: b}}
}

func newReq(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	if body == "" {
		return httptest.NewRequestWithContext(t.Context(), method, target, nil)
	}
	return httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
}

func TestHandleGetHealth(t *testing.T) {
	t.Parallel()

	c := newController(t)
	rec := httptest.NewRecorder()
	c.handleGetHealth(rec, newReq(t, http.MethodGet, "/v1/health", ""))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleGetVersion(t *testing.T) {
	t.Parallel()

	c := newController(t)
	rec := httptest.NewRecorder()
	c.handleGetVersion(rec, newReq(t, http.MethodGet, "/v1/version", ""))
	require.Equal(t, http.StatusOK, rec.Code)

	var got proto.VersionInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, c.backend.VersionInfo(), got)
}

func TestHandleGetConfig(t *testing.T) {
	t.Parallel()

	c := newController(t)
	rec := httptest.NewRecorder()
	c.handleGetConfig(rec, newReq(t, http.MethodGet, "/v1/config", ""))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleGetWorkspaces_Empty(t *testing.T) {
	t.Parallel()

	c := newController(t)
	rec := httptest.NewRecorder()
	c.handleGetWorkspaces(rec, newReq(t, http.MethodGet, "/v1/workspaces", ""))
	require.Equal(t, http.StatusOK, rec.Code)

	var got []proto.Workspace
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, got)
}

func TestHandlePostControl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		busy       bool
		wantStatus int
	}{
		{name: "malformed body", body: "not json", wantStatus: http.StatusBadRequest},
		{name: "unknown command", body: `{"command":"reboot"}`, wantStatus: http.StatusBadRequest},
		{name: "shutdown while busy", body: `{"command":"shutdown"}`, busy: true, wantStatus: http.StatusConflict},
		{name: "shutdown_if_idle while busy", body: `{"command":"shutdown_if_idle"}`, busy: true, wantStatus: http.StatusConflict},
		{name: "shutdown while idle", body: `{"command":"shutdown"}`, wantStatus: http.StatusOK},
		{name: "shutdown_if_idle while idle", body: `{"command":"shutdown_if_idle"}`, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			if tt.busy {
				c = newBusyController(t)
			}
			rec := httptest.NewRecorder()
			c.handlePostControl(rec, newReq(t, http.MethodPost, "/v1/control", tt.body))
			require.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestHandleDeleteClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		clientID   string
		wantStatus int
	}{
		{name: "not a uuid", clientID: "not-a-uuid", wantStatus: http.StatusBadRequest},
		{name: "unknown but valid uuid", clientID: uuid.New().String(), wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			req := newReq(t, http.MethodDelete, "/v1/clients/"+tt.clientID, "")
			req.SetPathValue("client_id", tt.clientID)
			rec := httptest.NewRecorder()
			c.handleDeleteClient(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestHandlePostWorkspaces_MalformedBody(t *testing.T) {
	t.Parallel()

	c := newController(t)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaces(rec, newReq(t, http.MethodPost, "/v1/workspaces", "not json"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleDeleteWorkspaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{name: "missing client_id", query: "", wantStatus: http.StatusBadRequest},
		{name: "invalid client_id", query: "?client_id=not-a-uuid", wantStatus: http.StatusBadRequest},
		// DeleteWorkspace delegates to releaseHold, which is documented
		// as idempotent: an unknown workspace is a no-op success, not
		// a 404.
		{name: "valid client_id, unknown workspace is a no-op", query: "?client_id=" + uuid.New().String(), wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			req := newReq(t, http.MethodDelete, "/v1/workspaces/x"+tt.query, "")
			req.SetPathValue("id", uuid.New().String())
			rec := httptest.NewRecorder()
			c.handleDeleteWorkspaces(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestHandlePostWorkspacePermissionsUnattended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "malformed body", body: "not json", wantStatus: http.StatusBadRequest},
		{name: "missing session_id", body: `{"unattended":true}`, wantStatus: http.StatusBadRequest},
		{name: "unknown workspace", body: `{"session_id":"s1","unattended":true}`, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			req := newReq(t, http.MethodPost, "/v1/workspaces/x/permissions/unattended", tt.body)
			req.SetPathValue("id", uuid.New().String())
			rec := httptest.NewRecorder()
			c.handlePostWorkspacePermissionsUnattended(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestHandleGetWorkspaceMCPAuthURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{name: "missing name", query: "", wantStatus: http.StatusBadRequest},
		{name: "with name", query: "?name=my-mcp-server", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			req := newReq(t, http.MethodGet, "/v1/workspaces/x/mcp/auth-url"+tt.query, "")
			req.SetPathValue("id", uuid.New().String())
			rec := httptest.NewRecorder()
			c.handleGetWorkspaceMCPAuthURL(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusOK {
				var got proto.MCPAuthResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			}
		})
	}
}

func TestHandleGetWorkspaceMCPStates_Empty(t *testing.T) {
	t.Parallel()

	c := newController(t)
	req := newReq(t, http.MethodGet, "/v1/workspaces/x/mcp/states", "")
	req.SetPathValue("id", uuid.New().String())
	rec := httptest.NewRecorder()
	c.handleGetWorkspaceMCPStates(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]proto.MCPClientInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
}

// TestHandleGetWorkspaceLSPs_Success exercises the success branch,
// which builds its response from a process-global LSP registry rather
// than anything on the workspace, so an otherwise-empty inserted
// workspace is enough to reach it.
func TestHandleGetWorkspaceLSPs_Success(t *testing.T) {
	t.Parallel()

	c := newController(t)
	wsID := uuid.New().String()
	backend.InsertWorkspaceForTest(c.backend, &backend.Workspace{ID: wsID, Path: t.TempDir()})

	req := newReq(t, http.MethodGet, "/v1/workspaces/x/lsps", "")
	req.SetPathValue("id", wsID)
	rec := httptest.NewRecorder()
	c.handleGetWorkspaceLSPs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]proto.LSPClientInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
}

// TestHandleGetWorkspaceMCPPendingAuth_Success exercises the success
// branch. PendingAuthMCPs only dereferences the workspace config for
// servers already in StateNeedsAuth, so an inserted workspace with no
// MCP state reaches the response-building code without needing a
// populated Cfg.
func TestHandleGetWorkspaceMCPPendingAuth_Success(t *testing.T) {
	t.Parallel()

	c := newController(t)
	wsID := uuid.New().String()
	backend.InsertWorkspaceForTest(c.backend, &backend.Workspace{ID: wsID, Path: t.TempDir()})

	req := newReq(t, http.MethodGet, "/v1/workspaces/x/mcp/pending-auth", "")
	req.SetPathValue("id", wsID)
	rec := httptest.NewRecorder()
	c.handleGetWorkspaceMCPPendingAuth(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got []proto.MCPPendingAuthServer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
}

func TestHandlePostWorkspaceMCPRefreshPrompts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "malformed body", body: "not json", wantStatus: http.StatusBadRequest},
		{name: "unknown server name is a no-op", body: `{"name":"does-not-exist"}`, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			req := newReq(t, http.MethodPost, "/v1/workspaces/x/mcp/refresh-prompts", tt.body)
			req.SetPathValue("id", uuid.New().String())
			rec := httptest.NewRecorder()
			c.handlePostWorkspaceMCPRefreshPrompts(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestHandlePostWorkspaceMCPRefreshResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "malformed body", body: "not json", wantStatus: http.StatusBadRequest},
		{name: "unknown server name is a no-op", body: `{"name":"does-not-exist"}`, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			req := newReq(t, http.MethodPost, "/v1/workspaces/x/mcp/refresh-resources", tt.body)
			req.SetPathValue("id", uuid.New().String())
			rec := httptest.NewRecorder()
			c.handlePostWorkspaceMCPRefreshResources(rec, req)
			require.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

// TestHandleError_MapsSentinelsToStatus exhaustively checks every
// backend error sentinel handleError knows about, plus an unrecognized
// error to confirm the default stays a 500.
func TestHandleError_MapsSentinelsToStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "workspace not found", err: backend.ErrWorkspaceNotFound, want: http.StatusNotFound},
		{name: "lsp client not found", err: backend.ErrLSPClientNotFound, want: http.StatusNotFound},
		{name: "session not found", err: backend.ErrSessionNotFound, want: http.StatusNotFound},
		{name: "agent not available", err: backend.ErrAgentNotAvailable, want: http.StatusBadRequest},
		{name: "variant not available", err: backend.ErrVariantNotAvailable, want: http.StatusBadRequest},
		{name: "model slot mismatch", err: backend.ErrModelSlotMismatch, want: http.StatusBadRequest},
		{name: "agent not initialized", err: backend.ErrAgentNotInitialized, want: http.StatusBadRequest},
		{name: "path required", err: backend.ErrPathRequired, want: http.StatusBadRequest},
		{name: "invalid permission action", err: backend.ErrInvalidPermissionAction, want: http.StatusBadRequest},
		{name: "invalid permission mode", err: backend.ErrInvalidPermissionMode, want: http.StatusBadRequest},
		{name: "unknown command", err: backend.ErrUnknownCommand, want: http.StatusBadRequest},
		{name: "invalid client id", err: backend.ErrInvalidClientID, want: http.StatusBadRequest},
		{name: "client not attached", err: backend.ErrClientNotAttached, want: http.StatusConflict},
		{name: "workspace closing", err: backend.ErrWorkspaceClosing, want: http.StatusConflict},
		{name: "server not idle", err: backend.ErrServerNotIdle, want: http.StatusConflict},
		{name: "client retired", err: backend.ErrClientRetired, want: http.StatusConflict},
		{name: "server shutting down", err: backend.ErrServerShuttingDown, want: http.StatusServiceUnavailable},
		{name: "unrecognized error stays a 500", err: errors.New("disk I/O error"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newController(t)
			rec := httptest.NewRecorder()
			req := newReq(t, http.MethodGet, "/v1/workspaces/x", "")
			c.handleError(rec, req, tt.err)
			require.Equal(t, tt.want, rec.Code)

			var body proto.Error
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, tt.err.Error(), body.Message)
		})
	}
}
