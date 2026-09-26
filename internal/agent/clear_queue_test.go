package agent

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestClearQueueNotifiesEveryDroppedPrompt pins that clearing the queue
// accounts for every prompt it discards. The clear used to Get the
// queue and then Del it as two steps, so a prompt enqueued in between
// was dropped with no terminal RunComplete — and `angela run` blocks on
// that event forever.
func TestClearQueueNotifiesEveryDroppedPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	blocked := &gatedStreamModel{text: "done", gate: make(chan struct{}), entered: make(chan struct{})}
	resolvedModel := Model{
		Model:      blocked,
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
	}
	titles := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}
	sa := NewSessionAgent(SessionAgentOptions{
		IsYolo:        true,
		Sessions:      env.sessions,
		Messages:      env.messages,
		RunComplete:   broker,
		GenerateTitle: titles.generateSessionTitle,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	resolved := resolvedAgent{
		ID:        config.AgentCoder,
		Model:     resolvedModel,
		MaxTokens: resolvedModel.CatwalkCfg.DefaultMaxTokens,
	}

	// Occupy the session so everything after it queues.
	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolved, SessionID: sess.ID, RunID: "run-main", Prompt: "main",
		})
		mainDone <- runErr
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	const queued = 6
	var wg sync.WaitGroup
	for i := range queued {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, runErr := sa.Run(context.Background(), SessionAgentCall{
				Agent:     resolved,
				SessionID: sess.ID,
				RunID:     "run-queued-" + strconv.Itoa(i),
				Prompt:    "queued",
			})
			require.NoError(t, runErr)
		}()
	}
	wg.Wait()
	require.Equal(t, queued, sa.QueuedPrompts(sess.ID))

	sa.ClearQueue(sess.ID)
	require.Zero(t, sa.QueuedPrompts(sess.ID))

	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < queued {
		select {
		case ev := <-events:
			if ev.Payload.RunID == "run-main" {
				continue
			}
			require.True(t, ev.Payload.Cancelled, "a dropped prompt must report as cancelled")
			seen[ev.Payload.RunID] = true
		case <-deadline:
			t.Fatalf("only %d of %d dropped prompts were notified: %v", len(seen), queued, seen)
		}
	}

	close(blocked.gate)
	require.NoError(t, <-mainDone)
}

// TestTakeQueuedPromptsReturnsAttachmentBytesAndLeavesInternalEntry pins
// two properties of TakeQueuedPrompts that QueuedPromptsList
// deliberately does not have: it returns full attachment bytes (the
// list-preview strips them for cheap polling) and it leaves an internal
// resume-after-summarize entry queued behind rather than taking it too.
// It also proves a taken RunID-bearing prompt gets its cancelled
// RunComplete, the same as ClearQueue's drops do.
func TestTakeQueuedPromptsReturnsAttachmentBytesAndLeavesInternalEntry(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	blocked := &gatedStreamModel{text: "done", gate: make(chan struct{}), entered: make(chan struct{})}
	resolvedModel := Model{
		Model:      blocked,
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
	}
	titles := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}
	sa := NewSessionAgent(SessionAgentOptions{
		IsYolo:        true,
		Sessions:      env.sessions,
		Messages:      env.messages,
		RunComplete:   broker,
		GenerateTitle: titles.generateSessionTitle,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	resolved := resolvedAgent{
		ID:        config.AgentCoder,
		Model:     resolvedModel,
		MaxTokens: resolvedModel.CatwalkCfg.DefaultMaxTokens,
	}

	// Occupy the session so everything after it queues.
	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolved, SessionID: sess.ID, RunID: "run-main", Prompt: "main",
		})
		mainDone <- runErr
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	// A user-queued prompt carrying an attachment and a RunID, plus an
	// internal resume-after-summarize entry that must survive the take
	// untouched.
	_, err = sa.Run(context.Background(), SessionAgentCall{
		Agent:     resolved,
		SessionID: sess.ID,
		RunID:     "run-queued",
		Prompt:    "queued with image",
		Attachments: []message.Attachment{
			{FileName: "a.png", MimeType: "image/png", Content: []byte("x")},
		},
	})
	require.NoError(t, err)
	sa.enqueueResumeBeforeSummarize(SessionAgentCall{SessionID: sess.ID, Prompt: "resume"})
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID), "the internal entry must not be counted")

	taken := sa.TakeQueuedPrompts(sess.ID)
	require.Len(t, taken, 1)
	require.Equal(t, "queued with image", taken[0].Prompt)
	require.Len(t, taken[0].Attachments, 1)
	require.Equal(t, []byte("x"), taken[0].Attachments[0].Content,
		"TakeQueuedPrompts must return full attachment bytes, unlike QueuedPromptsList")

	// The internal entry must still be queued: TakeQueuedPrompts is not
	// allowed to take it along with the user prompt.
	remaining, ok := sa.messageQueue.Get(sess.ID)
	require.True(t, ok)
	require.Len(t, remaining, 1)
	require.True(t, remaining[0].internal)
	require.Zero(t, sa.QueuedPrompts(sess.ID))

	select {
	case ev := <-events:
		require.Equal(t, "run-queued", ev.Payload.RunID)
		require.True(t, ev.Payload.Cancelled, "a taken RunID-bearing prompt must report as cancelled")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the taken prompt's RunComplete")
	}

	// Drop the internal entry directly (rather than letting it run) so
	// releasing the main turn below drains to an empty queue: this test
	// is only about TakeQueuedPrompts leaving it behind, not about
	// actually resuming it, and the entry was never wired up with a
	// resolved agent to run.
	sa.popResumeOnSummarizeFailure(sess.ID)

	close(blocked.gate)
	require.NoError(t, <-mainDone)
}
