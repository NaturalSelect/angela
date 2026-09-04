package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NaturalSelect/angela/internal/client"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestClientWorkspace_AgentRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "accepted", status: http.StatusAccepted},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody proto.AgentMessage
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/agent", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				w.WriteHeader(tc.status)
			})

			err := ws.AgentRun(t.Context(), "s1", "do the thing")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "s1", gotBody.SessionID)
			require.Equal(t, "do the thing", gotBody.Prompt)
			require.Empty(t, gotBody.RunID, "interactive TUI runs must not stamp a correlator")
		})
	}
}

func TestClientWorkspace_AgentRunShellCommand(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/shell", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.ShellCommandResponse{Output: "hi\n", ExitCode: 0}))
			})

			got, err := ws.AgentRunShellCommand(t.Context(), "s1", "echo hi", 80, nil, false)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, proto.ShellCommandResponse{Output: "hi\n", ExitCode: 0}, got)
		})
	}
}

func TestClientWorkspace_AgentCancel(t *testing.T) {
	t.Parallel()

	var gotPath string
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	ws.AgentCancel("s1")
	require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/cancel", gotPath)
}

func TestClientWorkspace_AgentAbandonBranch(t *testing.T) {
	t.Parallel()

	var gotPath string
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	ws.AgentAbandonBranch("s1")
	require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/abandon-branch", gotPath)
}

func TestClientWorkspace_AgentIsBusy(t *testing.T) {
	t.Parallel()

	t.Run("busy", func(t *testing.T) {
		t.Parallel()
		ws := agentInfoWorkspace(t, proto.AgentInfo{IsBusy: true})
		require.True(t, ws.AgentIsBusy())
	})

	t.Run("server error defaults to not busy", func(t *testing.T) {
		t.Parallel()
		c, err := client.NewClient(t.TempDir(), "tcp", "127.0.0.1:1")
		require.NoError(t, err)
		ws := NewClientWorkspace(c, proto.Workspace{ID: "ws-1"})
		require.False(t, ws.AgentIsBusy())
	})
}

func TestClientWorkspace_AgentIsSessionBusyAndBranch(t *testing.T) {
	t.Parallel()

	t.Run("busy branch", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(proto.AgentSession{
				IsBusy: true, IsBranch: true,
			}))
		})

		require.True(t, ws.AgentIsSessionBusy("s1"))
		require.True(t, ws.AgentIsSessionBranch("s1"))
	})

	t.Run("server error defaults to false", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.False(t, ws.AgentIsSessionBusy("s1"))
		require.False(t, ws.AgentIsSessionBranch("s1"))
	})
}

func TestClientWorkspace_AgentQueuedPrompts(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/prompts/queued", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(3))
		})

		require.Equal(t, 3, ws.AgentQueuedPrompts("s1"))
	})

	t.Run("server error defaults to zero", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Equal(t, 0, ws.AgentQueuedPrompts("s1"))
	})
}

func TestClientWorkspace_AgentQueuedPromptsList(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/prompts/list", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode([]string{"first", "second"}))
		})

		require.Equal(t, []string{"first", "second"}, ws.AgentQueuedPromptsList("s1"))
	})

	t.Run("server error defaults to nil", func(t *testing.T) {
		t.Parallel()
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		require.Nil(t, ws.AgentQueuedPromptsList("s1"))
	})
}

func TestClientWorkspace_AgentClearQueue(t *testing.T) {
	t.Parallel()

	var gotPath string
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	ws.AgentClearQueue("s1")
	require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/prompts/clear", gotPath)
}

func TestClientWorkspace_AgentActive_ServerError(t *testing.T) {
	t.Parallel()

	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := ws.AgentActive(t.Context(), "s1")
	require.Error(t, err)
}

func TestClientWorkspace_AgentEditActive(t *testing.T) {
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

			var gotBody config.ActiveAgentEdit
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/active-agent", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.ActiveAgent{
					AgentID: "coder", AgentName: "Coder", Think: true,
				}))
			})

			edit := config.ActiveAgentEdit{Agent: "coder", ToggleThink: true}
			got, err := ws.AgentEditActive(t.Context(), "s1", edit)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "coder", gotBody.Agent)
			require.True(t, gotBody.ToggleThink)
			require.Equal(t, ActiveAgent{AgentID: "coder", AgentName: "Coder", Think: true}, got)
		})
	}
}

func TestClientWorkspace_AgentSummarize(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/agent/sessions/s1/summarize", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			err := ws.AgentSummarize(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_UpdateAgentModel(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/agent/update", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			err := ws.UpdateAgentModel(t.Context())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClientWorkspace_InitCoderAgent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		call        func(*ClientWorkspace, context.Context) error
		interactive bool
		wantErr     bool
	}{
		{
			name:        "InitCoderAgent interactive",
			call:        (*ClientWorkspace).InitCoderAgent,
			interactive: true,
		},
		{
			name:        "InitCoderAgentNonInteractive",
			call:        (*ClientWorkspace).InitCoderAgentNonInteractive,
			interactive: false,
		},
		{
			name:    "InitCoderAgent server error",
			call:    (*ClientWorkspace).InitCoderAgent,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody proto.AgentInitRequest
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/workspaces/ws-1/agent/init", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			err := tc.call(ws, t.Context())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.interactive, gotBody.Interactive)
		})
	}
}
