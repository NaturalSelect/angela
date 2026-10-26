package acp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/session"
)

type harness struct {
	ws     *fakeWorkspace
	client *fakeClient
	agent  *Agent
	opened []ClientCaps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{ws: newFakeWorkspace(), client: &fakeClient{}}
	h.agent = NewAgent(t.Context(), h.client, func(_ context.Context, _ string, caps ClientCaps) (Workspace, error) {
		h.opened = append(h.opened, caps)
		return h.ws, nil
	})
	t.Cleanup(h.agent.Close)
	return h
}

func (h *harness) newSession(t *testing.T) string {
	t.Helper()
	resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
	require.NoError(t, err)
	return resp.SessionID
}

func (h *harness) publishMessage(m message.Message) {
	h.ws.publish(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: m})
}

// finishRun reports the end of a run the way the coordinator does: it flags
// the context marker and publishes the terminal event.
func (h *harness) finishRun(ctx context.Context, sessionID string, rc notify.RunComplete) {
	rc.SessionID = sessionID
	if rc.RunID == "" {
		rc.RunID = agent.RunIDFromContext(ctx)
	}
	agent.MarkRunCompletePublished(ctx)
	h.ws.publish(pubsub.Event[notify.RunComplete]{Type: pubsub.UpdatedEvent, Payload: rc})
}

type runFunc = func(ctx context.Context, sessionID, prompt string, atts []message.Attachment) error

func (h *harness) completeWith(rc notify.RunComplete) runFunc {
	return func(ctx context.Context, sessionID, _ string, _ []message.Attachment) error {
		h.finishRun(ctx, sessionID, rc)
		return nil
	}
}

func (h *harness) answerWith(text string) runFunc {
	return func(ctx context.Context, sessionID, _ string, _ []message.Attachment) error {
		m := assistantMsg("a1", message.TextContent{Text: text}, message.Finish{Reason: message.FinishReasonEndTurn})
		m.SessionID = sessionID
		h.ws.setMessages(sessionID, m)
		h.publishMessage(m)
		h.finishRun(ctx, sessionID, notify.RunComplete{MessageID: m.ID})
		return nil
	}
}

type promptResult struct {
	resp protocol.PromptResponse
	err  error
}

func (h *harness) promptAsync(t *testing.T, sessionID string) <-chan promptResult {
	t.Helper()
	ch := make(chan promptResult, 1)
	go func() {
		resp, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "go"))
		ch <- promptResult{resp: resp, err: err}
	}()
	return ch
}

func await(t *testing.T, ch <-chan promptResult) promptResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not return")
		return promptResult{}
	}
}

func textPrompt(sessionID, text string) protocol.PromptRequest {
	return protocol.PromptRequest{SessionID: sessionID, Prompt: []protocol.ContentBlock{protocol.TextBlock{Text: text}}}
}

func requireCode(t *testing.T, err error, code protocol.ErrorCode) {
	t.Helper()
	var perr protocol.Error
	require.ErrorAs(t, err, &perr)
	require.Equal(t, code, perr.Code, perr.Message)
}

