package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
)

func TestNoticeInboxKeepsPerSessionOrder(t *testing.T) {
	t.Parallel()

	inbox := newNoticeInbox()
	inbox.Post("s1", "first")
	inbox.Post("s1", "second")
	inbox.Post("s2", "other")

	require.True(t, inbox.Has("s1"))
	require.Equal(t, []string{"first", "second"}, inbox.Take("s1"))
	require.False(t, inbox.Has("s1"), "Take empties the session's inbox")
	require.Empty(t, inbox.Take("s1"))
	require.True(t, inbox.Has("s2"), "another session's notices are untouched")
}

func TestNoticeInboxRequeuePutsNoticesBackInFront(t *testing.T) {
	t.Parallel()

	inbox := newNoticeInbox()
	inbox.Post("s1", "old")
	taken := inbox.Take("s1")
	inbox.Post("s1", "new")
	inbox.Requeue("s1", taken)

	require.Equal(t, []string{"old", "new"}, inbox.Take("s1"))
}

func TestNilNoticeInboxHasNothing(t *testing.T) {
	t.Parallel()

	var inbox *noticeInbox
	require.False(t, inbox.Has("s1"))
	require.Empty(t, inbox.Take("s1"))
	inbox.Requeue("s1", []string{"x"})
	require.NotPanics(t, func() { inbox.Close("s1") })
}

func TestNoticeInboxCloseDropsPendingAndRefusesFurtherNotices(t *testing.T) {
	t.Parallel()

	inbox := newNoticeInbox()
	require.True(t, inbox.Post("s1", "before"))
	require.True(t, inbox.Post("s2", "other"))

	inbox.Close("s1")

	require.False(t, inbox.Has("s1"), "closing drops what was pending")
	require.False(t, inbox.Post("s1", "after"), "a closed session reports nobody to tell")
	require.False(t, inbox.Has("s1"))
	require.Empty(t, inbox.Take("s1"))
	require.True(t, inbox.Has("s2"), "another session is untouched")
	require.True(t, inbox.Post("s2", "still open"))
}

func TestNoticeInboxCloseKeepsRequeuedNoticesOut(t *testing.T) {
	t.Parallel()

	inbox := newNoticeInbox()
	inbox.Post("s1", "one")
	taken := inbox.Take("s1")
	inbox.Close("s1")
	inbox.Requeue("s1", taken)

	require.False(t, inbox.Has("s1"), "a failed delivery must not resurrect a closed session's inbox")
}

// noticeAgentEnv wires a sessionAgent that shares an inbox with the test
// and records every wake request.
type noticeAgentEnv struct {
	sa    *sessionAgent
	env   fakeEnv
	inbox *noticeInbox

	mu    sync.Mutex
	wakes []string
}

func (e *noticeAgentEnv) wakeCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.wakes)
}

func newNoticeAgentEnv(t *testing.T) *noticeAgentEnv {
	t.Helper()

	env := testEnv(t)
	titles := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}
	e := &noticeAgentEnv{env: env, inbox: newNoticeInbox()}
	e.sa = NewSessionAgent(SessionAgentOptions{
		IsYolo:        true,
		Sessions:      env.sessions,
		Messages:      env.messages,
		GenerateTitle: titles.generateSessionTitle,
		Notices:       e.inbox,
		WakeForNotices: func(sessionID string) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.wakes = append(e.wakes, sessionID)
		},
	}).(*sessionAgent)
	return e
}

func (e *noticeAgentEnv) userTexts(t *testing.T, sessionID string) []string {
	t.Helper()
	msgs, err := e.env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	var texts []string
	for _, m := range msgs {
		if m.Role == message.User {
			texts = append(texts, m.Content().Text)
		}
	}
	return texts
}

func TestRun_NoticeWakeOnIdleSessionDeliversNoticesAsAReminder(t *testing.T) {
	t.Parallel()

	e := newNoticeAgentEnv(t)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	e.inbox.Post(sess.ID, "<agent-notification>one</agent-notification>")
	e.inbox.Post(sess.ID, "<agent-notification>two</agent-notification>")

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"noted"}, fantasy.FinishReasonStop), nil)

	_, err = e.sa.Run(t.Context(), SessionAgentCall{
		Agent:      largeWindowAgent(model),
		SessionID:  sess.ID,
		noticeWake: true,
	})
	require.NoError(t, err)

	msgs, err := e.env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var reminders []message.Message
	for _, m := range msgs {
		if m.IsReminder() {
			reminders = append(reminders, m)
		}
	}
	require.Len(t, reminders, 1, "a burst of notices is one reminder")
	text := reminders[0].Content().Text
	require.Contains(t, text, "one")
	require.Contains(t, text, "two")
	require.False(t, e.inbox.Has(sess.ID))
}

