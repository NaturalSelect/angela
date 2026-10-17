package agent

import (
	"context"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// thinkingStartRecorder returns a Stream stub that records the session's
// thinking clock as the model sees it, then replies with the given stream.
func thinkingStartRecorder(t *testing.T, env fakeEnv, sessionID string, seen *[]int64, reply fantasy.StreamResponse) func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	t.Helper()

	return func(ctx context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
		sess, err := env.sessions.Get(ctx, sessionID)
		require.NoError(t, err)
		*seen = append(*seen, sess.ThinkingStartedAt)
		return reply, nil
	}
}

// The status line reads this clock from the session, so it has to be set
// while a step runs and gone once the turn is over.
func TestRun_SetsThinkingClockWhileAStepRunsAndClearsItAfter(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	var seen []int64
	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(thinkingStartRecorder(t, env, sess.ID, &seen,
			textThenFinish("done", fantasy.FinishReasonStop, fantasy.Usage{})))

	before := time.Now()
	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent:     largeWindowAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "hello",
	})
	require.NoError(t, err)

	require.Len(t, seen, 1)
	require.GreaterOrEqual(t, seen[0], before.UnixMilli(), "the step's start must be stamped before the model is asked")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Zero(t, after.ThinkingStartedAt, "nothing is thinking once the turn has ended")
}

// Each step restarts the clock, so the second step of a turn reads its own
// start rather than the turn's.
func TestRun_RestartsThinkingClockOnEachStep(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	var seen []int64
	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			DoAndReturn(thinkingStartRecorder(t, env, sess.ID, &seen,
				delayedToolCallThenFinish(20*time.Millisecond, fantasy.Usage{}))),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			DoAndReturn(thinkingStartRecorder(t, env, sess.ID, &seen,
				textThenFinish("all done", fantasy.FinishReasonStop, fantasy.Usage{}))),
	)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent:     largeWindowAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "do the thing",
	})
	require.NoError(t, err)

	require.Len(t, seen, 2)
	require.NotZero(t, seen[0])
	require.Greater(t, seen[1], seen[0], "the second step must start its own clock after the first step's tool call")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Zero(t, after.ThinkingStartedAt)
}

// A canceled turn takes the early-return paths, which still have to leave
// the session with no running clock or the status line would keep counting.
func TestRun_ClearsThinkingClockWhenTheTurnIsCanceled(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
			sess, err := env.sessions.Get(t.Context(), sess.ID)
			require.NoError(t, err)
			require.NotZero(t, sess.ThinkingStartedAt)
			cancel()
			return nil, context.Canceled
		})

	_, _ = sa.Run(runCtx, SessionAgentCall{
		Agent:     largeWindowAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "hello",
	})

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Zero(t, after.ThinkingStartedAt)
}
