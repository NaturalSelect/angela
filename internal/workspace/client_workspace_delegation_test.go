package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/client"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/undo"
	"github.com/stretchr/testify/require"
)

// testClientWorkspace returns a ClientWorkspace backed by an httptest
// server running handler, for the given workspace ID. Shared by the
// delegation-focused test files in this package.
func testClientWorkspace(t *testing.T, wsID string, handler http.HandlerFunc) *ClientWorkspace {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	return NewClientWorkspace(c, proto.Workspace{ID: wsID})
}

func TestClientWorkspace_CreateSession(t *testing.T) {
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

			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/sessions", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(tc.status)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.Session{ID: "s1", Title: "hello"}))
			})

			got, err := ws.CreateSession(t.Context(), "hello")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, session.Session{ID: "s1", Title: "hello"}, got)
		})
	}
}

func TestClientWorkspace_GetSession(t *testing.T) {
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
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.Session{ID: "s1", Title: "resumed"}))
			})

			got, err := ws.GetSession(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "resumed", got.Title)
		})
	}
}

func TestClientWorkspace_SaveSession(t *testing.T) {
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
				require.Equal(t, http.MethodPut, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.Session{ID: "s1", Title: "renamed"}))
			})

			got, err := ws.SaveSession(t.Context(), session.Session{ID: "s1", Title: "renamed"})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "renamed", got.Title)
		})
	}
}

func TestClientWorkspace_DeleteSession(t *testing.T) {
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
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			err := ws.DeleteSession(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestClientWorkspace_AgentToolSessionIDRoundTrip pins the combined
// tool-session ID format used to scope task-tool sub-sessions: it must
// survive a create/parse round trip and reject malformed input rather
// than silently truncating it.
func TestClientWorkspace_AgentToolSessionIDRoundTrip(t *testing.T) {
	t.Parallel()

	ws := &ClientWorkspace{}

	cases := []struct {
		name       string
		messageID  string
		toolCallID string
	}{
		{name: "simple ids", messageID: "msg-1", toolCallID: "call-1"},
		{name: "empty ids", messageID: "", toolCallID: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			combined := ws.CreateAgentToolSessionID(tc.messageID, tc.toolCallID)
			gotMsg, gotCall, ok := ws.ParseAgentToolSessionID(combined)
			require.True(t, ok)
			require.Equal(t, tc.messageID, gotMsg)
			require.Equal(t, tc.toolCallID, gotCall)
		})
	}
}

func TestClientWorkspace_ParseAgentToolSessionID_Malformed(t *testing.T) {
	t.Parallel()

	ws := &ClientWorkspace{}

	cases := []struct {
		name      string
		sessionID string
	}{
		{name: "plain session id", sessionID: "sess-1"},
		{name: "too many separators", sessionID: "a$$b$$c"},
		{name: "empty string", sessionID: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, ok := ws.ParseAgentToolSessionID(tc.sessionID)
			require.False(t, ok)
		})
	}
}

func TestClientWorkspace_SetCurrentSession(t *testing.T) {
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

			var gotBody proto.CurrentSession
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/current-session", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			err := ws.SetCurrentSession(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "s1", gotBody.SessionID)
			require.Equal(t, "s1", ws.lastSession)
		})
	}
}

func TestClientWorkspace_ListMessages(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1/messages", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]proto.Message{
					{ID: "m1", SessionID: "s1", Role: proto.User},
				}))
			})

			got, err := ws.ListMessages(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []message.Message{{ID: "m1", SessionID: "s1", Role: message.MessageRole(proto.User)}}, got)
		})
	}
}

func TestClientWorkspace_ListUserMessages(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1/messages/user", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]proto.Message{
					{ID: "m1", SessionID: "s1", Role: proto.User},
				}))
			})

			got, err := ws.ListUserMessages(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, "m1", got[0].ID)
		})
	}
}

func TestClientWorkspace_ListAllUserMessages(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/messages/user", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]proto.Message{
					{ID: "m2", SessionID: "s2", Role: proto.User},
				}))
			})

			got, err := ws.ListAllUserMessages(t.Context())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, "m2", got[0].ID)
		})
	}
}

func TestClientWorkspace_FileTrackerRecordRead(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	var gotBody struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
	}
	ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		require.Equal(t, "/v1/workspaces/ws-1/filetracker/read", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusOK)
	})

	ws.FileTrackerRecordRead(t.Context(), "s1", "/tmp/file.go")
	require.True(t, called.Load())
	require.Equal(t, "s1", gotBody.SessionID)
	require.Equal(t, "/tmp/file.go", gotBody.Path)
}

func TestClientWorkspace_FileTrackerLastReadTime(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/workspaces/ws-1/filetracker/lastread", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(want))
		})

		got := ws.FileTrackerLastReadTime(t.Context(), "s1", "/tmp/file.go")
		require.True(t, want.Equal(got))
	})

	t.Run("server error returns zero time", func(t *testing.T) {
		t.Parallel()

		ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		got := ws.FileTrackerLastReadTime(t.Context(), "s1", "/tmp/file.go")
		require.True(t, got.IsZero())
	})
}

func TestClientWorkspace_FileTrackerListReadFiles(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1/filetracker/files", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]string{"/tmp/a.go", "/tmp/b.go"}))
			})

			got, err := ws.FileTrackerListReadFiles(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{"/tmp/a.go", "/tmp/b.go"}, got)
		})
	}
}

func TestClientWorkspace_ListSessionHistory(t *testing.T) {
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
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1/history", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode([]proto.File{
					{ID: "f1", SessionID: "s1", Path: "/tmp/a.go", Version: 2},
				}))
			})

			got, err := ws.ListSessionHistory(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []history.File{{ID: "f1", SessionID: "s1", Path: "/tmp/a.go", Version: 2}}, got)
		})
	}
}

func TestClientWorkspace_PreviewUndo(t *testing.T) {
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
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1/undo", r.URL.Path)
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.UndoPreview{
					CutMessageID: "m1",
					PoppedText:   "popped",
					MessageCount: 3,
					Revert:       []string{"/tmp/a.go"},
					Delete:       []string{"/tmp/b.go"},
					Skipped:      []proto.UndoSkippedFile{{Path: "/tmp/c.go", Reason: "locked"}},
				}))
			})

			got, err := ws.PreviewUndo(t.Context(), "s1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, undo.Preview{
				CutMessageID: "m1",
				PoppedText:   "popped",
				MessageCount: 3,
				Revert:       []string{"/tmp/a.go"},
				Delete:       []string{"/tmp/b.go"},
				Skipped:      []undo.SkippedFile{{Path: "/tmp/c.go", Reason: "locked"}},
			}, got)
		})
	}
}

func TestClientWorkspace_Undo(t *testing.T) {
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

			var gotBody proto.UndoRequest
			ws := testClientWorkspace(t, "ws-1", func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/ws-1/sessions/s1/undo", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				if tc.wantErr {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(proto.UndoResult{
					PoppedText:   "popped",
					Reverted:     []string{"/tmp/a.go"},
					Deleted:      []string{"/tmp/b.go"},
					MessageCount: 2,
				}))
			})

			got, err := ws.Undo(t.Context(), "s1", "m1")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "m1", gotBody.CutMessageID)
			require.Equal(t, undo.Result{
				PoppedText:   "popped",
				Reverted:     []string{"/tmp/a.go"},
				Deleted:      []string{"/tmp/b.go"},
				MessageCount: 2,
			}, got)
		})
	}
}