func TestRun_NoticeWakeWithNothingPendingDoesNotRun(t *testing.T) {
	t.Parallel()

	e := newNoticeAgentEnv(t)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// A mock with no Stream expectation fails the test if the model runs.
	model := newMockLanguageModel(t)

	res, err := e.sa.Run(t.Context(), SessionAgentCall{
		Agent:      largeWindowAgent(model),
		SessionID:  sess.ID,
		noticeWake: true,
	})
	require.NoError(t, err)
	require.Nil(t, res)
	require.Empty(t, e.userTexts(t, sess.ID))
}

func TestRun_NoticeWakeOnBusySessionIsDroppedAndTheTurnWakesAfterwards(t *testing.T) {
	t.Parallel()

	e := newNoticeAgentEnv(t)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	gated := &gatedStreamModel{text: "done", gate: make(chan struct{}), entered: make(chan struct{})}
	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}
	resolved := resolvedAgent{
		ID:        config.AgentCoder,
		Model:     Model{Model: gated, CatwalkCfg: catwalkCfg},
		MaxTokens: catwalkCfg.DefaultMaxTokens,
	}

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := e.sa.Run(t.Context(), SessionAgentCall{Agent: resolved, SessionID: sess.ID, Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-gated.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	e.inbox.Post(sess.ID, "<agent-notification>late</agent-notification>")
	res, err := e.sa.Run(t.Context(), SessionAgentCall{Agent: resolved, SessionID: sess.ID, noticeWake: true})
	require.NoError(t, err)
	require.Nil(t, res)
	require.Zero(t, e.sa.QueuedPrompts(sess.ID), "a wake must not queue behind a busy turn")
	require.True(t, e.inbox.Has(sess.ID), "the busy turn still owes the notice")

	close(gated.gate)
	require.NoError(t, <-mainDone)

	require.Equal(t, 1, e.wakeCount(), "a turn that ended normally with notices left wakes the session")
}

func TestRun_CanceledTurnLeavesNoticesAndDoesNotWake(t *testing.T) {
	t.Parallel()

	e := newNoticeAgentEnv(t)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	gated := &gatedStreamModel{text: "done", gate: make(chan struct{}), entered: make(chan struct{})}
	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}
	resolved := resolvedAgent{
		ID:        config.AgentCoder,
		Model:     Model{Model: gated, CatwalkCfg: catwalkCfg},
		MaxTokens: catwalkCfg.DefaultMaxTokens,
	}

	mainDone := make(chan struct{})
	go func() {
		defer close(mainDone)
		_, _ = e.sa.Run(t.Context(), SessionAgentCall{Agent: resolved, SessionID: sess.ID, Prompt: "main"})
	}()
	select {
	case <-gated.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	e.inbox.Post(sess.ID, "<agent-notification>pending</agent-notification>")
	e.sa.Cancel(sess.ID)
	e.sa.ClearQueue(sess.ID)
	_ = e.sa.TakeQueuedPrompts(sess.ID)
	<-mainDone

	require.True(t, e.inbox.Has(sess.ID), "cancel, clear and take must not touch the inbox")
	require.Zero(t, e.wakeCount(), "Esc is not answered by a new turn")
}

func TestRun_NoticesPendingAtTurnStartPrecedeThePrompt(t *testing.T) {
	t.Parallel()

	e := newNoticeAgentEnv(t)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	e.inbox.Post(sess.ID, "<agent-notification>earlier</agent-notification>")

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"ok"}, fantasy.FinishReasonStop), nil)

	_, err = e.sa.Run(t.Context(), SessionAgentCall{
		Agent:     largeWindowAgent(model),
		SessionID: sess.ID,
		Prompt:    "what now",
	})
	require.NoError(t, err)

	texts := e.userTexts(t, sess.ID)
	require.Len(t, texts, 2)
	require.Contains(t, texts[0], "earlier")
	require.Equal(t, "what now", texts[1])
	require.Zero(t, e.wakeCount())
}

func TestRun_NoticeArrivingMidTurnReachesTheNextStep(t *testing.T) {
	t.Parallel()

	e := newNoticeAgentEnv(t)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	type noParams struct{}
	arrive := tools.NewTool("some_tool", "posts a notice", func(_ context.Context, _ noParams, _ fantasy.ToolCall) tools.Result {
		e.inbox.Post(sess.ID, "<agent-notification>mid-turn</agent-notification>")
		return tools.Ok("tool done")
	})

	var secondStep fantasy.Call
	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
				secondStep = call
				return streamOf([]string{"saw it"}, fantasy.FinishReasonStop), nil
			}),
	)

	agent := largeWindowAgent(model)
	agent.Tools = []fantasy.AgentTool{arrive}
	_, err = e.sa.Run(t.Context(), SessionAgentCall{Agent: agent, SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	var seen bool
	for _, m := range secondStep.Prompt {
		for _, part := range m.Content {
			if text, ok := part.(fantasy.TextPart); ok && strings.Contains(text.Text, "mid-turn") {
				seen = true
			}
		}
	}
	require.True(t, seen, "the step after the notice arrived must carry it")
	require.False(t, e.inbox.Has(sess.ID))
	require.Zero(t, e.wakeCount(), "a delivered notice needs no wake")
}
