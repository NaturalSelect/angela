package agent

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// autoContinueAgent builds a resolvedAgent around model with a large
// enough context window that the compaction StopWhen condition never
// fires, isolating the max-tokens auto-continue path from
// auto-summarization.
func autoContinueAgent(model fantasy.LanguageModel) resolvedAgent {
	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}
	return resolvedAgent{
		ID:        config.AgentCoder,
		Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
		MaxTokens: catwalkCfg.DefaultMaxTokens,
	}
}

// TestRun_AutoContinuesOnMaxTokens verifies that a turn cut off by the
// model's own output token limit is automatically resumed with a fixed
// follow-up prompt, so the final answer isn't left visibly truncated.
func TestRun_AutoContinuesOnMaxTokens(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	truncated := model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"partial answer, got cut off"}, fantasy.FinishReasonLength), nil)
	finished := model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"...and the rest of the answer."}, fantasy.FinishReasonStop), nil)
	gomock.InOrder(truncated, finished)

	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent:     autoContinueAgent(model),
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "hello",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 4, "user prompt, truncated reply, synthetic continue prompt, final reply")

	require.Equal(t, message.User, msgs[0].Role)
	require.Equal(t, "hello", msgs[0].Content().Text)

	require.Equal(t, message.Assistant, msgs[1].Role)
	require.Equal(t, message.FinishReasonMaxTokens, msgs[1].FinishReason())
	require.Contains(t, msgs[1].Content().Text, "partial answer")

	require.Equal(t, message.User, msgs[2].Role)
	require.Equal(t, autoContinuePrompt, msgs[2].Content().Text,
		"the synthetic follow-up must be the fixed auto-continue prompt")

	require.Equal(t, message.Assistant, msgs[3].Role)
	require.Equal(t, message.FinishReasonEndTurn, msgs[3].FinishReason())
	require.Contains(t, msgs[3].Content().Text, "rest of the answer")

	_, queued := sa.messageQueue.Get(sess.ID)
	require.False(t, queued, "the queue must be drained once the turn ends cleanly")
}

// TestRun_AutoContinuesMultipleTimes verifies the auto-continue
// mechanism keeps resuming across more than one truncation in a row,
// since there is no cap on how many times a turn can be resumed.
func TestRun_AutoContinuesMultipleTimes(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	const truncations = 3
	model := newMockLanguageModel(t)
	var calls []any
	for i := range truncations {
		calls = append(calls, model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{fmt.Sprintf("chunk %d, ", i)}, fantasy.FinishReasonLength), nil))
	}
	calls = append(calls, model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"the end."}, fantasy.FinishReasonStop), nil))
	gomock.InOrder(calls...)

	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent:     autoContinueAgent(model),
		SessionID: sess.ID,
		RunID:     "run-multi",
		Prompt:    "hello",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var assistantMsgs, userMsgs int
	for i, m := range msgs {
		switch m.Role {
		case message.Assistant:
			assistantMsgs++
		case message.User:
			userMsgs++
			if i > 0 {
				require.Equal(t, autoContinuePrompt, m.Content().Text,
					"every follow-up after the first user prompt must be the fixed auto-continue prompt")
			}
		}
	}
	require.Equal(t, truncations+1, assistantMsgs, "one assistant reply per truncation plus the final clean one")
	require.Equal(t, truncations+1, userMsgs, "the original prompt plus one synthetic continue per truncation")

	last := msgs[len(msgs)-1]
	require.Equal(t, message.Assistant, last.Role)
	require.Equal(t, message.FinishReasonEndTurn, last.FinishReason())
	require.Contains(t, last.Content().Text, "the end.")

	_, queued := sa.messageQueue.Get(sess.ID)
	require.False(t, queued, "the queue must be drained once the turn ends cleanly")
}

// toolCallThenFinish streams a single pending tool call followed
// immediately by a finish carrying usage, so a StopWhen condition
// evaluated right after this step sees the reported usage while the
// tool call itself is left unexecuted, the same way a turn that gets
// cut off mid-tool-use by auto-compaction does.
func toolCallThenFinish(usage fantasy.Usage) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "call-1", ToolCallName: "some_tool", ToolCallInput: "{}"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: usage})
	}
}

// TestRun_RepeatedAutoCompactionsDoNotNestTheResumePrompt is the
// regression for a reported bug where a turn auto-compacted more than
// once while the same tool call stayed pending kept wrapping the
// "previous session was interrupted" preamble around the already
// -wrapped prompt, nesting it deeper on every compaction.
func TestRun_RepeatedAutoCompactionsDoNotNestTheResumePrompt(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// A small context window with heavily-reported usage forces the
	// StopWhen condition to fire on the first two turns, each of
	// which leaves the tool call above pending (unexecuted).
	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"done"}, fantasy.FinishReasonStop), nil),
	)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"summary"}, fantasy.FinishReasonStop), nil).
		Times(2)

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        config.AgentCoder,
			Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
			MaxTokens: catwalkCfg.DefaultMaxTokens,
		},
		Compact:   compact,
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "hello",
	})
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var userPrompts []string
	for _, m := range msgs {
		if m.Role == message.User {
			userPrompts = append(userPrompts, m.Content().Text)
		}
	}
	require.Len(t, userPrompts, 3, "the original prompt plus one resumed prompt per compaction")
	require.Equal(t, "hello", userPrompts[0])
	require.Contains(t, userPrompts[1], "hello", "the resumed prompt must still carry the original request")
	require.Equal(t, userPrompts[1], userPrompts[2],
		"a second compaction of the same queued turn must not wrap the resume prompt again")
	require.Equal(t, 1, strings.Count(userPrompts[2], "The previous session was interrupted"),
		"the wrapper text must appear exactly once no matter how many compactions the turn goes through")
}
