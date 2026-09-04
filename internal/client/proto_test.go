package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestSendEventAfterContextCancelIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events := make(chan any, 1)
	require.False(t, sendEvent(ctx, events, "one"))
	require.False(t, sendEvent(ctx, events, "two"))

	select {
	case ev := <-events:
		require.Failf(t, "unexpected event", "event: %v", ev)
	default:
	}
}

func TestSubscribeEventsContextCancelClosesEvents(t *testing.T) {
	t.Parallel()

	payload := marshalSSEPayload(t)
	firstEventSent := make(chan struct{})
	writeSecondEvent := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		_, err := fmt.Fprintf(w, "data: %s\n\n", payload)
		require.NoError(t, err)
		flusher.Flush()
		close(firstEventSent)

		select {
		case <-writeSecondEvent:
		case <-time.After(5 * time.Second):
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := captureClient(t, srv)
	events, err := c.SubscribeEvents(ctx, "ws1")
	require.NoError(t, err)

	select {
	case <-firstEventSent:
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for server event")
	}

	select {
	case <-events:
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for first event")
	}

	cancel()
	close(writeSecondEvent)

	select {
	case _, ok := <-events:
		require.False(t, ok)
	case <-time.After(5 * time.Second):
		require.Fail(t, "timed out waiting for event channel close")
	}
}

func TestSubscribeEventsDecodesAllPayloadTypes(t *testing.T) {
	t.Parallel()

	type payloadCase struct {
		name    string
		typ     pubsub.PayloadType
		payload []byte
	}

	cases := []payloadCase{
		{name: "lsp", typ: pubsub.PayloadTypeLSPEvent, payload: mustJSON(t, pubsub.Event[proto.LSPEvent]{Type: pubsub.CreatedEvent, Payload: proto.LSPEvent{Name: "gopls"}})},
		{name: "mcp", typ: pubsub.PayloadTypeMCPEvent, payload: mustJSON(t, pubsub.Event[proto.MCPEvent]{Type: pubsub.CreatedEvent, Payload: proto.MCPEvent{Name: "srv1"}})},
		{name: "permission request", typ: pubsub.PayloadTypePermissionRequest, payload: mustJSON(t, pubsub.Event[proto.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: proto.PermissionRequest{ID: "p1"}})},
		{name: "permission notification", typ: pubsub.PayloadTypePermissionNotification, payload: mustJSON(t, pubsub.Event[proto.PermissionNotification]{Type: pubsub.CreatedEvent, Payload: proto.PermissionNotification{ToolCallID: "t1"}})},
		{name: "question request", typ: pubsub.PayloadTypeQuestionRequest, payload: mustJSON(t, pubsub.Event[proto.QuestionRequest]{Type: pubsub.CreatedEvent, Payload: proto.QuestionRequest{ID: "q1"}})},
		{name: "question notification", typ: pubsub.PayloadTypeQuestionNotification, payload: mustJSON(t, pubsub.Event[proto.QuestionNotification]{Type: pubsub.CreatedEvent, Payload: proto.QuestionNotification{BatchID: "b1"}})},
		{name: "message", typ: pubsub.PayloadTypeMessage, payload: mustJSON(t, pubsub.Event[proto.Message]{Type: pubsub.CreatedEvent, Payload: proto.Message{ID: "m1"}})},
		{name: "session", typ: pubsub.PayloadTypeSession, payload: mustJSON(t, pubsub.Event[proto.Session]{Type: pubsub.CreatedEvent, Payload: proto.Session{ID: "s1"}})},
		{name: "file", typ: pubsub.PayloadTypeFile, payload: mustJSON(t, pubsub.Event[proto.File]{Type: pubsub.CreatedEvent, Payload: proto.File{ID: "f1"}})},
		{name: "config changed", typ: pubsub.PayloadTypeConfigChanged, payload: mustJSON(t, pubsub.Event[proto.ConfigChanged]{Type: pubsub.CreatedEvent, Payload: proto.ConfigChanged{WorkspaceID: "ws1"}})},
		{name: "skills", typ: pubsub.PayloadTypeSkillsEvent, payload: mustJSON(t, pubsub.Event[proto.SkillsEvent]{Type: pubsub.CreatedEvent, Payload: proto.SkillsEvent{}})},
		{name: "run complete", typ: pubsub.PayloadTypeRunComplete, payload: mustJSON(t, pubsub.Event[proto.RunComplete]{Type: pubsub.CreatedEvent, Payload: proto.RunComplete{SessionID: "s1"}})},
		{name: "update available", typ: pubsub.PayloadTypeUpdateAvailable, payload: mustJSON(t, pubsub.Event[proto.UpdateAvailable]{Type: pubsub.CreatedEvent, Payload: proto.UpdateAvailable{LatestVersion: "1.2.3"}})},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		for _, tc := range cases {
			env, err := json.Marshal(pubsub.Payload{Type: tc.typ, Payload: tc.payload})
			require.NoError(t, err)
			_, err = fmt.Fprintf(w, "data: %s\n\n", env)
			require.NoError(t, err)
		}
		// Exercise the "invalid format" and "unknown type" branches too;
		// neither should stop the stream or reach the events channel.
		_, _ = fmt.Fprint(w, "not-a-data-line\n\n")
		unknown, err := json.Marshal(pubsub.Payload{Type: "unknown_type", Payload: []byte("{}")})
		require.NoError(t, err)
		_, err = fmt.Fprintf(w, "data: %s\n\n", unknown)
		require.NoError(t, err)
		flusher.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := captureClient(t, srv)
	events, err := c.SubscribeEvents(ctx, "ws1")
	require.NoError(t, err)

	got := make([]any, 0, len(cases))
	for range cases {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-time.After(5 * time.Second):
			require.Fail(t, "timed out waiting for event")
		}
	}
	require.Len(t, got, len(cases))
}

func TestSendMessageAcceptsStatusAccepted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.SendMessage(context.Background(), "ws1", "sess1", "", "hello"))
}

