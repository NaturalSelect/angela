package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	angelamcp "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestWorkspaceScopedHandlers_SuccessPaths drives the handlers whose
// prior coverage stopped at the shared not-found/malformed-body
// guards (see wsHandlerCases) through their success path, against one
// real workspace created via the CreateWorkspace HTTP path. That
// closes the "backend call succeeded" line each handler still needs:
// the sandbox status GET and both MCP enable/disable handlers.
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

	// Seed an MCP entry backed by a real (in-process) MCP HTTP server so
	// MCPEnable's success path is reachable. Disabled is left false here:
	// the lifecycle reconciler runs concurrently and would otherwise race
	// with InitializeSingle, tearing the just-started session back down
	// because config still says Disabled. That race is a runtime-toggle
	// concern unrelated to what this test is exercising (the HTTP
	// handlers' success paths).
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-mcp-server"}, nil)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	httpSrv := httptest.NewServer(mcpHandler)
	t.Cleanup(httpSrv.Close)
	// Wait for the startup mcp.Initialize goroutine (armed by app.New
	// during workspace creation above) to finish ranging over
	// Config().MCP before mutating that same map below: this write goes
	// straight into the live config instead of through the store's
	// copy-on-write mutators, so it must not overlap that read.
	require.NoError(t, angelamcp.WaitForInit(t.Context()))
	ws.Cfg.Config().MCP["disabled-server"] = config.MCPConfig{
		Type: config.MCPHttp,
		URL:  httpSrv.URL,
	}

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

	t.Run("PostWorkspaceMCPEnable", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
			strings.NewReader(`{"name":"disabled-server"}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceMCPEnable(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		info, ok := angelamcp.GetState("disabled-server")
		require.True(t, ok)
		require.Equal(t, angelamcp.StateConnected, info.State)
	})

	// Reverse direction: disable the server the previous subtest just
	// connected, proving the handler tears down a live session rather
	// than just reporting success against no-op state.
	t.Run("PostWorkspaceMCPDisable", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
			strings.NewReader(`{"name":"disabled-server"}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceMCPDisable(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		info, ok := angelamcp.GetState("disabled-server")
		require.True(t, ok)
		require.Equal(t, angelamcp.StateDisabled, info.State)
	})

	t.Run("PostWorkspaceSandboxEnter_NotSupportedInDaemon", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
		req.SetPathValue("id", ws.ID)
		rec := httptest.NewRecorder()
		c.handlePostWorkspaceSandboxEnter(rec, req)
		require.Equal(t, http.StatusNotImplemented, rec.Code)
	})
}

// TestPostWorkspaceAgentSessionCommitMessage_Success drives the
// commit-message handler's success path: neither wsHandlerCases (the
// shared not-found/malformed-body table) nor
// TestWorkspaceScopedHandlers_SuccessPaths registers this route, since
// both are keyed off a workspace-lookup or config failure that this
// handler's remaining behavior — decoding the request, delegating to
// the coordinator, and encoding its answer — doesn't exercise. This
// mirrors the closest POST-with-body agent route (active-agent) by
// wiring a mock coordinator directly, the same way
// TestActiveAgentErrorsCarryTheRightStatus does for its error paths.
func TestPostWorkspaceAgentSessionCommitMessage_Success(t *testing.T) {
	t.Parallel()

	coord := NewMockCoordinator(gomock.NewController(t))
	coord.EXPECT().GenerateCommitMessage(gomock.Any(), "sess1", "diff --git a/x b/x").Return("fix: correct the bug", nil)

	c, wsID := buildAgentWorkspace(t, coord)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
		strings.NewReader(`{"diff":"diff --git a/x b/x"}`))
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", "sess1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceAgentSessionCommitMessage(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp proto.CommitMessageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "fix: correct the bug", resp.Message)
}

// TestPostWorkspaceAgentSessionCommitMessage_DecodeError pins that a
// malformed request body is rejected before the coordinator is ever
// consulted — the mock has no expectation set, so an unwanted call
// to GenerateCommitMessage would fail the test on its own.
func TestPostWorkspaceAgentSessionCommitMessage_DecodeError(t *testing.T) {
	t.Parallel()

	coord := NewMockCoordinator(gomock.NewController(t))
	c, wsID := buildAgentWorkspace(t, coord)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("not json"))
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", "sess1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceAgentSessionCommitMessage(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestPostWorkspaceAgentSessionCommitMessage_BackendError pins that a
// coordinator failure is translated through the shared handleError
// path, distinctly from the decode-error and success paths above.
func TestPostWorkspaceAgentSessionCommitMessage_BackendError(t *testing.T) {
	t.Parallel()

	coord := NewMockCoordinator(gomock.NewController(t))
	coord.EXPECT().GenerateCommitMessage(gomock.Any(), "sess1", "diff --git a/x b/x").Return("", errors.New("boom"))

	c, wsID := buildAgentWorkspace(t, coord)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
		strings.NewReader(`{"diff":"diff --git a/x b/x"}`))
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", "sess1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceAgentSessionCommitMessage(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
