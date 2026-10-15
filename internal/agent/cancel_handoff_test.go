package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// cancelWindowModel blocks its first Stream until the run context is
// cancelled, then holds the cancelled turn in its cleanup window until
// released. Later Stream calls finish normally.
type cancelWindowModel struct {
	finishStreamModel
	calls     atomic.Int64
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func newCancelWindowModel() *cancelWindowModel {
	return &cancelWindowModel{
		finishStreamModel: finishStreamModel{text: "done"},
		entered:           make(chan struct{}),
		cancelled:         make(chan struct{}),
		release:           make(chan struct{}),
	}
}

func (m *cancelWindowModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if m.calls.Add(1) > 1 {
		return m.finishStreamModel.Stream(ctx, call)
	}
	close(m.entered)
	<-ctx.Done()
	close(m.cancelled)
	<-m.release
	return nil, ctx.Err()
}

// TestRun_PromptQueuedWhileCancelledTurnCleansUpStillRuns covers a prompt
// submitted after the user cancels but before the cancelled turn has
// released the session. Cancel only clears what was queued when it ran,
// so this prompt lands in the queue behind a turn that will never reach
// the normal queue handoff. It must still run, and each RunID must see
// exactly one terminal event.
func TestRun_PromptQueuedWhileCancelledTurnCleansUpStillRuns(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)
	model := newCancelWindowModel()
	saAgent, resolved := testSessionAgent(env, model, model, "system")
	sa := saAgent.(*sessionAgent)
	sa.runComplete = broker

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	events := broker.Subscribe(t.Context())

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{Agent: resolved, SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	<-model.entered

	sa.Cancel(sess.ID)
	<-model.cancelled

	require.True(t, sa.IsSessionBusy(sess.ID), "the cancelled turn still owns the session during cleanup")
	res, err := sa.Run(t.Context(), SessionAgentCall{Agent: resolved, SessionID: sess.ID, RunID: "run-follow", Prompt: "follow-up"})
	require.NoError(t, err)
	require.Nil(t, res)
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID))

	close(model.release)
	select {
	case runErr := <-mainDone:
		require.ErrorIs(t, runErr, context.Canceled, "the cancelled turn still reports its own cancellation")
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled run never returned")
	}

	require.False(t, sa.IsSessionBusy(sess.ID))
	require.Zero(t, sa.QueuedPrompts(sess.ID), "the follow-up must run instead of staying queued")

	got := map[string]notify.RunComplete{}
	deadline := time.After(5 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-events:
			_, dup := got[ev.Payload.RunID]
			require.False(t, dup, "RunID %q published more than one terminal event", ev.Payload.RunID)
			got[ev.Payload.RunID] = ev.Payload
		case <-deadline:
			t.Fatalf("timed out waiting for both RunCompletes; got %v", got)
		}
	}
	require.True(t, got["run-main"].Cancelled)
	require.False(t, got["run-follow"].Cancelled)
	require.Empty(t, got["run-follow"].Error)
	require.Equal(t, int64(2), model.calls.Load(), "the follow-up must have reached the model")
}

// TestRun_ShutdownCancelDoesNotStartQueuedPrompt covers the other kind of
// cancellation: the parent context ending, as on workspace shutdown. That
// is not a user choice to keep going, so nothing queued may start.
func TestRun_ShutdownCancelDoesNotStartQueuedPrompt(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := newCancelWindowModel()
	saAgent, resolved := testSessionAgent(env, model, model, "system")
	sa := saAgent.(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(ctx, SessionAgentCall{Agent: resolved, SessionID: sess.ID, Prompt: "main"})
		mainDone <- runErr
	}()
	<-model.entered

	res, err := sa.Run(t.Context(), SessionAgentCall{Agent: resolved, SessionID: sess.ID, Prompt: "follow-up"})
	require.NoError(t, err)
	require.Nil(t, res)

	shutdown()
	<-model.cancelled
	close(model.release)
	select {
	case runErr := <-mainDone:
		require.True(t, errors.Is(runErr, context.Canceled))
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled run never returned")
	}

	require.Equal(t, int64(1), model.calls.Load(), "shutdown must not start the queued prompt")
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID))
}