func TestSendMessageAcceptsStatusOK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.SendMessage(context.Background(), "ws1", "sess1", "", "hello"))
}

func TestSendMessageDecodesErrorBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(proto.Error{Message: "session id is required"})
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	err := c.SendMessage(context.Background(), "ws1", "", "", "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 400")
	require.Contains(t, err.Error(), "session id is required")
}

func TestSendMessageFallsBackOnMalformedErrorBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	err := c.SendMessage(context.Background(), "ws1", "sess1", "", "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
	require.NotContains(t, err.Error(), "not json")
}

func TestSendMessageFallsBackOnEmptyErrorBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	err := c.SendMessage(context.Background(), "ws1", "sess1", "", "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func marshalSSEPayload(t *testing.T) []byte {
	t.Helper()

	eventPayload, err := json.Marshal(pubsub.Event[proto.AgentEvent]{
		Type: pubsub.CreatedEvent,
		Payload: proto.AgentEvent{
			Type: proto.AgentEventTypeResponse,
		},
	})
	require.NoError(t, err)

	payload, err := json.Marshal(pubsub.Payload{
		Type:    pubsub.PayloadTypeAgentEvent,
		Payload: eventPayload,
	})
	require.NoError(t, err)
	return payload
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestDecodeErrorMessageJSONBody(t *testing.T) {
	t.Parallel()

	msg := decodeErrorMessage(strings.NewReader(`{"message":"boom"}`))
	require.Equal(t, "boom", msg)
}

func TestDecodeErrorMessagePlainTextBody(t *testing.T) {
	t.Parallel()

	msg := decodeErrorMessage(strings.NewReader("not json"))
	require.Equal(t, "", msg)
}

func TestDecodeErrorMessageEmptyBody(t *testing.T) {
	t.Parallel()

	msg := decodeErrorMessage(strings.NewReader(""))
	require.Equal(t, "", msg)
}

func TestListWorkspacesSuccess(t *testing.T) {
	t.Parallel()

	want := []proto.Workspace{
		{ID: "ws1", Path: "/tmp/ws1", PermissionMode: "manual"},
		{ID: "ws2", Path: "/tmp/ws2", PermissionMode: "yolo"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/workspaces", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).ListWorkspaces(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestListWorkspacesNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).ListWorkspaces(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestListWorkspacesMalformedBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).ListWorkspaces(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode workspaces")
}

func TestGetSessionSuccess(t *testing.T) {
	t.Parallel()

	want := proto.Session{ID: "sess1", Title: "hello", MessageCount: 4}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/sessions/sess1", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).GetSession(context.Background(), "ws1", "sess1")
	require.NoError(t, err)
	require.Equal(t, want, *got)
}

func TestGetSessionNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).GetSession(context.Background(), "ws1", "sess1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 404")
}

