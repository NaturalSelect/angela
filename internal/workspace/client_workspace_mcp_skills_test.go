package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/client"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestClientWorkspace_ListSkills(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantErr bool
	}{
		{name: "success"},
		{name: "server error", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/workspaces/ws-1/skills", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]proto.SkillInfo{
					{ID: "s1", Name: "Formatter", Description: "formats code", Label: "fmt", Source: "project", UserInvocable: true},
				}))
			})

			got, err := ws.ListSkills(t.Context())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []skills.CatalogEntry{
				{ID: "s1", Name: "Formatter", Description: "formats code", Label: "fmt", Source: skills.SourceProject, UserInvocable: true},
			}, got)
		})
	}
}

func TestClientWorkspace_ReadSkill(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantErr bool
	}{
		{name: "success"},
		{name: "server error", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody proto.ReadSkillRequest
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/workspaces/ws-1/skills/read", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.ReadSkillResponse{
					Content: []byte("# Skill"),
					Result:  proto.SkillReadResult{Name: "Formatter", Description: "formats code", Source: "system", Builtin: true},
				}))
			})

			content, result, err := ws.ReadSkill(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "s1", gotBody.SkillID)
			require.Equal(t, []byte("# Skill"), content)
			require.Equal(t, skills.SkillReadResult{Name: "Formatter", Description: "formats code", Source: skills.SourceSystem, Builtin: true}, result)
		})
	}
}

func TestClientWorkspace_MCPGetStates(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		connectedAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/mcp/states", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]proto.MCPClientInfo{
				"github": {
					Name: "github", State: proto.MCPStateConnected,
					ToolCount: 3, PromptCount: 1, ResourceCount: 2,
					ConnectedAt: connectedAt,
				},
			}))
		})

		got := ws.MCPGetStates()
		require.Len(t, got, 1)
		require.Equal(t, mcptools.ClientInfo{
			Name:  "github",
			State: mcptools.StateConnected,
			Counts: mcptools.Counts{
				Tools: 3, Prompts: 1, Resources: 2,
			},
			ConnectedAt: connectedAt,
		}, got["github"])
	})

	t.Run("server error returns nil", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Nil(t, ws.MCPGetStates())
	})
}

func TestClientWorkspace_MCPRefreshPromptsAndResources(t *testing.T) {
	t.Parallel()

	t.Run("refresh prompts", func(t *testing.T) {
		t.Parallel()
		var gotPath string
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		ws.MCPRefreshPrompts(t.Context(), "github")
		require.Equal(t, "/v1/workspaces/ws-1/mcp/refresh-prompts", gotPath)
	})

	t.Run("refresh resources", func(t *testing.T) {
		t.Parallel()
		var gotPath string
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		ws.MCPRefreshResources(t.Context(), "github")
		require.Equal(t, "/v1/workspaces/ws-1/mcp/refresh-resources", gotPath)
	})

	t.Run("refresh tools", func(t *testing.T) {
		t.Parallel()
		var gotPath string
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		ws.RefreshMCPTools(t.Context(), "github")
		require.Equal(t, "/v1/workspaces/ws-1/mcp/refresh-tools", gotPath)
	})
}

func TestClientWorkspace_ReadMCPResource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantErr bool
	}{
		{name: "success"},
		{name: "server error", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/workspaces/ws-1/mcp/read-resource", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]client.MCPResourceContents{
					{URI: "file:///a.txt", MIMEType: "text/plain", Text: "hello"},
				}))
			})

			got, err := ws.ReadMCPResource(t.Context(), "github", "file:///a.txt")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []MCPResourceContents{
				{URI: "file:///a.txt", MIMEType: "text/plain", Text: "hello"},
			}, got)
		})
	}
}

func TestClientWorkspace_GetMCPPrompt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantErr bool
	}{
		{name: "success"},
		{name: "server error", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/workspaces/ws-1/mcp/get-prompt", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(struct {
					Prompt string `json:"prompt"`
				}{Prompt: "review this PR"}))
			})

			got, err := ws.GetMCPPrompt("github", "review", map[string]string{"focus": "security"})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "review this PR", got)
		})
	}
}