func TestInitialize(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	resp, err := h.agent.Initialize(t.Context(), protocol.InitializeRequest{ProtocolVersion: 1, ElicitationForm: true})
	require.NoError(t, err)
	require.Equal(t, 1, resp.ProtocolVersion)
	require.Equal(t, "angela", resp.AgentName)
	require.True(t, resp.LoadSession)
	require.True(t, resp.ResumeSession)
	require.True(t, resp.ListSessions)
	require.True(t, resp.DeleteSessions)
	require.True(t, resp.CloseSession)
	require.True(t, resp.PromptImage)
	require.True(t, resp.PromptEmbeddedContext)

	h.newSession(t)
	require.Equal(t, []ClientCaps{{Elicitation: true}}, h.opened)
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	t.Run("builds the workspace once and registers the session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		first, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		require.NotEmpty(t, first.SessionID)
		require.NotEmpty(t, first.ConfigOptions)

		second, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd + "/"})
		require.NoError(t, err)
		require.NotEqual(t, first.SessionID, second.SessionID)

		require.Len(t, h.opened, 1)
		require.Equal(t, []string{first.SessionID, second.SessionID}, h.agent.reg.ids())
	})

	t.Run("relative cwd", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: "proj"})
		requireCode(t, err, protocol.CodeInvalidParams)
		require.Empty(t, h.opened)
	})

	t.Run("a second project is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.newSession(t)
		_, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: filepath.Join(filepath.Dir(testCwd), "other")})
		requireCode(t, err, protocol.CodeInvalidParams)
	})

	t.Run("no provider configured", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.readyErr = errors.New("no model")
		_, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		requireCode(t, err, protocol.CodeInternal)
		require.Empty(t, h.agent.reg.ids())
	})

	t.Run("a provider set up later is picked up without a restart", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.readyErr = errors.New("no model")
		_, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		requireCode(t, err, protocol.CodeInternal)

		ws, cwd := h.agent.current()
		require.Nil(t, ws, "an unready workspace must not be kept")
		require.Empty(t, cwd)
		h.ws.snapshot(func() { require.Equal(t, 1, h.ws.shutdowns, "the unready workspace is released") })

		h.ws.mu.Lock()
		h.ws.readyErr = nil
		h.ws.mu.Unlock()
		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		require.NotEmpty(t, resp.SessionID)
		require.Len(t, h.opened, 2, "the second request opens the workspace again")
	})

	t.Run("load and resume also refuse an unready workspace", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.readyErr = errors.New("no model")
		_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "old", Cwd: testCwd})
		requireCode(t, err, protocol.CodeInternal)
		_, err = h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "old", Cwd: testCwd})
		requireCode(t, err, protocol.CodeInternal)
		ws, _ := h.agent.current()
		require.Nil(t, ws)
	})

	t.Run("workspace cannot be opened", func(t *testing.T) {
		t.Parallel()
		a := NewAgent(t.Context(), &fakeClient{}, func(context.Context, string, ClientCaps) (Workspace, error) {
			return nil, errors.New("database locked")
		})
		t.Cleanup(a.Close)
		_, err := a.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		requireCode(t, err, protocol.CodeInternal)
	})
}

func TestLoadSession(t *testing.T) {
	t.Parallel()

	t.Run("replays history, then continues from where it stopped", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		user := message.Message{ID: "u1", SessionID: "old", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "fix it"}}}
		reply := assistantMsg("a1", message.TextContent{Text: "done"}, message.Finish{Reason: message.FinishReasonEndTurn})
		reply.SessionID = "old"
		h.ws.addSession(session.Session{ID: "old", Title: "Fix bug"})
		h.ws.setMessages("old", user, reply)

		resp, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "old", Cwd: testCwd})
		require.NoError(t, err)
		require.NotEmpty(t, resp.ConfigOptions)

		replay := []protocol.SessionUpdate{
			userChunk("u1", protocol.TextBlock{Text: "fix it"}),
			answer("a1", "done"),
			protocol.SessionInfoUpdate{Title: new("Fix bug")},
			protocol.Plan{Entries: []protocol.PlanEntry{}},
			protocol.AvailableCommandsUpdate{Commands: builtinAdverts},
		}
		require.Equal(t, replay, h.client.updates("old"))

		extended := assistantMsg("a1", message.TextContent{Text: "done!"}, message.Finish{Reason: message.FinishReasonEndTurn})
		extended.SessionID = "old"
		h.publishMessage(extended)
		eventually(t, func() bool { return len(h.client.updates("old")) == len(replay)+1 })
		require.Equal(t, answer("a1", "!"), h.client.updates("old")[len(replay)])
	})

	t.Run("unknown session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "nope", Cwd: testCwd})
		requireCode(t, err, protocol.CodeNotFound)
	})

	t.Run("sub-agent session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "child", ParentSessionID: "root"})
		_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "child", Cwd: testCwd})
		requireCode(t, err, protocol.CodeInvalidParams)
	})

	t.Run("running session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "busy"})
		h.ws.busy["busy"] = true
		_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "busy", Cwd: testCwd})
		requireCode(t, err, protocol.CodeInvalidParams)
	})
}

