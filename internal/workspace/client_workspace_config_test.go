package workspace

import (
	"encoding/json"
	"net/http"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

// TestClientWorkspace_UpdatePreferredModel exercises all three
// refreshAfter outcomes: a failed write, a write that succeeds but
// whose follow-up workspace refresh fails (surfaced as an error even
// though the write landed, since the next Config() read would
// otherwise serve a stale snapshot), and a fully successful round
// trip.
func TestClientWorkspace_UpdatePreferredModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		mutateStatus    int
		refreshStatus   int
		wantErr         bool
		wantErrContains string
	}{
		{name: "success", mutateStatus: http.StatusOK, refreshStatus: http.StatusOK},
		{name: "write fails", mutateStatus: http.StatusInternalServerError, refreshStatus: http.StatusOK, wantErr: true},
		{
			name: "write ok but refresh fails", mutateStatus: http.StatusOK, refreshStatus: http.StatusInternalServerError,
			wantErr: true, wantErrContains: "saved, but refreshing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody struct {
				Scope config.Scope         `json:"scope"`
				Slot  config.SlotName      `json:"slot"`
				Model config.SelectedModel `json:"model"`
			}
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/workspaces/ws-1/config/model":
					require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
					w.WriteHeader(tc.mutateStatus)
				case "/v1/workspaces/ws-1":
					if tc.refreshStatus != http.StatusOK {
						w.WriteHeader(tc.refreshStatus)
						return
					}
					require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{ID: "ws-1"}))
				default:
					t.Fatalf("unexpected request path %s", r.URL.Path)
				}
			})

			model := config.SelectedModel{Model: "gpt-5", Provider: "openai"}
			err := ws.UpdatePreferredModel(config.ScopeGlobal, config.SlotMain, model)
			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrContains != "" {
					require.Contains(t, err.Error(), tc.wantErrContains)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, config.ScopeGlobal, gotBody.Scope)
			require.Equal(t, config.SlotMain, gotBody.Slot)
			require.Equal(t, model, gotBody.Model)
		})
	}
}

// configRefreshServer serves the config-mutation path with mutateStatus
// and the workspace-refresh GET with 200, used by the abbreviated
// config-mutation tests that only need to pin the write's own success
// and failure paths (refreshAfter's own branches are covered in full by
// TestClientWorkspace_UpdatePreferredModel).
func configRefreshServer(t *testing.T, mutatePath string, mutateStatus int, onMutate func(r *http.Request)) *ClientWorkspace {
	t.Helper()
	return testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mutatePath:
			if onMutate != nil {
				onMutate(r)
			}
			w.WriteHeader(mutateStatus)
		case "/v1/workspaces/ws-1":
			require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{ID: "ws-1"}))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	})
}

func TestClientWorkspace_RecordRecentModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/recent-model", tc.status, nil)
			err := ws.RecordRecentModel(config.ScopeGlobal, config.SlotMain, config.SelectedModel{Model: "gpt-5", Provider: "openai"})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_PruneRecentModels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/prune-recent-models", tc.status, nil)
			err := ws.PruneRecentModels(config.ScopeGlobal, config.SlotMain, []config.SelectedModel{{Model: "old", Provider: "openai"}})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_UpsertProviderModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/provider-model", tc.status, nil)
			err := ws.UpsertProviderModel(config.ScopeGlobal, "acme", config.ProviderModel{Model: catwalk.Model{ID: "m"}})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_SetProviderAPIKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody proto.ConfigProviderKeyRequest
			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/provider-key", tc.status, func(r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			})
			err := ws.SetProviderAPIKey(config.ScopeGlobal, "acme", "sk-secret")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "acme", gotBody.ProviderID)
			require.Equal(t, proto.APIKeyKindString, gotBody.Kind)
		})
	}
}

func TestClientWorkspace_SetConfigField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody struct {
				Scope config.Scope `json:"scope"`
				Key   string       `json:"key"`
				Value any          `json:"value"`
			}
			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/set", tc.status, func(r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			})
			err := ws.SetConfigField(config.ScopeGlobal, "options.debug", true)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "options.debug", gotBody.Key)
			require.Equal(t, true, gotBody.Value)
		})
	}
}

func TestClientWorkspace_RemoveConfigField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody struct {
				Scope config.Scope `json:"scope"`
				Key   string       `json:"key"`
			}
			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/remove", tc.status, func(r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
			})
			err := ws.RemoveConfigField(config.ScopeGlobal, "options.debug")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "options.debug", gotBody.Key)
		})
	}
}

func TestClientWorkspace_RefreshOAuthToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := configRefreshServer(t, "/v1/workspaces/ws-1/config/refresh-oauth", tc.status, nil)
			err := ws.RefreshOAuthToken(t.Context(), config.ScopeGlobal, "acme")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_ImportCopilot(t *testing.T) {
	t.Parallel()

	t.Run("imported and refreshes", func(t *testing.T) {
		t.Parallel()

		var refreshed bool
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/workspaces/ws-1/config/import-copilot":
				require.NoError(t, json.NewEncoder(w).Encode(struct {
					Token   *oauth.Token `json:"token"`
					Success bool         `json:"success"`
				}{Token: &oauth.Token{AccessToken: "tok"}, Success: true}))
			case "/v1/workspaces/ws-1":
				refreshed = true
				require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{ID: "ws-1"}))
			default:
				t.Fatalf("unexpected request path %s", r.URL.Path)
			}
		})

		token, ok := ws.ImportCopilot()
		require.True(t, ok)
		require.NotNil(t, token)
		require.Equal(t, "tok", token.AccessToken)
		require.True(t, refreshed, "a successful import must refresh the cached workspace")
	})

	t.Run("nothing to import skips refresh", func(t *testing.T) {
		t.Parallel()

		var refreshed bool
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/workspaces/ws-1/config/import-copilot":
				require.NoError(t, json.NewEncoder(w).Encode(struct {
					Token   *oauth.Token `json:"token"`
					Success bool         `json:"success"`
				}{Success: false}))
			case "/v1/workspaces/ws-1":
				refreshed = true
				require.NoError(t, json.NewEncoder(w).Encode(proto.Workspace{ID: "ws-1"}))
			default:
				t.Fatalf("unexpected request path %s", r.URL.Path)
			}
		})

		token, ok := ws.ImportCopilot()
		require.False(t, ok)
		require.Nil(t, token)
		require.False(t, refreshed, "no import means no reason to refresh")
	})

	t.Run("server error", func(t *testing.T) {
		t.Parallel()

		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		token, ok := ws.ImportCopilot()
		require.False(t, ok)
		require.Nil(t, token)
	})
}

func TestClientWorkspace_ProjectNeedsInitialization(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/project/needs-init", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(struct {
					NeedsInit bool `json:"needs_init"`
				}{NeedsInit: true}))
			})

			got, err := ws.ProjectNeedsInitialization()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, got)
		})
	}
}

func TestClientWorkspace_MarkProjectInitialized(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/project/init", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			err := ws.MarkProjectInitialized()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_InitializePrompt(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/project/init-prompt", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(struct {
					Prompt string `json:"prompt"`
				}{Prompt: "Describe this project"}))
			})

			got, err := ws.InitializePrompt()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "Describe this project", got)
		})
	}
}
