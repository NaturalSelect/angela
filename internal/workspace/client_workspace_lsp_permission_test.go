package workspace

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestClientWorkspace_PermissionMode(t *testing.T) {
	t.Parallel()

	t.Run("parses the wire mode", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/permissions/mode", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(proto.PermissionModeRequest{Mode: "yolo"}))
		})

		require.Equal(t, permission.ModeYolo, ws.PermissionMode())
	})

	t.Run("server error defaults to manual", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Equal(t, permission.ModeManual, ws.PermissionMode())
	})
}

func TestClientWorkspace_PermissionSetMode(t *testing.T) {
	t.Parallel()

	var gotBody proto.PermissionModeRequest
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workspaces/ws-1/permissions/mode", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusOK)
	})

	ws.PermissionSetMode(permission.ModeAutoAcceptEdits)
	require.Equal(t, "auto_accept_edits", gotBody.Mode)
}

func TestClientWorkspace_QuestionAnswer(t *testing.T) {
	t.Parallel()

	t.Run("resolved", func(t *testing.T) {
		t.Parallel()

		var gotBody proto.QuestionAnswer
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/questions/answer", r.URL.Path)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			require.NoError(t, json.NewEncoder(w).Encode(proto.QuestionAnswerResponse{Resolved: true}))
		})

		yes := true
		got := ws.QuestionAnswer([]question.Answer{
			{QuestionID: "q1", SelectedIDs: []string{"a"}, FillInText: "note", Yes: &yes},
		})
		require.True(t, got)
		require.Len(t, gotBody.Responses, 1)
		require.Equal(t, "q1", gotBody.Responses[0].QuestionID)
		require.Equal(t, []string{"a"}, gotBody.Responses[0].SelectedIDs)
		require.True(t, *gotBody.Responses[0].Yes)
	})

	t.Run("server error defaults to false", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.False(t, ws.QuestionAnswer([]question.Answer{{QuestionID: "q1"}}))
	})
}

func TestClientWorkspace_QuestionCancel(t *testing.T) {
	t.Parallel()

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/questions/cancel", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(proto.QuestionAnswerResponse{Resolved: true}))
		})

		require.True(t, ws.QuestionCancel())
	})

	t.Run("server error defaults to false", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.False(t, ws.QuestionCancel())
	})
}

func TestClientWorkspace_LSPStart(t *testing.T) {
	t.Parallel()

	var gotPath, gotBody string
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Path string `json:"path"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotBody = body.Path
		w.WriteHeader(http.StatusOK)
	})

	ws.LSPStart(t.Context(), "/tmp/project")
	require.Equal(t, "/v1/workspaces/ws-1/lsps/start", gotPath)
	require.Equal(t, "/tmp/project", gotBody)
}

func TestClientWorkspace_LSPStopAll(t *testing.T) {
	t.Parallel()

	var gotPath string
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	ws.LSPStopAll(t.Context())
	require.Equal(t, "/v1/workspaces/ws-1/lsps/stop", gotPath)
}

func TestClientWorkspace_LSPGetStates(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		connectedAt := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/lsps", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]proto.LSPClientInfo{
				"gopls": {
					Name: "gopls", State: lsp.StateReady, DiagnosticCount: 2,
					ConnectedAt: connectedAt,
				},
			}))
		})

		got := ws.LSPGetStates()
		require.Len(t, got, 1)
		require.Equal(t, LSPClientInfo{
			Name: "gopls", State: lsp.StateReady, DiagnosticCount: 2,
			ConnectedAt: connectedAt,
		}, got["gopls"])
	})

	t.Run("server error returns nil", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Nil(t, ws.LSPGetStates())
	})
}

func TestClientWorkspace_LSPGetDiagnosticCounts(t *testing.T) {
	t.Parallel()

	t.Run("counts every severity", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/lsps/gopls/diagnostics", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(map[protocol.DocumentURI][]protocol.Diagnostic{
				"file:///a.go": {
					{Severity: protocol.SeverityError},
					{Severity: protocol.SeverityWarning},
					{Severity: protocol.SeverityInformation},
					{Severity: protocol.SeverityHint},
					{Severity: protocol.SeverityError},
				},
			}))
		})

		got := ws.LSPGetDiagnosticCounts("gopls")
		require.Equal(t, lsp.DiagnosticCounts{Error: 2, Warning: 1, Information: 1, Hint: 1}, got)
	})

	t.Run("server error returns zero counts", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Equal(t, lsp.DiagnosticCounts{}, ws.LSPGetDiagnosticCounts("gopls"))
	})
}

// TestClientWorkspace_WorkingDirAndResolver pins two read-only accessors
// backed entirely by the cached workspace snapshot, with no server round
// trip: WorkingDir surfaces the cached path, and Resolver always hands
// back an identity resolver since a remote client has no local
// environment to resolve variables against.
func TestClientWorkspace_WorkingDirAndResolver(t *testing.T) {
	t.Parallel()

	ws := NewClientWorkspace(nil, proto.Workspace{ID: "ws-1", Path: "/home/user/project"})

	require.Equal(t, "/home/user/project", ws.WorkingDir())

	resolver := ws.Resolver()
	require.NotNil(t, resolver)
	got, err := resolver.ResolveValue("$SOME_VAR")
	require.NoError(t, err)
	require.Equal(t, "$SOME_VAR", got, "the client-side resolver must be a no-op identity resolver")
}