func TestResumeSession(t *testing.T) {
	t.Parallel()

	t.Run("sends the session state but not the history", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		user := message.Message{ID: "u1", SessionID: "old", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "fix it"}}}
		reply := assistantMsg("a1", message.TextContent{Text: "done"}, message.Finish{Reason: message.FinishReasonEndTurn})
		reply.SessionID = "old"
		h.ws.addSession(session.Session{ID: "old", Title: "Fix bug"})
		h.ws.setMessages("old", user, reply)

		resp, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "old", Cwd: testCwd})
		require.NoError(t, err)
		require.NotEmpty(t, resp.ConfigOptions)

		require.Equal(t, []protocol.SessionUpdate{
			protocol.SessionInfoUpdate{Title: new("Fix bug")},
			protocol.Plan{Entries: []protocol.PlanEntry{}},
			protocol.AvailableCommandsUpdate{Commands: builtinAdverts},
		}, h.client.updates("old"))
	})

	t.Run("later changes to an old message are sent as new text only", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		reply := assistantMsg("a1", message.TextContent{Text: "done"}, message.Finish{Reason: message.FinishReasonEndTurn})
		reply.SessionID = "old"
		h.ws.addSession(session.Session{ID: "old", Title: "Fix bug"})
		h.ws.setMessages("old", reply)

		_, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "old", Cwd: testCwd})
		require.NoError(t, err)
		before := len(h.client.updates("old"))

		// NOTE: The pump must treat the resumed message as already sent. If
		// it did not, it would resend "done" in full and the editor would
		// show the answer twice.
		extended := assistantMsg("a1", message.TextContent{Text: "done!"}, message.Finish{Reason: message.FinishReasonEndTurn})
		extended.SessionID = "old"
		h.publishMessage(extended)
		eventually(t, func() bool { return len(h.client.updates("old")) == before+1 })
		require.Equal(t, answer("a1", "!"), h.client.updates("old")[before])
	})

	t.Run("the session accepts a prompt afterwards", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "old", Title: "Fix bug"})
		h.ws.run = h.completeWith(notify.RunComplete{})

		_, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "old", Cwd: testCwd})
		require.NoError(t, err)

		got, err := h.agent.Prompt(t.Context(), textPrompt("old", "go on"))
		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, got.StopReason)
	})

	t.Run("unknown session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "nope", Cwd: testCwd})
		requireCode(t, err, protocol.CodeNotFound)
	})

	t.Run("sub-agent session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "child", ParentSessionID: "root"})
		_, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "child", Cwd: testCwd})
		requireCode(t, err, protocol.CodeInvalidParams)
	})

	t.Run("running session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "busy"})
		h.ws.busy["busy"] = true
		_, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "busy", Cwd: testCwd})
		requireCode(t, err, protocol.CodeInvalidParams)
	})
}

