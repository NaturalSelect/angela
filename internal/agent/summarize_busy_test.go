package agent

import (
	"context"
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

func summarizeTestAgent(t *testing.T, model *gatedStreamModel) (*sessionAgent, fakeEnv, Model) {
	t.Helper()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	resolvedModel := Model{
		Model:      model,
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

	return sa, env, resolvedModel
}

// TestSummarizeRefusesWhileATurnIsActive is the deterministic half: a
// summarize requested while a turn holds the session must be refused,
// not run concurrently with it.
func TestSummarizeRefusesWhileATurnIsActive(t *testing.T) {
	t.Parallel()

	turn := &gatedStreamModel{text: "done", gate: make(chan struct{}), entered: make(chan struct{})}
	sa, env, resolvedModel := summarizeTestAgent(t, turn)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     resolvedModel,
				MaxTokens: resolvedModel.CatwalkCfg.DefaultMaxTokens,
			},
			SessionID: sess.ID,
			RunID:     "run-main",
			Prompt:    "main",
		})
		mainDone <- runErr
	}()

	select {
	case <-turn.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	compact := resolvedAgent{Model: resolvedModel, SystemPrompt: "summarize"}
	require.ErrorIs(t, sa.Summarize(t.Context(), sess.ID, compact, nil, nil), ErrSessionBusy)

	close(turn.gate)
	require.NoError(t, <-mainDone)
}

// TestConcurrentSummarizeElectsOneWinner covers the window the busy
// check used to leave open: it ran before two DB round-trips, and the
// active registration only happened after them, so several callers
// could pass the check and all start summarizing the same session.
func TestConcurrentSummarizeElectsOneWinner(t *testing.T) {
	t.Parallel()

	compactModel := &gatedStreamModel{text: "summary", gate: make(chan struct{}), entered: make(chan struct{})}
	sa, env, resolvedModel := summarizeTestAgent(t, compactModel)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// Summarize returns early on an empty session, before it ever
	// claims it — give it something to summarize.
	turn := &gatedStreamModel{text: "hello", gate: make(chan struct{}), entered: make(chan struct{})}
	close(turn.gate)
	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        config.AgentCoder,
			Model:     Model{Model: turn, CatwalkCfg: resolvedModel.CatwalkCfg},
			MaxTokens: resolvedModel.CatwalkCfg.DefaultMaxTokens,
		},
		SessionID: sess.ID,
		RunID:     "run-seed",
		Prompt:    "seed",
	})
	require.NoError(t, err)

	const callers = 4
	compact := resolvedAgent{Model: resolvedModel, SystemPrompt: "summarize"}

	var wg sync.WaitGroup
	results := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- sa.Summarize(context.Background(), sess.ID, compact, nil, nil)
		}()
	}

	select {
	case <-compactModel.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no summarize ever entered Stream")
	}

	// Every loser must be turned away while the winner still holds the
	// claim. Releasing the winner before they have all tried would let
	// a straggler claim the session it just freed and be admitted as a
	// second winner — the contention this test exists to rule out
	// would then depend on how the goroutines happened to be scheduled.
	var admitted int
	for range callers - 1 {
		select {
		case err := <-results:
			require.ErrorIs(t, err, ErrSessionBusy,
				"the winner is still inside Stream, so every other caller must be refused")
		case <-time.After(5 * time.Second):
			t.Fatal("a losing summarize never returned")
		}
	}

	close(compactModel.gate)
	wg.Wait()
	close(results)

	for err := range results {
		if err == nil {
			admitted++
			continue
		}
		require.ErrorIs(t, err, ErrSessionBusy)
	}
	require.Equal(t, 1, admitted, "exactly one summarize may claim the session")
	require.EqualValues(t, 1, compactModel.calls.Load(), "only the winner may reach the model")
}