func TestCreateSessionSuccess(t *testing.T) {
	t.Parallel()

	want := proto.Session{ID: "sess1", Title: "new session"}
	var gotBody proto.Session
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/sessions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).CreateSession(context.Background(), "ws1", "new session")
	require.NoError(t, err)
	require.Equal(t, want, *got)
	require.Equal(t, "new session", gotBody.Title)
}

func TestCreateSessionNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).CreateSession(context.Background(), "ws1", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 400")
}

func TestGrantPermissionSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resolved bool
	}{
		{name: "resolved by this call", resolved: true},
		{name: "already resolved by another caller", resolved: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/ws1/permissions/grant", r.URL.Path)
				var got proto.PermissionGrant
				require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
				require.Equal(t, proto.PermissionAllow, got.Action)
				require.NoError(t, json.NewEncoder(w).Encode(proto.PermissionGrantResponse{Resolved: tc.resolved}))
			}))
			defer srv.Close()

			resolved, err := captureClient(t, srv).GrantPermission(context.Background(), "ws1", proto.PermissionGrant{
				Permission: proto.PermissionRequest{ID: "p1", SessionID: "sess1"},
				Action:     proto.PermissionAllow,
			})
			require.NoError(t, err)
			require.Equal(t, tc.resolved, resolved)
		})
	}
}

func TestGrantPermissionNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := captureClient(t, srv).GrantPermission(context.Background(), "ws1", proto.PermissionGrant{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestSetPermissionModeSuccess(t *testing.T) {
	t.Parallel()

	var got proto.PermissionModeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/workspaces/ws1/permissions/mode", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, captureClient(t, srv).SetPermissionMode(context.Background(), "ws1", "yolo"))
	require.Equal(t, "yolo", got.Mode)
}

func TestSetPermissionModeNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := captureClient(t, srv).SetPermissionMode(context.Background(), "ws1", "yolo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}

func TestListMessagesToleratesEmptyBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got, err := captureClient(t, srv).ListMessages(context.Background(), "ws1", "sess1")
	require.NoError(t, err)
	require.Empty(t, got)
}

// protoMethodCase exercises one *Client method's happy path: the server
// asserts the request shape it received, the client asserts what it
// decoded back. call invokes the real production method.
type protoMethodCase struct {
	name       string
	wantMethod string
	wantPath   string
	body       []byte
	call       func(t *testing.T, c *Client)
}

func runProtoMethodCases(t *testing.T, cases []protoMethodCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tc.wantMethod, r.Method)
				require.Equal(t, tc.wantPath, r.URL.Path)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()

			tc.call(t, captureClient(t, srv))
		})
	}
}

