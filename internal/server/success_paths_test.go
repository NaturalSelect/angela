package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceScopedHandlers_SuccessPaths drives the handlers whose
// prior coverage stopped at the shared not-found/malformed-body
// guards (see wsHandlerCases) through their success path, against one
// real workspace created via the CreateWorkspace HTTP path. That
// closes the "backend call succeeded" line each handler still needs:
// the sandbox status GET, the agent-variant override, and both MCP
// enable/disable handlers.
//
// PostWorkspaceSandboxEnter is checked instead for the 501 mapping:
// Backend.EnterSandbox always fails with sandbox.ErrNotSupported for
// a daemon-hosted workspace (entering a sandbox is process-wide and
// would restrict every other workspace the daemon serves), so its
// success line can never run through the real backend.
func TestWorkspaceScopedHandlers_SuccessPaths(t *testing.T) {
	// Not parallel: newRealCreateHarness calls t.Setenv.
	h := newRealCreateHarness(t)
	h.backend.SetCreateGrace(2 * time.Second)

	clientID := uuid.New().String()
	wsResp := h.postWorkspace(t, proto.Workspace{
		Path:     t.TempDir(),
		DataDir:  t.TempDir(),
		ClientID: clientID,
	})
	// Release the create hold so the workspace's App (and its SQLite
	// DB) closes before the t.TempDir() cleanups above try to remove
	// DataDir. Without this the DB only closes later via the create-
	// grace timer, racing TempDir's RemoveAll; Windows fails that
	// race since it can't delete a still-open file, unlike POSIX.
	t.Cleanup(func() { _ = h.backend.DeleteWorkspace(wsResp.ID, clientID) })
	ws, err := h.backend.GetWorkspace(wsResp.ID)
	require.NoError(t, err)

	// Seed a disabled MCP entry so MCPEnable's success path (which
	// otherwise needs a live server process) is reachable:
	// InitializeSingle treats a disabled entry as an immediate no-op
	// success.
	ws.Cfg.Config().MCP["disabled-server"] = config.MCPConfig{Disabled: true}

	c := &controllerV1{backend: h.backend, server: h.srv}

	t.Run("GetWorkspaceSandbox", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handleGetWorkspaceSandbox(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var got proto.SandboxStatusResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	})

	t.Run("PostWorkspaceConfigAgentVariant", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
			strings.NewReader(`{"agent_id":"coder","variant":"fast"}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceConfigAgentVariant(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "fast", ws.Cfg.Config().Agents[config.AgentCoder].Variant)
	})

	t.Run("PostWorkspaceMCPEnable", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
			strings.NewReader(`{"name":"disabled-server"}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceMCPEnable(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("PostWorkspaceMCPDisable", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
			strings.NewReader(`{"name":"disabled-server"}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceMCPDisable(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("PostWorkspaceSandboxEnter_NotSupportedInDaemon", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceSandboxEnter(rec, req)
		require.Equal(t, http.StatusNotImplemented, rec.Code)
	})
}
