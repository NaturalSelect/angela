package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/client"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

// newRunTestClient stands up a fake server and returns a real
// *client.Client pointed at it, mirroring the technique already used by
// restart_stale_test.go and channels_flag_test.go's logout tests.
func newRunTestClient(t *testing.T, handler http.HandlerFunc) *client.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient("", "tcp", u.Host)
	require.NoError(t, err)
	return c
}

func TestWaitForAgent_ReturnsWhenAlreadyReady(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workspaces/ws1/agent", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: true}))
	})

	require.NoError(t, waitForAgent(t.Context(), c, "ws1"))
}

// TestWaitForAgent_PollsUntilReady covers the retry loop: the agent
// reports not-ready on the first call and ready on the second, so the
// call must survive at least one 200ms poll interval.
func TestWaitForAgent_PollsUntilReady(t *testing.T) {
	t.Parallel()

	var calls int32
	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		require.NoError(t, json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: n >= 2}))
	})

	start := time.Now()
	require.NoError(t, waitForAgent(t.Context(), c, "ws1"))
	require.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
	require.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(2))
}

// TestWaitForAgent_ContextCancelledPropagatesError covers a context
// that expires before the agent ever reports ready.
func TestWaitForAgent_ContextCancelledPropagatesError(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: false}))
	})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := waitForAgent(ctx, c, "ws1")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// overrideProvidersConfig builds a *config.Config with one provider
// ("openai") exposing two models, matching overrideProviders() in
// model_overrides_test.go but wrapped for a GetConfig response.
func overrideProvidersConfig() config.Config {
	return config.Config{
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"openai": {
				ID: "openai",
				Models: []config.ProviderModel{
					{Model: catwalk.Model{ID: "gpt-4o"}},
					{Model: catwalk.Model{ID: "gpt-4o-mini"}},
				},
			},
		}),
	}
}

// applyOverridesLog records which endpoints applyModelOverrides hit on
// the fake server, so tests can assert the call sequence (e.g. "no
// small model means no config write") and not just the final result.
type applyOverridesLog struct {
	preferredModelCalls int32
	updateAgentCalls    int32
}

func newApplyOverridesServer(t *testing.T, cfg config.Config) (*client.Client, *applyOverridesLog) {
	t.Helper()
	log := &applyOverridesLog{}
	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workspaces/ws1/config":
			require.NoError(t, json.NewEncoder(w).Encode(cfg))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workspaces/ws1/config/model":
			atomic.AddInt32(&log.preferredModelCalls, 1)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workspaces/ws1/agent/update":
			atomic.AddInt32(&log.updateAgentCalls, 1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return c, log
}

func TestApplyModelOverrides_BothModelsApplied(t *testing.T) {
	t.Parallel()

	c, log := newApplyOverridesServer(t, overrideProvidersConfig())
	ws := &proto.Workspace{ID: "ws1"}

	got, err := applyModelOverrides(t.Context(), c, ws, "gpt-4o", "gpt-4o-mini")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "openai", got.Provider)
	require.Equal(t, "gpt-4o", got.Model)
	require.EqualValues(t, 1, atomic.LoadInt32(&log.preferredModelCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&log.updateAgentCalls))
}

// TestApplyModelOverrides_OnlyLargeModelSkipsConfigWrite covers the
// comment's contract: the large model belongs to the session and must
// not touch workspace config at all when no small model was requested.
func TestApplyModelOverrides_OnlyLargeModelSkipsConfigWrite(t *testing.T) {
	t.Parallel()

	c, log := newApplyOverridesServer(t, overrideProvidersConfig())
	ws := &proto.Workspace{ID: "ws1"}

	got, err := applyModelOverrides(t.Context(), c, ws, "gpt-4o", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "gpt-4o", got.Model)
	require.EqualValues(t, 0, atomic.LoadInt32(&log.preferredModelCalls))
	require.EqualValues(t, 0, atomic.LoadInt32(&log.updateAgentCalls))
}

// TestApplyModelOverrides_InvalidModelRejectsBeforeAnyWrite is the
// regression covered at the resolveModelOverrides level in
// model_overrides_test.go (TestABadMainModelResolvesNothing), exercised
// here through the client-facing wrapper: an invalid flag must fail
// before any config mutation reaches the server.
func TestApplyModelOverrides_InvalidModelRejectsBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	c, log := newApplyOverridesServer(t, overrideProvidersConfig())
	ws := &proto.Workspace{ID: "ws1"}

	_, err := applyModelOverrides(t.Context(), c, ws, "no-such-model", "gpt-4o-mini")
	require.Error(t, err)
	require.EqualValues(t, 0, atomic.LoadInt32(&log.preferredModelCalls))
	require.EqualValues(t, 0, atomic.LoadInt32(&log.updateAgentCalls))
}