func TestProtoMethodsSuccessPaths(t *testing.T) {
	t.Parallel()

	runProtoMethodCases(t, []protoMethodCase{
		{
			name:       "CreateWorkspace",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces",
			body:       mustJSON(t, proto.Workspace{ID: "ws1", Path: "/tmp/ws1"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.CreateWorkspace(context.Background(), proto.Workspace{Path: "/tmp/ws1"})
				require.NoError(t, err)
				require.Equal(t, "ws1", got.ID)
			},
		},
		{
			name:       "GetWorkspace",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1",
			body:       mustJSON(t, proto.Workspace{ID: "ws1", Path: "/tmp/ws1"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetWorkspace(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, "ws1", got.ID)
			},
		},
		{
			name:       "DeleteWorkspace",
			wantMethod: http.MethodDelete,
			wantPath:   "/v1/workspaces/ws1",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.DeleteWorkspace(context.Background(), "ws1"))
			},
		},
		{
			name:       "SetCurrentSession",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/current-session",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.SetCurrentSession(context.Background(), "ws1", "sess1"))
			},
		},
		{
			name:       "GetLSPDiagnostics",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/lsps/gopls/diagnostics",
			body: mustJSON(t, map[protocol.DocumentURI][]protocol.Diagnostic{
				protocol.DocumentURI("file:///a.go"): {},
			}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetLSPDiagnostics(context.Background(), "ws1", "gopls")
				require.NoError(t, err)
				require.Contains(t, got, protocol.DocumentURI("file:///a.go"))
			},
		},
		{
			name:       "GetLSPs",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/lsps",
			body: mustJSON(t, map[string]proto.LSPClientInfo{
				"gopls": {Name: "gopls", State: lsp.StateReady},
			}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetLSPs(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, lsp.StateReady, got["gopls"].State)
			},
		},
		{
			name:       "MCPGetStates",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/mcp/states",
			body: mustJSON(t, map[string]proto.MCPClientInfo{
				"srv1": {Name: "srv1", State: proto.MCPStateConnected},
			}),
			call: func(t *testing.T, c *Client) {
				got, err := c.MCPGetStates(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, proto.MCPStateConnected, got["srv1"].State)
			},
		},
		{
			name:       "MCPPendingAuth",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/mcp/pending-auth",
			body:       mustJSON(t, []proto.MCPPendingAuthServer{{Name: "srv1", URL: "http://auth"}}),
			call: func(t *testing.T, c *Client) {
				got, err := c.MCPPendingAuth(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, []proto.MCPPendingAuthServer{{Name: "srv1", URL: "http://auth"}}, got)
			},
		},
		{
			name:       "MCPAuthURL",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/mcp/auth-url",
			body:       mustJSON(t, proto.MCPAuthResponse{AuthURL: "http://auth"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.MCPAuthURL(context.Background(), "ws1", "srv1")
				require.NoError(t, err)
				require.Equal(t, "http://auth", got)
			},
		},
		{
			name:       "MCPAuthenticate",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/mcp/auth",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.MCPAuthenticate(context.Background(), "ws1", "srv1"))
			},
		},
		{
			name:       "MCPRefreshPrompts",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/mcp/refresh-prompts",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.MCPRefreshPrompts(context.Background(), "ws1", "srv1"))
			},
		},
		{
			name:       "MCPRefreshResources",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/mcp/refresh-resources",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.MCPRefreshResources(context.Background(), "ws1", "srv1"))
			},
		},
		{
			name:       "GetAgentSessionQueuedPrompts",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/prompts/queued",
			body:       mustJSON(t, 3),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetAgentSessionQueuedPrompts(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Equal(t, 3, got)
			},
		},
		{
			name:       "ClearAgentSessionQueuedPrompts",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/prompts/clear",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.ClearAgentSessionQueuedPrompts(context.Background(), "ws1", "sess1"))
			},
		},
		{
			name:       "GetAgentInfo",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/agent",
			body: mustJSON(t, proto.AgentInfo{
				IsBusy: true, IsReady: true,
				ModelCfg: config.SelectedModel{Provider: "mock", Model: "m"},
			}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetAgentInfo(context.Background(), "ws1")
				require.NoError(t, err)
				require.True(t, got.IsBusy)
				require.Equal(t, "mock", got.ModelCfg.Provider)
			},
		},
		{
			name:       "UpdateAgent",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/update",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.UpdateAgent(context.Background(), "ws1"))
			},
		},
		{
			name:       "RunShellCommand",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/shell",
			body:       mustJSON(t, proto.ShellCommandResponse{Output: "hi", ExitCode: 0}),
			call: func(t *testing.T, c *Client) {
				got, err := c.RunShellCommand(context.Background(), "ws1", "sess1", "echo hi", 80)
				require.NoError(t, err)
				require.Equal(t, "hi", got.Output)
			},
		},
		{
			name:       "GetAgentSessionInfo",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1",
			body:       mustJSON(t, proto.AgentSession{Session: proto.Session{ID: "sess1"}, IsBusy: true}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetAgentSessionInfo(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.True(t, got.IsBusy)
				require.Equal(t, "sess1", got.ID)
			},
		},
		{
			name:       "AgentSummarizeSession",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/summarize",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.AgentSummarizeSession(context.Background(), "ws1", "sess1"))
			},
		},
		{
			name:       "AgentEditSessionActive",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/active-agent",
			body:       mustJSON(t, proto.ActiveAgent{AgentID: "a1", AgentName: "Agent One"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.AgentEditSessionActive(context.Background(), "ws1", "sess1", proto.ActiveAgentEditRequest{Agent: "a1"})
				require.NoError(t, err)
				require.Equal(t, "a1", got.AgentID)
			},
		},
		{
			name:       "AgentGetSessionActive with session",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/active-agent",
			body:       mustJSON(t, proto.ActiveAgent{AgentID: "a1", AgentName: "Agent One"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.AgentGetSessionActive(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Equal(t, "a1", got.AgentID)
			},
		},
		{
			name:       "AgentGetSessionActive without session",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/agent/active-agent",
			body:       mustJSON(t, proto.ActiveAgent{AgentID: "default", AgentName: "Default Agent"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.AgentGetSessionActive(context.Background(), "ws1", "")
				require.NoError(t, err)
				require.Equal(t, "default", got.AgentID)
			},
		},
		{
			name:       "InitiateAgentProcessing",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/init",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.InitiateAgentProcessing(context.Background(), "ws1", true))
			},
		},
		{
			name:       "ListMessages",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1/messages",
			body:       mustJSON(t, []proto.Message{{ID: "m1", Role: proto.User, SessionID: "sess1"}}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ListMessages(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Len(t, got, 1)
				require.Equal(t, "m1", got[0].ID)
			},
		},
		{
			name:       "ListSessionHistoryFiles",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1/history",
			body:       mustJSON(t, []proto.File{{ID: "f1", SessionID: "sess1", Path: "a.go"}}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ListSessionHistoryFiles(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Equal(t, []proto.File{{ID: "f1", SessionID: "sess1", Path: "a.go"}}, got)
			},
		},
		{
			name:       "PreviewUndo",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1/undo",
			body:       mustJSON(t, proto.UndoPreview{CutMessageID: "m1", PoppedText: "hi", MessageCount: 2}),
			call: func(t *testing.T, c *Client) {
				got, err := c.PreviewUndo(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Equal(t, "m1", got.CutMessageID)
			},
		},
		{
			name:       "Undo",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1/undo",
			body:       mustJSON(t, proto.UndoResult{PoppedText: "hi", MessageCount: 1}),
			call: func(t *testing.T, c *Client) {
				got, err := c.Undo(context.Background(), "ws1", "sess1", "m1")
				require.NoError(t, err)
				require.Equal(t, "hi", got.PoppedText)
			},
		},
		{
			name:       "ListSessions",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/sessions",
			body:       mustJSON(t, []proto.Session{{ID: "s1"}, {ID: "s2"}}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ListSessions(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, []proto.Session{{ID: "s1"}, {ID: "s2"}}, got)
			},
		},
		{
			name:       "AnswerQuestionBatch",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/questions/answer",
			body:       mustJSON(t, proto.QuestionAnswerResponse{Resolved: true}),
			call: func(t *testing.T, c *Client) {
				resolved, err := c.AnswerQuestionBatch(context.Background(), "ws1", proto.QuestionAnswer{BatchRequestID: "b1"})
				require.NoError(t, err)
				require.True(t, resolved)
			},
		},
		{
			name:       "CancelQuestionBatch",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/questions/cancel",
			body:       mustJSON(t, proto.QuestionAnswerResponse{Resolved: false}),
			call: func(t *testing.T, c *Client) {
				resolved, err := c.CancelQuestionBatch(context.Background(), "ws1")
				require.NoError(t, err)
				require.False(t, resolved)
			},
		},
		{
			name:       "SetSessionUnattended",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/permissions/unattended",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.SetSessionUnattended(context.Background(), "ws1", "sess1", true))
			},
		},
		{
			name:       "GetPermissionMode",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/permissions/mode",
			body:       mustJSON(t, proto.PermissionModeRequest{Mode: "yolo"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetPermissionMode(context.Background(), "ws1")
				require.NoError(t, err)
				require.Equal(t, "yolo", got)
			},
		},
		{
			name:       "GetConfig",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/config",
			body:       []byte(`{}`),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetConfig(context.Background(), "ws1")
				require.NoError(t, err)
				require.NotNil(t, got)
			},
		},
		{
			name:       "SaveSession",
			wantMethod: http.MethodPut,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1",
			body:       mustJSON(t, proto.Session{ID: "sess1", Title: "updated"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.SaveSession(context.Background(), "ws1", proto.Session{ID: "sess1", Title: "updated"})
				require.NoError(t, err)
				require.Equal(t, "updated", got.Title)
			},
		},
		{
			name:       "DeleteSession",
			wantMethod: http.MethodDelete,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.DeleteSession(context.Background(), "ws1", "sess1"))
			},
		},
		{
			name:       "ListUserMessages",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1/messages/user",
			body:       mustJSON(t, []proto.Message{{ID: "m1", Role: proto.User, SessionID: "sess1"}}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ListUserMessages(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Len(t, got, 1)
			},
		},
		{
			name:       "ListAllUserMessages",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/messages/user",
			body:       mustJSON(t, []proto.Message{{ID: "m1", Role: proto.User, SessionID: "sess1"}}),
			call: func(t *testing.T, c *Client) {
				got, err := c.ListAllUserMessages(context.Background(), "ws1")
				require.NoError(t, err)
				require.Len(t, got, 1)
			},
		},
		{
			name:       "CancelAgentSession",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/cancel",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.CancelAgentSession(context.Background(), "ws1", "sess1"))
			},
		},
		{
			name:       "AbandonAgentBranch",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/abandon-branch",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.AbandonAgentBranch(context.Background(), "ws1", "sess1"))
			},
		},
		{
			name:       "GetAgentSessionQueuedPromptsList",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/agent/sessions/sess1/prompts/list",
			body:       mustJSON(t, []string{"p1", "p2"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.GetAgentSessionQueuedPromptsList(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Equal(t, []string{"p1", "p2"}, got)
			},
		},
		{
			name:       "FileTrackerRecordRead",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/filetracker/read",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.FileTrackerRecordRead(context.Background(), "ws1", "sess1", "a.go"))
			},
		},
		{
			name:       "FileTrackerLastReadTime",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/filetracker/lastread",
			body:       mustJSON(t, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)),
			call: func(t *testing.T, c *Client) {
				got, err := c.FileTrackerLastReadTime(context.Background(), "ws1", "sess1", "a.go")
				require.NoError(t, err)
				require.True(t, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).Equal(got))
			},
		},
		{
			name:       "FileTrackerListReadFiles",
			wantMethod: http.MethodGet,
			wantPath:   "/v1/workspaces/ws1/sessions/sess1/filetracker/files",
			body:       mustJSON(t, []string{"a.go", "b.go"}),
			call: func(t *testing.T, c *Client) {
				got, err := c.FileTrackerListReadFiles(context.Background(), "ws1", "sess1")
				require.NoError(t, err)
				require.Equal(t, []string{"a.go", "b.go"}, got)
			},
		},
		{
			name:       "LSPStart",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/lsps/start",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.LSPStart(context.Background(), "ws1", "/tmp/ws1"))
			},
		},
		{
			name:       "LSPStopAll",
			wantMethod: http.MethodPost,
			wantPath:   "/v1/workspaces/ws1/lsps/stop",
			call: func(t *testing.T, c *Client) {
				require.NoError(t, c.LSPStopAll(context.Background(), "ws1"))
			},
		},
	})
}

