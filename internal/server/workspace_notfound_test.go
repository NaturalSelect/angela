package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/backend"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// wsHandlerCase describes one workspace-scoped handler under test. Every
// handler in the table follows the same shape: read the "id" path
// value, optionally decode a JSON body, call a backend method, and map
// a missing workspace to 404 via handleError. invoke is a method
// expression (e.g. (*controllerV1).handleGetWorkspace) bound to the
// receiver at call time.
type wsHandlerCase struct {
	name    string
	body    string // "" means no request body is sent
	decodes bool   // true if the handler decodes r.Body as JSON
	invoke  func(c *controllerV1, w http.ResponseWriter, r *http.Request)
}

// wsHandlerCases enumerates every workspace-scoped handler (across
// proto.go and config.go) whose first observable behavior for an
// unknown workspace ID is a 404 produced by handleError. The request
// method is irrelevant here since these tests call handlers directly,
// bypassing the mux that would otherwise enforce it.
var wsHandlerCases = []wsHandlerCase{
	{name: "GetWorkspace", invoke: (*controllerV1).handleGetWorkspace},
	{name: "GetWorkspaceConfig", invoke: (*controllerV1).handleGetWorkspaceConfig},
	{name: "GetWorkspaceProviders", invoke: (*controllerV1).handleGetWorkspaceProviders},
	{name: "GetWorkspaceLSPs", invoke: (*controllerV1).handleGetWorkspaceLSPs},
	{name: "GetWorkspaceLSPDiagnostics", invoke: (*controllerV1).handleGetWorkspaceLSPDiagnostics},
	{name: "PostWorkspaceSessions", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceSessions},
	{name: "GetWorkspaceSessionHistory", invoke: (*controllerV1).handleGetWorkspaceSessionHistory},
	{name: "GetWorkspaceSessionMessages", invoke: (*controllerV1).handleGetWorkspaceSessionMessages},
	{name: "PutWorkspaceSession", body: "{}", decodes: true, invoke: (*controllerV1).handlePutWorkspaceSession},
	{name: "DeleteWorkspaceSession", invoke: (*controllerV1).handleDeleteWorkspaceSession},
	{name: "GetWorkspaceSessionUserMessages", invoke: (*controllerV1).handleGetWorkspaceSessionUserMessages},
	{name: "GetWorkspaceAllUserMessages", invoke: (*controllerV1).handleGetWorkspaceAllUserMessages},
	{name: "GetWorkspaceSessionFileTrackerFiles", invoke: (*controllerV1).handleGetWorkspaceSessionFileTrackerFiles},
	{name: "PostWorkspaceFileTrackerRead", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceFileTrackerRead},
	{name: "GetWorkspaceFileTrackerLastRead", invoke: (*controllerV1).handleGetWorkspaceFileTrackerLastRead},
	{name: "PostWorkspaceLSPStart", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceLSPStart},
	{name: "PostWorkspaceLSPStopAll", invoke: (*controllerV1).handlePostWorkspaceLSPStopAll},
	{name: "GetWorkspaceAgent", invoke: (*controllerV1).handleGetWorkspaceAgent},
	{name: "PostWorkspaceAgentInit", invoke: (*controllerV1).handlePostWorkspaceAgentInit},
	{name: "PostWorkspaceAgentUpdate", invoke: (*controllerV1).handlePostWorkspaceAgentUpdate},
	{name: "PostWorkspaceAgentSessionCancel", invoke: (*controllerV1).handlePostWorkspaceAgentSessionCancel},
	{name: "PostWorkspaceAgentSessionAbandonBranch", invoke: (*controllerV1).handlePostWorkspaceAgentSessionAbandonBranch},
	{name: "GetWorkspaceAgentSessionPromptQueued", invoke: (*controllerV1).handleGetWorkspaceAgentSessionPromptQueued},
	{name: "PostWorkspaceAgentSessionPromptClear", invoke: (*controllerV1).handlePostWorkspaceAgentSessionPromptClear},
	{name: "PostWorkspaceAgentSessionSummarize", invoke: (*controllerV1).handlePostWorkspaceAgentSessionSummarize},
	{name: "GetWorkspaceAgentDefaultActiveAgent", invoke: (*controllerV1).handleGetWorkspaceAgentDefaultActiveAgent},
	{name: "PostWorkspaceAgentSessionShell", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceAgentSessionShell},
	{name: "GetWorkspaceAgentSessionPromptList", invoke: (*controllerV1).handleGetWorkspaceAgentSessionPromptList},
	{name: "PostWorkspacePermissionsGrant", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspacePermissionsGrant},
	{name: "PostWorkspaceQuestionsAnswer", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceQuestionsAnswer},
	{name: "PostWorkspaceQuestionsCancel", invoke: (*controllerV1).handlePostWorkspaceQuestionsCancel},
	{name: "PostWorkspacePermissionsMode", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspacePermissionsMode},
	{name: "GetWorkspacePermissionsMode", invoke: (*controllerV1).handleGetWorkspacePermissionsMode},

	{name: "PostWorkspaceConfigSet", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigSet},
	{name: "PostWorkspaceConfigRemove", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigRemove},
	{name: "PostWorkspaceConfigModel", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigModel},
	{name: "PostWorkspaceConfigRecentModel", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigRecentModel},
	{name: "PostWorkspaceConfigPruneRecentModels", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigPruneRecentModels},
	{name: "PostWorkspaceConfigCompact", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigCompact},
	{
		name: "PostWorkspaceConfigProviderKey", decodes: true,
		body:   `{"scope":0,"provider_id":"p1","kind":"string","api_key":"secret"}`,
		invoke: (*controllerV1).handlePostWorkspaceConfigProviderKey,
	},
	{name: "PostWorkspaceConfigProviderModel", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigProviderModel},
	{name: "PostWorkspaceConfigImportCopilot", invoke: (*controllerV1).handlePostWorkspaceConfigImportCopilot},
	{name: "PostWorkspaceConfigRefreshOAuth", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceConfigRefreshOAuth},
	{name: "GetWorkspaceProjectNeedsInit", invoke: (*controllerV1).handleGetWorkspaceProjectNeedsInit},
	{name: "PostWorkspaceProjectInit", invoke: (*controllerV1).handlePostWorkspaceProjectInit},
	{name: "GetWorkspaceProjectInitPrompt", invoke: (*controllerV1).handleGetWorkspaceProjectInitPrompt},
	{name: "GetWorkspaceSkills", invoke: (*controllerV1).handleGetWorkspaceSkills},
	{name: "PostWorkspaceSkillRead", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceSkillRead},
	{name: "PostWorkspaceMCPEnableDocker", invoke: (*controllerV1).handlePostWorkspaceMCPEnableDocker},
	{name: "PostWorkspaceMCPDisableDocker", invoke: (*controllerV1).handlePostWorkspaceMCPDisableDocker},
	{name: "PostWorkspaceMCPRefreshTools", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceMCPRefreshTools},
	{name: "PostWorkspaceMCPReadResource", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceMCPReadResource},
	{name: "GetWorkspaceMCPPrompts", invoke: (*controllerV1).handleGetWorkspaceMCPPrompts},
	{name: "PostWorkspaceMCPGetPrompt", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceMCPGetPrompt},
	{name: "GetWorkspaceMCPPendingAuth", invoke: (*controllerV1).handleGetWorkspaceMCPPendingAuth},
	{name: "PostWorkspaceMCPAuth", body: "{}", decodes: true, invoke: (*controllerV1).handlePostWorkspaceMCPAuth},
}