func TestListSessions(t *testing.T) {
	t.Parallel()

	t.Run("nothing before a project is open", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		resp, err := h.agent.ListSessions(t.Context(), protocol.ListSessionsRequest{})
		require.NoError(t, err)
		require.Empty(t, resp.Sessions)
		require.Empty(t, h.opened)
	})

	t.Run("opens the project named by the request", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "x", Title: "Existing", UpdatedAt: 1700000000})
		resp, err := h.agent.ListSessions(t.Context(), protocol.ListSessionsRequest{Cwd: testCwd})
		require.NoError(t, err)
		require.Equal(t, []protocol.SessionInfo{{
			SessionID: "x", Cwd: testCwd, Title: "Existing", UpdatedAt: time.Unix(1700000000, 0),
		}}, resp.Sessions)
	})

	t.Run("pages chain through the cursor", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		const total = 120
		for i := range total {
			h.ws.addSession(session.Session{ID: fmt.Sprintf("s-%03d", i), UpdatedAt: int64(i)})
		}

		seen := make(map[string]bool)
		cursor := ""
		pages := 0
		for {
			resp, err := h.agent.ListSessions(t.Context(), protocol.ListSessionsRequest{Cwd: testCwd, Cursor: cursor})
			require.NoError(t, err)
			pages++
			for _, s := range resp.Sessions {
				require.False(t, seen[s.SessionID], "repeated %s", s.SessionID)
				seen[s.SessionID] = true
			}
			if resp.NextCursor == "" {
				break
			}
			cursor = resp.NextCursor
		}
		require.Len(t, seen, total)
		require.Equal(t, 3, pages)
	})

	t.Run("bad cursor", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.agent.ListSessions(t.Context(), protocol.ListSessionsRequest{Cwd: testCwd, Cursor: "!!!"})
		requireCode(t, err, protocol.CodeInvalidParams)
	})
}

func TestDeleteSession(t *testing.T) {
	t.Parallel()

	t.Run("removes the session and forgets it", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		require.NoError(t, h.agent.DeleteSession(t.Context(), protocol.DeleteSessionRequest{SessionID: sid}))
		require.Equal(t, []string{sid}, h.ws.deleted)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		requireCode(t, err, protocol.CodeNotFound)
	})

	t.Run("a session that is already gone is not an error", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.newSession(t)
		require.NoError(t, h.agent.DeleteSession(t.Context(), protocol.DeleteSessionRequest{SessionID: "ghost"}))
		require.Empty(t, h.ws.deleted)
	})

	t.Run("running session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		h.ws.busy[sid] = true
		err := h.agent.DeleteSession(t.Context(), protocol.DeleteSessionRequest{SessionID: sid})
		requireCode(t, err, protocol.CodeInvalidParams)
		require.Empty(t, h.ws.deleted)
	})

	t.Run("before a project is open", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		err := h.agent.DeleteSession(t.Context(), protocol.DeleteSessionRequest{SessionID: "x"})
		requireCode(t, err, protocol.CodeInvalidParams)
	})
}

func TestCloseSession(t *testing.T) {
	t.Parallel()

	t.Run("ends the running turn at once and forgets the session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		started := make(chan struct{})
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		h.ws.run = func(context.Context, string, string, []message.Attachment) error {
			close(started)
			<-release
			return nil
		}

		result := h.promptAsync(t, sid)
		<-started
		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: sid}))

		// NOTE: The default grace period is far longer than await waits, so
		// this passes only if close ends the turn itself.
		got := await(t, result)
		require.NoError(t, got.err)
		require.Equal(t, protocol.StopReasonCancelled, got.resp.StopReason)

		h.ws.snapshot(func() {
			require.Equal(t, []string{sid}, h.ws.cancelled)
			require.Equal(t, []string{sid}, h.ws.queueCleared)
		})
		require.Empty(t, h.agent.reg.ids())

		_, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		requireCode(t, err, protocol.CodeNotFound)
	})

	t.Run("keeps the stored session so it can be resumed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: sid}))
		h.ws.snapshot(func() { require.Empty(t, h.ws.deleted) })

		_, err := h.ws.GetSession(t.Context(), sid)
		require.NoError(t, err)
		_, err = h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: sid, Cwd: testCwd})
		require.NoError(t, err)
		require.Equal(t, []string{sid}, h.agent.reg.ids())
	})

	t.Run("leaves other sessions alone", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		first := h.newSession(t)
		second := h.newSession(t)

		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: first}))
		require.Equal(t, []string{second}, h.agent.reg.ids())
		h.ws.snapshot(func() { require.Equal(t, []string{first}, h.ws.cancelled) })
	})

	t.Run("a session that is already closed is not an error", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: sid}))
		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: sid}))
		h.ws.snapshot(func() { require.Equal(t, []string{sid}, h.ws.cancelled) })
	})

	t.Run("unknown session is not an error", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: "ghost"}))
		require.Equal(t, []string{sid}, h.agent.reg.ids())
		h.ws.snapshot(func() {
			require.Empty(t, h.ws.cancelled)
			require.Empty(t, h.ws.queueCleared)
		})
	})

	t.Run("before a project is open", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		require.NoError(t, h.agent.CloseSession(t.Context(), protocol.CloseSessionRequest{SessionID: "x"}))
	})
}