// protoErrorCase exercises one *Client method's non-2xx handling. Some
// methods only report the status code (the "plain" idiom); others also
// surface the server's decoded message (checkStatus, or an inline
// decodeErrorMessage call like MCPAuthenticate). checkMsg selects which
// behavior this case is pinning.
type protoErrorCase struct {
	name   string
	status int
	// message, when set, is served as a JSON proto.Error body.
	message string
	wantErr error
	// checkMsg asserts the client's error text surfaces message.
	checkMsg bool
	// noStatusInMsg marks a case whose error text does not include
	// the numeric status code at all (MCPAuthenticate's decoded-message
	// branch omits it entirely, unlike checkStatus and the plain
	// status-only idiom).
	noStatusInMsg bool
	call          func(c *Client) error
}

func runProtoErrorCases(t *testing.T, cases []protoErrorCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.message != "" {
					_ = json.NewEncoder(w).Encode(proto.Error{Message: tc.message})
				}
			}))
			defer srv.Close()

			err := tc.call(captureClient(t, srv))
			require.Error(t, err)
			if !tc.noStatusInMsg {
				require.Contains(t, err.Error(), fmt.Sprintf("%d", tc.status))
			}
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
			if tc.checkMsg {
				require.Contains(t, err.Error(), tc.message)
			}
		})
	}
}