func newRequestForWSCase(t *testing.T, id, sid, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", r)
	req.SetPathValue("id", id)
	req.SetPathValue("sid", sid)
	req.SetPathValue("lsp", "gopls")
	return req
}

// TestWorkspaceScopedHandlers_WorkspaceNotFound drives every handler in
// wsHandlerCases against a backend with no workspaces registered,
// asserting that the well-formed request path reaches the shared
// GetWorkspace lookup and comes back as a 404 via handleError.
func TestWorkspaceScopedHandlers_WorkspaceNotFound(t *testing.T) {
	t.Parallel()

	b := backend.New(context.Background(), nil, nil)
	c := &controllerV1{backend: b, server: &Server{backend: b}}
	id := uuid.New().String()

	for _, tc := range wsHandlerCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := newRequestForWSCase(t, id, "sid1", tc.body)
			rec := httptest.NewRecorder()
			tc.invoke(c, rec, req)
			require.Equal(t, http.StatusNotFound, rec.Code, "expected 404 for an unknown workspace")
		})
	}
}

// TestWorkspaceScopedHandlers_MalformedBody verifies that every
// body-decoding handler in wsHandlerCases rejects invalid JSON with a
// 400 before ever reaching the backend.
func TestWorkspaceScopedHandlers_MalformedBody(t *testing.T) {
	t.Parallel()

	b := backend.New(context.Background(), nil, nil)
	c := &controllerV1{backend: b, server: &Server{backend: b}}
	id := uuid.New().String()

	for _, tc := range wsHandlerCases {
		if !tc.decodes {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := newRequestForWSCase(t, id, "sid1", "not json")
			rec := httptest.NewRecorder()
			tc.invoke(c, rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for a malformed body")
		})
	}
}
