package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestRun_RecordsCacheStatsForSingleStep verifies that a step's cache
// read/creation token usage lands on the session's lifetime totals, the
// same way GenOutputTokens/GenDurationMs do.
func TestRun_RecordsCacheStatsForSingleStep(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(textThenFinish("the answer", fantasy.FinishReasonStop, fantasy.Usage{
			OutputTokens:        50,
			CacheReadTokens:     5000,
			CacheCreationTokens: 2000,
		}), nil)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent:     autoContinueAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "hello",
	})
	require.NoError(t, err)

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.EqualValues(t, 5000, updated.CacheReadTokens,
		"the session's lifetime cache-read total must pick up the step's usage")
	require.EqualValues(t, 2000, updated.CacheCreationTokens,
		"the session's lifetime cache-creation total must pick up the step's usage")
}

// TestRun_AccumulatesCacheStatsAcrossStepsIncludingToolCalls verifies
// that the session's cumulative cache totals sum every step of a
// multi-step turn, including one that made a tool call — the same
// inclusion rule TestRun_AccumulatesGenStatsAcrossStepsIncludingToolCalls
// pins for GenOutputTokens/GenDurationMs.
func TestRun_AccumulatesCacheStatsAcrossStepsIncludingToolCalls(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{
				OutputTokens:        20,
				CacheReadTokens:     1000,
				CacheCreationTokens: 4000,
			}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(textThenFinish("all done", fantasy.FinishReasonStop, fantasy.Usage{
				OutputTokens:        30,
				CacheReadTokens:     6000,
				CacheCreationTokens: 0,
			}), nil),
	)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent:     autoContinueAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "do the thing",
	})
	require.NoError(t, err)

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.EqualValues(t, 7000, updated.CacheReadTokens,
		"the session total must sum both steps' cache-read tokens, including the tool-call step")
	require.EqualValues(t, 4000, updated.CacheCreationTokens,
		"the session total must sum both steps' cache-creation tokens, including the tool-call step")
}
