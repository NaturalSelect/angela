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
			InputTokens:         100,
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
	require.EqualValues(t, 100, updated.UncachedInputTokens,
		"the session's lifetime uncached-input total must pick up the step's usage")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "user prompt and the single assistant reply")

	finish := msgs[1].FinishPart()
	require.NotNil(t, finish)
	require.EqualValues(t, 100, finish.InputTokens,
		"the step's own message must carry its uncached-input tokens")
	require.EqualValues(t, 5000, finish.CacheReadTokens,
		"the step's own message must carry its cache-read tokens")
	require.EqualValues(t, 2000, finish.CacheCreationTokens,
		"the step's own message must carry its cache-creation tokens")
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
				InputTokens:         200,
				OutputTokens:        20,
				CacheReadTokens:     1000,
				CacheCreationTokens: 4000,
			}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(textThenFinish("all done", fantasy.FinishReasonStop, fantasy.Usage{
				InputTokens:         150,
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
	require.EqualValues(t, 350, updated.UncachedInputTokens,
		"the session total must sum both steps' uncached-input tokens, including the tool-call step")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 4, "user prompt, first step's reply with a tool call, its tool result, and the second step's reply")

	firstFinish := msgs[1].FinishPart()
	require.NotNil(t, firstFinish)
	require.EqualValues(t, 200, firstFinish.InputTokens,
		"the first step's own message must carry only its own uncached-input tokens, not the cumulative total")
	require.EqualValues(t, 1000, firstFinish.CacheReadTokens,
		"the first step's own message must carry only its own cache-read tokens, not the cumulative total")
	require.EqualValues(t, 4000, firstFinish.CacheCreationTokens,
		"the first step's own message must carry only its own cache-creation tokens, not the cumulative total")

	secondFinish := msgs[3].FinishPart()
	require.NotNil(t, secondFinish)
	require.EqualValues(t, 150, secondFinish.InputTokens,
		"the second step's own message must carry only its own uncached-input tokens, not the cumulative total")
	require.EqualValues(t, 6000, secondFinish.CacheReadTokens,
		"the second step's own message must carry only its own cache-read tokens, not the cumulative total")
	require.EqualValues(t, 0, secondFinish.CacheCreationTokens,
		"the second step's own message must carry only its own cache-creation tokens, not the cumulative total")
}