func TestApplyModelOverrides_GetConfigErrorPropagates(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ws := &proto.Workspace{ID: "ws1"}

	_, err := applyModelOverrides(t.Context(), c, ws, "gpt-4o", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get config")
}

func TestApplyModelOverrides_UpdatePreferredModelErrorPropagates(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/workspaces/ws1/config":
			require.NoError(t, json.NewEncoder(w).Encode(overrideProvidersConfig()))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	ws := &proto.Workspace{ID: "ws1"}

	_, err := applyModelOverrides(t.Context(), c, ws, "", "gpt-4o-mini")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to set small model")
}

// TestApplyModelOverrides_OnlySmallModelReturnsNilLarge covers the
// mirror image of OnlyLargeModelSkipsConfigWrite: with no --model flag,
// the large-model return value must be nil (nothing for the caller to
// apply to the session) even though the small model was successfully
// written to workspace config.
func TestApplyModelOverrides_OnlySmallModelReturnsNilLarge(t *testing.T) {
	t.Parallel()

	c, log := newApplyOverridesServer(t, overrideProvidersConfig())
	ws := &proto.Workspace{ID: "ws1"}

	got, err := applyModelOverrides(t.Context(), c, ws, "", "gpt-4o-mini")
	require.NoError(t, err)
	require.Nil(t, got)
	require.EqualValues(t, 1, atomic.LoadInt32(&log.preferredModelCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&log.updateAgentCalls))
}

// TestApplyModelOverrides_UpdateAgentErrorPropagates covers the second
// failure point in the small-model path: the preferred-model write
// succeeds but reloading the agent with it fails.
func TestApplyModelOverrides_UpdateAgentErrorPropagates(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/workspaces/ws1/config":
			require.NoError(t, json.NewEncoder(w).Encode(overrideProvidersConfig()))
		case "/v1/workspaces/ws1/config/model":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	ws := &proto.Workspace{ID: "ws1"}

	_, err := applyModelOverrides(t.Context(), c, ws, "", "gpt-4o-mini")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to update agent")
}

// newResolveSessionServer builds a fake server backing GetSession,
// ListSessions, and CreateSession for a single workspace ID.
func newResolveSessionServer(t *testing.T, sessions map[string]proto.Session, listErr bool, createdTitle *string) *client.Client {
	t.Helper()
	return newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workspaces/ws1/sessions":
			if listErr {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			list := make([]proto.Session, 0, len(sessions))
			for _, s := range sessions {
				list = append(list, s)
			}
			// Sort deterministically (ascending by UpdatedAt) so tests don't
			// depend on Go's randomized map iteration order.
			sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt < list[j].UpdatedAt })
			require.NoError(t, json.NewEncoder(w).Encode(list))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workspaces/ws1/sessions":
			var req proto.Session
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			if createdTitle != nil {
				*createdTitle = req.Title
			}
			require.NoError(t, json.NewEncoder(w).Encode(proto.Session{ID: "created-session", Title: req.Title}))
		case r.Method == http.MethodGet && len(r.URL.Path) > len("/v1/workspaces/ws1/sessions/"):
			id := r.URL.Path[len("/v1/workspaces/ws1/sessions/"):]
			if s, ok := sessions[id]; ok {
				require.NoError(t, json.NewEncoder(w).Encode(s))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestResolveSession_ContinuesExistingSession(t *testing.T) {
	t.Parallel()

	want := proto.Session{ID: "s1", Title: "Existing"}
	c := newResolveSessionServer(t, map[string]proto.Session{"s1": want}, false, nil)

	got, err := resolveSession(t.Context(), c, "ws1", "s1", false)
	require.NoError(t, err)
	require.Equal(t, want.ID, got.ID)
}

func TestResolveSession_ContinueMissingSessionErrors(t *testing.T) {
	t.Parallel()

	c := newResolveSessionServer(t, map[string]proto.Session{}, false, nil)

	_, err := resolveSession(t.Context(), c, "ws1", "missing", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found: missing")
}

func TestResolveSession_ContinueChildSessionErrors(t *testing.T) {
	t.Parallel()

	child := proto.Session{ID: "s1", ParentSessionID: "parent-1"}
	c := newResolveSessionServer(t, map[string]proto.Session{"s1": child}, false, nil)

	_, err := resolveSession(t.Context(), c, "ws1", "s1", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot continue a child session")
}

// TestResolveSession_UseLastPicksMostRecentTopLevelSession covers both
// the ordering and the child-session exclusion: the newest session must
// win, but only among sessions with no parent.
func TestResolveSession_UseLastPicksMostRecentTopLevelSession(t *testing.T) {
	t.Parallel()

	sessions := map[string]proto.Session{
		"old":    {ID: "old", UpdatedAt: 100},
		"newest": {ID: "newest", UpdatedAt: 300},
		"child":  {ID: "child", UpdatedAt: 500, ParentSessionID: "newest"},
	}
	c := newResolveSessionServer(t, sessions, false, nil)

	got, err := resolveSession(t.Context(), c, "ws1", "", true)
	require.NoError(t, err)
	require.Equal(t, "newest", got.ID, "the most recently updated top-level session must win, ignoring a newer child")
}

func TestResolveSession_UseLastNoSessionsErrors(t *testing.T) {
	t.Parallel()

	c := newResolveSessionServer(t, map[string]proto.Session{}, false, nil)

	_, err := resolveSession(t.Context(), c, "ws1", "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no sessions found to continue")
}

func TestResolveSession_UseLastListErrorReportsNoSessions(t *testing.T) {
	t.Parallel()

	c := newResolveSessionServer(t, nil, true, nil)

	_, err := resolveSession(t.Context(), c, "ws1", "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no sessions found to continue")
}

func TestResolveSession_DefaultCreatesNewSession(t *testing.T) {
	t.Parallel()

	var createdTitle string
	c := newResolveSessionServer(t, map[string]proto.Session{}, false, &createdTitle)

	got, err := resolveSession(t.Context(), c, "ws1", "", false)
	require.NoError(t, err)
	require.Equal(t, "created-session", got.ID)
	require.Equal(t, "non-interactive", createdTitle)
}

func TestResolveSessionByID_DirectMatch(t *testing.T) {
	t.Parallel()

	want := proto.Session{ID: "session-uuid-1", Title: "Direct"}
	c := newResolveSessionServer(t, map[string]proto.Session{want.ID: want}, false, nil)

	got, err := resolveSessionByID(t.Context(), c, "ws1", want.ID)
	require.NoError(t, err)
	require.Equal(t, want.ID, got.ID)
}

func TestResolveSessionByID_HashPrefixMatch(t *testing.T) {
	t.Parallel()

	target := proto.Session{ID: "session-uuid-3", Title: "Prefix match"}
	c := newResolveSessionServer(t, map[string]proto.Session{target.ID: target}, false, nil)

	hash := session.HashID(target.ID)
	got, err := resolveSessionByID(t.Context(), c, "ws1", hash[:6])
	require.NoError(t, err)
	require.Equal(t, target.ID, got.ID)
}

func TestResolveSessionByID_AmbiguousMatch(t *testing.T) {
	t.Parallel()

	sessions := map[string]proto.Session{
		"session-uuid-4": {ID: "session-uuid-4", Title: "First"},
		"session-uuid-5": {ID: "session-uuid-5", Title: "Second"},
	}
	c := newResolveSessionServer(t, sessions, false, nil)

	// Every hash has "" as a prefix, so an empty query matches all
	// sessions and forces the ambiguous branch without needing a real
	// hash collision.
	_, err := resolveSessionByID(t.Context(), c, "ws1", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is ambiguous")
}

func TestResolveSessionByID_NotFound(t *testing.T) {
	t.Parallel()

	c := newResolveSessionServer(t, map[string]proto.Session{}, false, nil)

	_, err := resolveSessionByID(t.Context(), c, "ws1", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), `session "missing" not found`)
}

func TestResolveSessionByID_ListErrorPropagates(t *testing.T) {
	t.Parallel()

	c := newResolveSessionServer(t, nil, true, nil)

	_, err := resolveSessionByID(t.Context(), c, "ws1", "missing")
	require.Error(t, err)
}

// newRunNonInteractiveServer wires the full set of endpoints
// runNonInteractive drives during a single non-interactive turn:
// agent readiness, agent update, session creation, marking the
// session unattended, sending the message, and streaming back a
// matching RunComplete over SSE. It captures the RunID minted by
// SendMessage from the request body and echoes it back on the
// RunComplete event, mirroring how a real server correlates them.
func newRunNonInteractiveServer(t *testing.T, complete proto.RunComplete) *client.Client {
	t.Helper()
	runIDCh := make(chan string, 1)

	return newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/agent"):
			require.NoError(t, json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: true}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/agent/update"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sessions"):
			require.NoError(t, json.NewEncoder(w).Encode(proto.Session{ID: "sess-1"}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/permissions/unattended"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/agent"):
			var msg proto.AgentMessage
			require.NoError(t, json.NewDecoder(r.Body).Decode(&msg))
			runIDCh <- msg.RunID
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Flush headers immediately so SubscribeEvents's GET returns
			// before SendMessage is sent: otherwise the client never
			// issues the POST that supplies runIDCh, deadlocking both
			// sides of this handler.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			complete.RunID = <-runIDCh
			inner, err := json.Marshal(pubsub.Event[proto.RunComplete]{Type: pubsub.CreatedEvent, Payload: complete})
			require.NoError(t, err)
			env, err := json.Marshal(pubsub.Payload{Type: pubsub.PayloadTypeRunComplete, Payload: inner})
			require.NoError(t, err)
			_, err = fmt.Fprintf(w, "data: %s\n\n", env)
			require.NoError(t, err)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
}

// TestRunNonInteractive_HappyPath drives the client/server orchestration
// end to end against a fake server: readiness polling, the agent
// model update, default session creation, marking the session
// unattended, sending the prompt, and exiting cleanly on the
// RunComplete event correlated by RunID.
func TestRunNonInteractive_HappyPath(t *testing.T) {
	c := newRunNonInteractiveServer(t, proto.RunComplete{SessionID: "sess-1", MessageID: "m1", Text: "final answer"})
	ws := &proto.Workspace{ID: "ws1", Config: &config.Config{Options: &config.Options{}}}

	err := runNonInteractive(t.Context(), c, ws, "prompt", "", "", true, "", false)
	require.NoError(t, err)
}

// TestRunNonInteractive_RunCompleteErrorPropagates covers a
// RunComplete event carrying a server-side failure: the run must
// surface it as an error instead of exiting cleanly.
func TestRunNonInteractive_RunCompleteErrorPropagates(t *testing.T) {
	c := newRunNonInteractiveServer(t, proto.RunComplete{SessionID: "sess-1", MessageID: "m1", Error: "model exploded"})
	ws := &proto.Workspace{ID: "ws1", Config: &config.Config{Options: &config.Options{}}}

	err := runNonInteractive(t.Context(), c, ws, "prompt", "", "", true, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "model exploded")
}

// TestRunNonInteractive_AgentNotReadyPropagatesError covers the
// waitForAgent failure branch: a context that expires before the
// agent ever reports ready must fail fast with a wrapped error
// instead of hanging on the 30s internal timeout.
func TestRunNonInteractive_AgentNotReadyPropagatesError(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: false}))
	})
	ws := &proto.Workspace{ID: "ws1", Config: &config.Config{Options: &config.Options{}}}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	err := runNonInteractive(ctx, c, ws, "prompt", "", "", true, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent not ready")
}

