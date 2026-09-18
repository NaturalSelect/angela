package agent

import (
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// delayedTextThenFinish streams a single plain-text reply like
// textThenFinish, but sleeps for delay immediately before yielding the
// finish part. This stands in for the wall-clock time a real model
// spends generating a response, so a test can assert on the resulting
// GenDurationMs without depending on real network latency.
func delayedTextThenFinish(text string, delay time.Duration, finish fantasy.FinishReason, usage fantasy.Usage) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: text}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		time.Sleep(delay)
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: finish, Usage: usage})
	}
}

// delayedToolCallThenFinish streams a single pending tool call like
// toolCallThenFinish, but sleeps for delay immediately before yielding
// the finish part, so a step that goes on to execute a tool still
// records its own measured generation time, independent of whatever
// the (unregistered, in these tests) tool itself takes to run.
func delayedToolCallThenFinish(delay time.Duration, usage fantasy.Usage) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "call-1", ToolCallName: "some_tool", ToolCallInput: "{}"}) {
			return
		}
		time.Sleep(delay)
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: usage})
	}
}

// TestRun_RecordsPreciseGenDurationForSingleStep verifies that a step's
// measured generation time (from OnStepStart to the provider's
// StreamPartTypeFinish) lands on both the assistant message's Finish
// part and the session's lifetime totals, in place of the old
// second-granularity wall-clock diff the UI used to compute.
func TestRun_RecordsPreciseGenDurationForSingleStep(t *testing.T) {
	t.Parallel()

	const delay = 20 * time.Millisecond

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(delayedTextThenFinish("the answer", delay, fantasy.FinishReasonStop, fantasy.Usage{OutputTokens: 50}), nil)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent:     autoContinueAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "hello",
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "user prompt and the single assistant reply")

	finish := msgs[1].FinishPart()
	require.NotNil(t, finish)
	require.Equal(t, int64(50), finish.OutputTokens)
	require.GreaterOrEqual(t, finish.GenDurationMs, int64(20),
		"the message's own recorded generation time must cover at least the artificial delay")

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.EqualValues(t, 50, updated.GenOutputTokens,
		"the session's lifetime output-token total must pick up the step's usage")
	require.GreaterOrEqual(t, updated.GenDurationMs, int64(20),
		"the session's lifetime generation-time total must accumulate the step's measured duration")
}

// TestRun_AccumulatesGenStatsAcrossStepsIncludingToolCalls verifies that
// a multi-step turn records each step's generation time independently
// on its own message, and that the session's cumulative totals sum
// every step, including one that made a tool call. This is an
// intentional behavior change from the old UI-only tok/s calculation,
// which used to skip steps with tool calls entirely.
func TestRun_AccumulatesGenStatsAcrossStepsIncludingToolCalls(t *testing.T) {
	t.Parallel()

	const (
		delay1 = 30 * time.Millisecond
		delay2 = 15 * time.Millisecond
	)

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(delayedToolCallThenFinish(delay1, fantasy.Usage{OutputTokens: 20}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(delayedTextThenFinish("all done", delay2, fantasy.FinishReasonStop, fantasy.Usage{OutputTokens: 30}), nil),
	)

	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent:     autoContinueAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "do the thing",
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 4, "user prompt, first step's reply with a tool call, its tool result, and the second step's reply")

	require.Equal(t, message.User, msgs[0].Role)

	require.Equal(t, message.Assistant, msgs[1].Role)
	firstFinish := msgs[1].FinishPart()
	require.NotNil(t, firstFinish)
	require.Equal(t, int64(20), firstFinish.OutputTokens)
	require.GreaterOrEqual(t, firstFinish.GenDurationMs, int64(30),
		"the first step's own duration must be recorded even though it made a tool call")

	require.Equal(t, message.Tool, msgs[2].Role)

	require.Equal(t, message.Assistant, msgs[3].Role)
	secondFinish := msgs[3].FinishPart()
	require.NotNil(t, secondFinish)
	require.Equal(t, int64(30), secondFinish.OutputTokens)
	require.GreaterOrEqual(t, secondFinish.GenDurationMs, int64(15))

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.EqualValues(t, 50, updated.GenOutputTokens,
		"the session total must sum both steps' output tokens, including the tool-call step")
	require.Equal(t, firstFinish.GenDurationMs+secondFinish.GenDurationMs, updated.GenDurationMs,
		"the session's cumulative duration must equal the exact sum across both steps")
}
