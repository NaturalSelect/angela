package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

// captureClient returns a Client that talks to the given test server,
// plus a channel receiving the parsed request body for each call.
func captureClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	return c
}

func TestSetProviderAPIKeyStringSendsKind(t *testing.T) {
	t.Parallel()

	var got proto.ConfigProviderKeyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.SetProviderAPIKey(context.Background(), "ws1", config.ScopeGlobal, "openai", "sk-xyz"))

	require.Equal(t, proto.APIKeyKindString, got.Kind)
	require.Equal(t, "openai", got.ProviderID)
	require.Equal(t, config.ScopeGlobal, got.Scope)
	decoded, err := got.DecodeAPIKey()
	require.NoError(t, err)
	require.Equal(t, "sk-xyz", decoded)
}

func TestSetProviderAPIKeyOAuthSendsKind(t *testing.T) {
	t.Parallel()

	var got proto.ConfigProviderKeyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tok := &oauth.Token{AccessToken: "a", RefreshToken: "r", ExpiresIn: 60, ExpiresAt: 1234567890}
	c := captureClient(t, srv)
	require.NoError(t, c.SetProviderAPIKey(context.Background(), "ws1", config.ScopeGlobal, "acme", tok))

	require.Equal(t, proto.APIKeyKindOAuth, got.Kind)
	decoded, err := got.DecodeAPIKey()
	require.NoError(t, err)
	require.Equal(t, tok, decoded.(*oauth.Token))
}

func TestSetProviderAPIKeyUnsupportedTypeFailsLocally(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	err := c.SetProviderAPIKey(context.Background(), "ws1", config.ScopeGlobal, "x", 42)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported api key type")
	require.False(t, called, "server should not have been reached")
}

func TestSetProviderAPIKeyNilOAuthFailsLocally(t *testing.T) {
	t.Parallel()

	c := captureClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	var tok *oauth.Token
	err := c.SetProviderAPIKey(context.Background(), "ws1", config.ScopeGlobal, "x", tok)
	require.Error(t, err)
}