func TestSetConfigOption(t *testing.T) {
	t.Parallel()

	newConfigured := func(t *testing.T) *harness {
		t.Helper()
		h := newHarness(t)
		h.ws.cfg = twoProviders()
		h.ws.active = activeOn("openai", "gpt-5", providerModel("gpt-5", "GPT-5"))
		return h
	}

	t.Run("unknown session", func(t *testing.T) {
		t.Parallel()
		h := newConfigured(t)
		h.newSession(t)
		_, err := h.agent.SetConfigOption(t.Context(), protocol.SetConfigOptionRequest{SessionID: "nope", ConfigID: optMode, Value: "yolo"})
		requireCode(t, err, protocol.CodeNotFound)
	})

	t.Run("mode change reaches the other sessions", func(t *testing.T) {
		t.Parallel()
		h := newConfigured(t)
		first, second := h.newSession(t), h.newSession(t)

		resp, err := h.agent.SetConfigOption(t.Context(), protocol.SetConfigOptionRequest{
			SessionID: first, ConfigID: optMode, Value: permission.ModeYolo.String(),
		})
		require.NoError(t, err)
		require.Equal(t, permission.ModeYolo, h.ws.PermissionMode())
		require.Equal(t, permission.ModeYolo.String(), requireOption(t, resp.ConfigOptions, optMode).CurrentValue)

		require.Empty(t, h.client.updates(first), "the requester learns from the response")
		updates := h.client.updates(second)
		require.Len(t, updates, 1)
		pushed, ok := updates[0].(protocol.ConfigOptionUpdate)
		require.True(t, ok)
		require.Equal(t, permission.ModeYolo.String(), requireOption(t, pushed.ConfigOptions, optMode).CurrentValue)
	})

	t.Run("invalid mode leaves the mode alone", func(t *testing.T) {
		t.Parallel()
		h := newConfigured(t)
		sid := h.newSession(t)
		_, err := h.agent.SetConfigOption(t.Context(), protocol.SetConfigOptionRequest{SessionID: sid, ConfigID: optMode, Value: "reckless"})
		requireCode(t, err, protocol.CodeInvalidParams)
		require.Equal(t, permission.ModeManual, h.ws.PermissionMode())
	})

	t.Run("model change edits the active agent", func(t *testing.T) {
		t.Parallel()
		h := newConfigured(t)
		sid := h.newSession(t)
		_, err := h.agent.SetConfigOption(t.Context(), protocol.SetConfigOptionRequest{
			SessionID: sid, ConfigID: optModel, Value: modelValue("anthropic", "sonnet"),
		})
		require.NoError(t, err)
		require.Equal(t, []config.ActiveAgentEdit{
			{Model: &config.SelectedModel{Provider: "anthropic", Model: "sonnet"}},
		}, h.ws.edits)
	})

	t.Run("an unavailable choice is the caller's mistake", func(t *testing.T) {
		t.Parallel()
		h := newConfigured(t)
		sid := h.newSession(t)
		h.ws.editErr = fmt.Errorf("switch: %w", agent.ErrVariantNotAvailable)
		_, err := h.agent.SetConfigOption(t.Context(), protocol.SetConfigOptionRequest{
			SessionID: sid, ConfigID: optModel, Value: modelValue("anthropic", "sonnet"),
		})
		requireCode(t, err, protocol.CodeInvalidParams)
	})

	t.Run("other failures are internal", func(t *testing.T) {
		t.Parallel()
		h := newConfigured(t)
		sid := h.newSession(t)
		h.ws.editErr = errors.New("disk full")
		_, err := h.agent.SetConfigOption(t.Context(), protocol.SetConfigOptionRequest{
			SessionID: sid, ConfigID: optModel, Value: modelValue("anthropic", "sonnet"),
		})
		requireCode(t, err, protocol.CodeInternal)
	})
}

