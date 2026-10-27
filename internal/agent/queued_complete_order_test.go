package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// stagedStreamModel holds every Stream call at its own gate, so a test can
// freeze a run partway through and look at what has been published so far.
type stagedStreamModel struct {
	mu      sync.Mutex
	calls   int
	gates   []chan struct{}
	entered []chan struct{}
}

func newStagedStreamModel(stages int) *stagedStreamModel {
	m := &stagedStreamModel{}
	for range stages {
		m.gates = append(m.gates, make(chan struct{}))
		m.entered = append(m.entered, make(chan struct{}))
	}
	return m
}

func (m *stagedStreamModel) Provider() string { return "fake" }
func (m *stagedStreamModel) Model() string    { return "fake-model" }

func (m *stagedStreamModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *stagedStreamModel) Stream(ctx context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	stage := m.calls
	m.calls++
	m.mu.Unlock()
	if stage < len(m.gates) {
		close(m.entered[stage])
		select {
		case <-m.gates[stage]:
		case <-ctx.Done():
		}
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "done"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *stagedStreamModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *stagedStreamModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// TestRun_FinishedTurnPublishesBeforeTheQueuedPromptRuns pins that a turn
// whose queue hands off to another prompt reports its own completion before
// that prompt runs. The coordinator gives every turn a hook that holds the
// completion until its Run returns, and the handed-off prompt runs inside
// that same Run. Going through the hook made the first prompt's caller wait
// for all of the second prompt's work, so the responses came back in the
// wrong order.
func TestRun_FinishedTurnPublishesBeforeTheQueuedPromptRuns(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	model := newStagedStreamModel(2)
	staged := Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}}
	titles := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}
	resolved := resolvedAgent{ID: config.AgentCoder, Model: staged, MaxTokens: staged.CatwalkCfg.DefaultMaxTokens}
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
	ch := broker.Subscribe(subCtx)

	// NOTE: This is the coordinator's hook: it keeps the last completion
	// and publishes it only after Run returns.
	var held atomic.Bool
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent:      resolved,
			SessionID:  sess.ID,
			RunID:      "run-first",
			Prompt:     "first",
			OnComplete: func(notify.RunComplete) { held.Store(true) },
		})
		firstDone <- runErr
	}()

	select {
	case <-model.entered[0]:
	case <-time.After(5 * time.Second):
		t.Fatal("first run never entered Stream")
	}
	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent:     resolved,
		SessionID: sess.ID,
		RunID:     "run-second",
		Prompt:    "second",
	})
	require.NoError(t, err)
	require.Nil(t, res, "a busy-session prompt must queue and return (nil, nil)")

	// The first turn finishes and hands off. The second prompt is now
	// running, held inside its model call.
	close(model.gates[0])
	select {
	case <-model.entered[1]:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued prompt never started")
	}

	select {
	case ev := <-ch:
		require.Equal(t, "run-first", ev.Payload.RunID,
			"the first turn must report completion while the queued prompt is still running")
		require.Empty(t, ev.Payload.Error)
		require.False(t, ev.Payload.Cancelled)
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn's completion was held back until the queued prompt finished")
	}
	select {
	case <-firstDone:
		t.Fatal("Run returned while the queued prompt was still running")
	default:
	}
	require.False(t, held.Load(), "the completion must not go through the coordinator's hook")

	close(model.gates[1])
	require.NoError(t, <-firstDone)

	select {
	case ev := <-ch:
		require.Equal(t, "run-second", ev.Payload.RunID)
		require.Empty(t, ev.Payload.Error)
	case <-time.After(5 * time.Second):
		t.Fatal("the queued prompt never reported completion")
	}
}