// TestRunNonInteractive_InvalidModelOverridePropagatesError covers
// the model-override error branch: an unmatched --model flag must
// fail before ever polling agent readiness or sending a message.
func TestRunNonInteractive_InvalidModelOverridePropagatesError(t *testing.T) {
	t.Parallel()

	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/config"):
			require.NoError(t, json.NewEncoder(w).Encode(overrideProvidersConfig()))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	ws := &proto.Workspace{ID: "ws1", Config: &config.Config{Options: &config.Options{}}}

	err := runNonInteractive(t.Context(), c, ws, "prompt", "no-such-model", "", true, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to override models")
}

// TestRunNonInteractive_SetSessionUnattendedErrorPropagates covers
// the unattended-marking failure branch, which must abort the run
// before a message is ever sent (no server-side turn should start
// for a session nothing here can answer permission prompts for).
func TestRunNonInteractive_SetSessionUnattendedErrorPropagates(t *testing.T) {
	t.Parallel()

	var sendMessageCalled atomic.Bool
	c := newRunTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/agent"):
			require.NoError(t, json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: true}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/agent/update"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sessions"):
			require.NoError(t, json.NewEncoder(w).Encode(proto.Session{ID: "sess-1"}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/permissions/unattended"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/agent"):
			sendMessageCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	ws := &proto.Workspace{ID: "ws1", Config: &config.Config{Options: &config.Options{}}}

	err := runNonInteractive(t.Context(), c, ws, "prompt", "", "", true, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to mark the session unattended")
	require.False(t, sendMessageCalled.Load(), "must not send the prompt once marking the session unattended failed")
}