func TestProtoMethodsErrorPaths(t *testing.T) {
	t.Parallel()

	runProtoErrorCases(t, []protoErrorCase{
		{
			name: "GetWorkspace not found", status: http.StatusNotFound, message: "workspace gone",
			wantErr: ErrNotFound, checkMsg: true,
			call: func(c *Client) error { _, err := c.GetWorkspace(context.Background(), "ws1"); return err },
		},
		{
			name: "ListSessions server error", status: http.StatusInternalServerError,
			call: func(c *Client) error { _, err := c.ListSessions(context.Background(), "ws1"); return err },
		},
		{
			name: "UpdateAgent server error", status: http.StatusInternalServerError,
			call: func(c *Client) error { return c.UpdateAgent(context.Background(), "ws1") },
		},
		{
			name: "Undo not found", status: http.StatusNotFound, message: "session gone",
			wantErr: ErrNotFound, checkMsg: true,
			call: func(c *Client) error { _, err := c.Undo(context.Background(), "ws1", "sess1", "m1"); return err },
		},
		{
			name: "AnswerQuestionBatch server error", status: http.StatusInternalServerError,
			call: func(c *Client) error {
				_, err := c.AnswerQuestionBatch(context.Background(), "ws1", proto.QuestionAnswer{})
				return err
			},
		},
		{
			name: "RunShellCommand server error", status: http.StatusInternalServerError,
			call: func(c *Client) error {
				_, err := c.RunShellCommand(context.Background(), "ws1", "sess1", "ls", 80)
				return err
			},
		},
		{
			name: "GetLSPs server error", status: http.StatusInternalServerError,
			call: func(c *Client) error { _, err := c.GetLSPs(context.Background(), "ws1"); return err },
		},
		{
			name: "DeleteSession server error", status: http.StatusInternalServerError,
			call: func(c *Client) error { return c.DeleteSession(context.Background(), "ws1", "sess1") },
		},
		{
			name: "MCPAuthenticate decodes server message", status: http.StatusBadRequest, message: "oauth flow failed",
			checkMsg: true, noStatusInMsg: true,
			call: func(c *Client) error { return c.MCPAuthenticate(context.Background(), "ws1", "srv1") },
		},
		{
			name: "MCPAuthenticate falls back to status", status: http.StatusInternalServerError,
			call: func(c *Client) error { return c.MCPAuthenticate(context.Background(), "ws1", "srv1") },
		},
		{
			name: "GetConfig server error", status: http.StatusInternalServerError,
			call: func(c *Client) error { _, err := c.GetConfig(context.Background(), "ws1"); return err },
		},
	})
}