func TestListMCPPrompts(t *testing.T) {
	t.Parallel()

	want := []proto.MCPPrompt{
		{
			ID:          "server:review",
			Title:       "Review changes",
			Description: "Review the current changes.",
			PromptID:    "review",
			ClientID:    "server",
			Arguments: []proto.MCPPromptArgument{
				{ID: "focus", Title: "Focus", Description: "Area to review", Required: true},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/mcp/prompts", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).ListMCPPrompts(t.Context(), "ws1")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestListMCPPromptsNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.ListMCPPrompts(t.Context(), "ws1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestListMCPPromptsMalformedBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.ListMCPPrompts(t.Context(), "ws1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode MCP prompts")
}

func TestSetConfigFieldSuccess(t *testing.T) {
	t.Parallel()

	var got struct {
		Scope config.Scope `json:"scope"`
		Key   string       `json:"key"`
		Value any          `json:"value"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/config/set", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.SetConfigField(context.Background(), "ws1", config.ScopeGlobal, "options.debug", true))
	require.Equal(t, config.ScopeGlobal, got.Scope)
	require.Equal(t, "options.debug", got.Key)
	require.Equal(t, true, got.Value)
}

func TestSetConfigFieldNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	err := c.SetConfigField(context.Background(), "ws1", config.ScopeGlobal, "options.debug", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestListSkillsSuccess(t *testing.T) {
	t.Parallel()

	want := []proto.SkillInfo{{ID: "s1", Name: "Skill One", Source: "builtin"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/skills", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).ListSkills(context.Background(), "ws1")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestListSkillsNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).ListSkills(context.Background(), "ws1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestImportCopilotSuccess(t *testing.T) {
	t.Parallel()

	tok := &oauth.Token{AccessToken: "a", RefreshToken: "r", ExpiresIn: 60, ExpiresAt: 123}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/config/import-copilot", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(struct {
			Token   *oauth.Token `json:"token"`
			Success bool         `json:"success"`
		}{Token: tok, Success: true}))
	}))
	defer srv.Close()

	gotTok, gotOK, err := captureClient(t, srv).ImportCopilot(context.Background(), "ws1")
	require.NoError(t, err)
	require.True(t, gotOK)
	require.Equal(t, tok, gotTok)
}

func TestImportCopilotNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, err := captureClient(t, srv).ImportCopilot(context.Background(), "ws1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestReadMCPResourceSuccess(t *testing.T) {
	t.Parallel()

	want := []MCPResourceContents{{URI: "file:///a.go", MIMEType: "text/plain", Text: "hi"}}
	var gotBody struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/mcp/read-resource", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).ReadMCPResource(context.Background(), "ws1", "srv1", "file:///a.go")
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, "srv1", gotBody.Name)
	require.Equal(t, "file:///a.go", gotBody.URI)
}

func TestReadMCPResourceNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).ReadMCPResource(context.Background(), "ws1", "srv1", "file:///a.go")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestGetMCPPromptSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/mcp/get-prompt", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(struct {
			Prompt string `json:"prompt"`
		}{Prompt: "rendered prompt"}))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).GetMCPPrompt(context.Background(), "ws1", "srv1", "review", map[string]string{"focus": "security"})
	require.NoError(t, err)
	require.Equal(t, "rendered prompt", got)
}

func TestGetMCPPromptNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).GetMCPPrompt(context.Background(), "ws1", "srv1", "review", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestConfigMethodsSuccessPaths(t *testing.T) {
	t.Parallel()

	runProtoMethodCases(t, []protoMethodCase{
		{
			name:       "RemoveConfigField",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/config/remove",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.RemoveConfigField(context.Background(), "ws1", config.ScopeGlobal, "options.debug"))
			},
		},
		{
			name:       "UpdatePreferredModel",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/config/model",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.UpdatePreferredModel(context.Background(), "ws1", config.ScopeGlobal, config.SlotMain, config.SelectedModel{Provider: "mock", Model: "m"}))
			},
		},
		{
			name:       "PruneRecentModels",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/config/prune-recent-models",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.PruneRecentModels(context.Background(), "ws1", config.ScopeGlobal, config.SlotMain, []config.SelectedModel{{Provider: "mock", Model: "old"}}))
			},
		},
		{
			name:       "SetCompactMode",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/config/compact",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.SetCompactMode(context.Background(), "ws1", config.ScopeGlobal, true))
			},
		},
		{
			name:       "UpsertProviderModel",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/config/provider-model",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.UpsertProviderModel(context.Background(), "ws1", config.ScopeGlobal, "acme", config.ProviderModel{}))
			},
		},
		{
			name:       "RefreshOAuthToken",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/config/refresh-oauth",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.RefreshOAuthToken(context.Background(), "ws1", config.ScopeGlobal, "acme"))
			},
		},
		{
			name:       "ProjectNeedsInitialization",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/project/needs-init",
			body: mustJSON(t, struct {
				NeedsInit bool `json:"needs_init"`
			}{NeedsInit: true}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ProjectNeedsInitialization(context.Background(), "ws1")
				require.NoError(t, err)
				require.True(t, got)
			},
		},
		{
			name:       "MarkProjectInitialized",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/project/init",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.MarkProjectInitialized(context.Background(), "ws1"))
			},
		},
		{
			name:       "GetInitializePrompt",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/project/init-prompt",
			body: mustJSON(t, struct {
				Prompt string `json:"prompt"`
			}{Prompt: "please init"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetInitializePrompt(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, "please init", got)
			},
		},
		{
			name:       "ReadSkill",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/skills/read",
			body: mustJSON(t, proto.ReadSkillResponse{
				Content: []byte("skill body"),
				Result:  proto.SkillReadResult{Name: "s1"},
			}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ReadSkill(context.Background(), "ws1", "s1")
				require.NoError(t, err)
				require.Equal(t, "skill body", string(got.Content))
			},
		},
		{
			name:       "EnableDockerMCP",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/mcp/docker/enable",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.EnableDockerMCP(context.Background(), "ws1"))
			},
		},
		{
			name:       "DisableDockerMCP",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/mcp/docker/disable",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.DisableDockerMCP(context.Background(), "ws1"))
			},
		},
		{
			name:       "RefreshMCPTools",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/mcp/refresh-tools",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.RefreshMCPTools(context.Background(), "ws1", "srv1"))
			},
		},
	})
}

func TestConfigMethodsErrorPaths(t *testing.T) {
	t.Parallel()

	runProtoErrorCases(t, []protoErrorCase{
		{
			name: "UpsertProviderModel not found", status: http.StatusNotFound, message: "provider missing",
			wantErr: ErrNotFound, checkMsg: true,
			call: func(c *Client) error {
				return c.UpsertProviderModel(context.Background(), "ws1", config.ScopeGlobal, "acme", config.ProviderModel{})
			},
		},
		{
			name: "RemoveConfigField server error", status: http.StatusInternalServerError,
			call: func(c *Client) error {
				return c.RemoveConfigField(context.Background(), "ws1", config.ScopeGlobal, "options.debug")
			},
		},
		{
			name: "ListSkills server error", status: http.StatusInternalServerError,
			call: func(c *Client) error { _, err := c.ListSkills(context.Background(), "ws1"); return err },
		},
	})
}