func TestClientWorkspace_EnableDisableDockerMCP(t *testing.T) {
	t.Parallel()

	t.Run("enable success", func(t *testing.T) {
		t.Parallel()
		var gotPath string
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		require.NoError(t, ws.EnableDockerMCP(t.Context()))
		require.Equal(t, "/v1/workspaces/ws-1/mcp/docker/enable", gotPath)
	})

	t.Run("enable error", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		require.Error(t, ws.EnableDockerMCP(t.Context()))
	})

	t.Run("disable success", func(t *testing.T) {
		t.Parallel()
		var gotPath string
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		})
		require.NoError(t, ws.DisableDockerMCP())
		require.Equal(t, "/v1/workspaces/ws-1/mcp/docker/disable", gotPath)
	})

	t.Run("disable error", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		require.Error(t, ws.DisableDockerMCP())
	})
}

func TestClientWorkspace_SandboxOps(t *testing.T) {
	t.Parallel()

	t.Run("IsInSandbox success", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/v1/workspaces/ws-1/sandbox", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(proto.SandboxStatusResponse{InSandbox: true}))
		})
		require.True(t, ws.IsInSandbox())
	})

	t.Run("IsInSandbox error swallows and returns false", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		require.False(t, ws.IsInSandbox())
	})

	t.Run("EnterSandbox success", func(t *testing.T) {
		t.Parallel()
		var gotPath string
		var gotBody proto.EnterSandboxRequest
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			w.WriteHeader(http.StatusOK)
		})
		require.NoError(t, ws.EnterSandbox(t.Context(), sandbox.Config{ReadWrite: []string{"/tmp"}, AllowNetwork: true}))
		require.Equal(t, "/v1/workspaces/ws-1/sandbox/enter", gotPath)
		require.Equal(t, []string{"/tmp"}, gotBody.ReadWrite)
		require.True(t, gotBody.AllowNetwork)
	})

	t.Run("EnterSandbox error", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		require.Error(t, ws.EnterSandbox(t.Context(), sandbox.Config{}))
	})
}

func TestClientWorkspace_MCPPendingAuth(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/mcp/pending-auth", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode([]proto.MCPPendingAuthServer{
				{Name: "github", URL: "https://github.com/login/oauth"},
			}))
		})

		got := ws.MCPPendingAuth()
		require.Equal(t, []mcptools.PendingAuthServer{{Name: "github", URL: "https://github.com/login/oauth"}}, got)
	})

	t.Run("server error returns nil", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Nil(t, ws.MCPPendingAuth())
	})
}

func TestClientWorkspace_MCPAuthURL(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/mcp/auth-url", r.URL.Path)
			require.Equal(t, "github", r.URL.Query().Get("name"))
			require.NoError(t, json.NewEncoder(w).Encode(proto.MCPAuthResponse{AuthURL: "https://example.com/auth"}))
		})

		require.Equal(t, "https://example.com/auth", ws.MCPAuthURL("github"))
	})

	t.Run("server error returns empty string", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Empty(t, ws.MCPAuthURL("github"))
	})
}

// TestClientWorkspace_MCPAuthenticate exercises the two outcomes that
// do not depend on opening a real local browser: the OAuth flow
// completing (success or failure) before the URL-polling ticker ever
// fires, and the caller's context being cancelled mid-flow. The
// ticker's own "open the browser" branch is intentionally left
// uncovered — driving it would call out to a real browser launcher as
// a side effect, which is unsafe in a unit test.
func TestClientWorkspace_MCPAuthenticate(t *testing.T) {
	t.Parallel()

	t.Run("completes successfully", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/mcp/auth", r.URL.Path)
			w.WriteHeader(http.StatusOK)
		})

		require.NoError(t, ws.MCPAuthenticate(t.Context(), "github"))
	})

	t.Run("server reports failure", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Error(t, ws.MCPAuthenticate(t.Context(), "github"))
	})

	t.Run("context already cancelled", func(t *testing.T) {
		t.Parallel()

		var called atomic.Bool
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			called.Store(true)
			w.WriteHeader(http.StatusOK)
		})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := ws.MCPAuthenticate(ctx, "github")
		require.Error(t, err)
		require.False(t, called.Load(), "an already-cancelled context must short-circuit before any request is sent")
	})
}