func TestPrompt(t *testing.T) {
	t.Parallel()

	t.Run("updates arrive before the response", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		h.ws.run = h.answerWith("hello")

		resp, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
		require.Equal(t, []protocol.SessionUpdate{answer("a1", "hello")}, h.client.updates(sid))
	})

	t.Run("a message event that was lost is recovered from the database", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		h.ws.run = func(ctx context.Context, sessionID, _ string, _ []message.Attachment) error {
			m := assistantMsg("a1", message.TextContent{Text: "recovered"}, message.Finish{Reason: message.FinishReasonEndTurn})
			m.SessionID = sessionID
			h.ws.setMessages(sessionID, m)
			h.finishRun(ctx, sessionID, notify.RunComplete{MessageID: m.ID})
			return nil
		}

		resp, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
		require.Equal(t, []protocol.SessionUpdate{answer("a1", "recovered")}, h.client.updates(sid))
	})

	t.Run("the completion of another run is ignored", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		h.ws.run = func(ctx context.Context, sessionID, _ string, _ []message.Attachment) error {
			h.finishRun(ctx, sessionID, notify.RunComplete{RunID: "someone-else", Cancelled: true})
			h.finishRun(ctx, sessionID, notify.RunComplete{})
			return nil
		}
		resp, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
	})

	t.Run("run outcomes map to stop reasons", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			rc     notify.RunComplete
			want   protocol.StopReason
			failed bool
		}{
			{name: "cancelled", rc: notify.RunComplete{Cancelled: true}, want: protocol.StopReasonCancelled},
			{name: "truncated", rc: notify.RunComplete{Truncated: true}, want: protocol.StopReasonMaxTokens},
			{name: "error", rc: notify.RunComplete{Error: "provider exploded"}, failed: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t)
				sid := h.newSession(t)
				h.ws.run = h.completeWith(tt.rc)
				resp, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
				if tt.failed {
					requireCode(t, err, protocol.CodeInternal)
					require.ErrorContains(t, err, tt.rc.Error)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tt.want, resp.StopReason)
			})
		}
	})

	t.Run("a run that fails before it can report returns its error", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		h.ws.run = func(context.Context, string, string, []message.Attachment) error { return errors.New("no model") }
		_, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "no model")
	})

	t.Run("a missing completion event ends the turn after the grace period", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.agent.completionGrace = 20 * time.Millisecond
		sid := h.newSession(t)
		h.ws.run = func(ctx context.Context, _ string, _ string, _ []message.Attachment) error {
			agent.MarkRunCompletePublished(ctx)
			return nil
		}
		resp, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
	})

	t.Run("a cancelled run without a completion event is still cancelled", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.agent.completionGrace = 20 * time.Millisecond
		sid := h.newSession(t)
		h.ws.run = func(ctx context.Context, _ string, _ string, _ []message.Attachment) error {
			agent.MarkRunCompletePublished(ctx)
			return context.Canceled
		}
		resp, err := h.agent.Prompt(t.Context(), textPrompt(sid, "hi"))
		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonCancelled, resp.StopReason)
	})

	t.Run("unknown session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.newSession(t)
		_, err := h.agent.Prompt(t.Context(), textPrompt("nope", "hi"))
		requireCode(t, err, protocol.CodeNotFound)
	})

	t.Run("empty prompt never starts a run", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		started := false
		h.ws.run = func(context.Context, string, string, []message.Attachment) error {
			started = true
			return nil
		}
		_, err := h.agent.Prompt(t.Context(), textPrompt(sid, "  "))
		requireCode(t, err, protocol.CodeInvalidParams)
		require.False(t, started)
	})
}

