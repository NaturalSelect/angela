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