func TestCancel(t *testing.T) {
	t.Parallel()

	t.Run("ends the turn as cancelled when the run does not report back", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.agent.completionGrace = 20 * time.Millisecond
		sid := h.newSession(t)

		started := make(chan struct{})
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		h.ws.run = func(context.Context, string, string, []message.Attachment) error {
			close(started)
			<-release
			return nil
		}

		result := h.promptAsync(t, sid)
		<-started
		require.NoError(t, h.agent.Cancel(t.Context(), protocol.CancelRequest{SessionID: sid}))

		got := await(t, result)
		require.NoError(t, got.err)
		require.Equal(t, protocol.StopReasonCancelled, got.resp.StopReason)
		h.ws.snapshot(func() { require.Equal(t, []string{sid}, h.ws.cancelled) })
	})

	t.Run("the real completion wins over the grace period", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		started := make(chan struct{})
		release := make(chan struct{})
		h.ws.run = func(ctx context.Context, sessionID, _ string, _ []message.Attachment) error {
			close(started)
			<-release
			h.finishRun(ctx, sessionID, notify.RunComplete{Cancelled: true})
			return context.Canceled
		}

		result := h.promptAsync(t, sid)
		<-started
		require.NoError(t, h.agent.Cancel(t.Context(), protocol.CancelRequest{SessionID: sid}))
		close(release)

		got := await(t, result)
		require.NoError(t, got.err)
		require.Equal(t, protocol.StopReasonCancelled, got.resp.StopReason)
	})

	t.Run("unknown session is not an error", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.newSession(t)
		require.NoError(t, h.agent.Cancel(t.Context(), protocol.CancelRequest{SessionID: "nope"}))
		h.ws.snapshot(func() { require.Empty(t, h.ws.cancelled) })
	})

	t.Run("a cancel is only recorded once the run holds its reservation", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.agent.completionGrace = 20 * time.Millisecond
		sid := h.newSession(t)

		started := make(chan struct{})
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		h.ws.run = func(context.Context, string, string, []message.Attachment) error {
			close(started)
			<-release
			return nil
		}

		result := h.promptAsync(t, sid)
		<-started
		require.NoError(t, h.agent.Cancel(t.Context(), protocol.CancelRequest{SessionID: sid}))
		await(t, result)

		h.ws.snapshot(func() {
			require.Equal(t, []string{"begin", "run", "cancel"}, h.ws.trace,
				"the reservation must exist before the run starts, or an early cancel is dropped")
		})
	})

	t.Run("a cancel while the prompt is still being set up finds the reservation", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		inSetup := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		h.ws.snapshot(func() {
			h.ws.onActive = func() {
				once.Do(func() {
					close(inSetup)
					<-release
				})
			}
		})
		h.ws.run = h.completeWith(notify.RunComplete{Cancelled: true})

		result := h.promptAsync(t, sid)
		<-inSetup
		require.NoError(t, h.agent.Cancel(t.Context(), protocol.CancelRequest{SessionID: sid}))
		close(release)

		got := await(t, result)
		require.NoError(t, got.err)
		require.Equal(t, protocol.StopReasonCancelled, got.resp.StopReason)
		h.ws.snapshot(func() {
			require.Equal(t, []string{"begin", "cancel", "run"}, h.ws.trace,
				"a cancel during setup must land on a held reservation, or the run starts as if it never came")
		})
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("shuts the workspace down", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.newSession(t)
		h.agent.Close()
		h.ws.snapshot(func() { require.Equal(t, 1, h.ws.shutdowns) })
	})

	t.Run("without a workspace", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		require.NotPanics(t, h.agent.Close)
	})
}
