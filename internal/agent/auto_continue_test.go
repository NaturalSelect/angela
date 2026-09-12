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

// TestRun_AutoContinueDoesNotResendAttachments is the regression for a
// bug where the synthetic continuation reused the original call's
// Attachments verbatim, so every auto-continue round re-sent the same
// files/images the user attached to their original prompt.
func TestRun_AutoContinueDoesNotResendAttachments(t *testing.T) {
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
		Attachments: []message.Attachment{
			{FileName: "photo.png", FilePath: "photo.png", MimeType: "image/png", Content: []byte("fake-png")},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 4, "user prompt, truncated reply, synthetic continue prompt, final reply")

	require.Equal(t, message.User, msgs[0].Role)
	require.Len(t, msgs[0].BinaryContent(), 1, "the original turn keeps its attachment")

	require.Equal(t, message.User, msgs[2].Role)
	require.Equal(t, autoContinuePrompt, msgs[2].Content().Text)
	require.Empty(t, msgs[2].BinaryContent(),
		"the synthetic continuation must not resend the original turn's attachments")
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
	for _, m := range msgs {
		switch m.Role {
		case message.Assistant:
			assistantMsgs++
		case message.User:
			// Persisted reminders (e.g. todo recency, once enough
			// assistant turns pile up across truncations) are also
			// User-role messages but are not a follow-up prompt.
			if m.IsReminder() {
				continue
			}
			userMsgs++
			if userMsgs > 1 {
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

// textThenFinish streams a single plain-text reply with an explicit
// finish reason and usage, so a StopWhen condition evaluated right
// after this step can be pushed over the compaction threshold on a
// turn with no tool calls of its own, instead of depending on
// streamOf's approximate token estimate.
func textThenFinish(text string, finish fantasy.FinishReason, usage fantasy.Usage) fantasy.StreamResponse {
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
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: finish, Usage: usage})
	}
}

// TestRun_SubAgentSkipsSummarizeWhenAlreadyDone is the regression for a
// reported bug where a sub-agent's session happened to cross the
// compaction threshold on the very step that already produced its
// final answer (no pending tool calls). A Task/Agent tool session is
// created fresh per call and never resumed, so compacting it right
// before returning serves no purpose: this must return the answer
// directly instead of spending an extra round trip compacting a
// session that is about to be discarded.
func TestRun_SubAgentSkipsSummarizeWhenAlreadyDone(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// A small context window with heavily-reported usage forces the
	// StopWhen condition to fire on this step, the same way a real
	// sub-agent's finished answer can happen to land right at the
	// compaction threshold.
	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(textThenFinish("the answer is 42", fantasy.FinishReasonStop, fantasy.Usage{InputTokens: 900}), nil)

	// No Stream expectation is set on the compact model: if the fix
	// regresses, Summarize would call it and gomock fails the test for
	// an unexpected call.
	compactModel := newMockLanguageModel(t)
	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        "task",
			Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
			MaxTokens: catwalkCfg.DefaultMaxTokens,
		},
		Compact:        compact,
		SessionID:      sess.ID,
		RunID:          "run-1",
		Prompt:         "do the thing",
		NonInteractive: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "user prompt and the final reply, with no summary message and no resumed prompt")

	require.Equal(t, message.User, msgs[0].Role)
	require.Equal(t, "do the thing", msgs[0].Content().Text)

	require.Equal(t, message.Assistant, msgs[1].Role)
	require.Equal(t, message.FinishReasonEndTurn, msgs[1].FinishReason())
	require.Contains(t, msgs[1].Content().Text, "the answer is 42")
	require.False(t, msgs[1].IsSummaryMessage, "the returned answer must not be replaced by a compaction summary")

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, updated.SummaryMessageID, "a one-shot sub-agent session that already finished must not be compacted")

	_, queued := sa.messageQueue.Get(sess.ID)
	require.False(t, queued, "a finished sub-agent turn must not be requeued for a resume that will never come")
}

// TestRun_InteractiveSessionStillSummarizesWhenAlreadyDone guards the
// fix above against being too broad. Unlike a sub-agent session, an
// interactive session is reused across turns, so compacting it as
// soon as it crosses the threshold is still worthwhile even when the
// turn that crossed it produced no tool calls of its own.
func TestRun_InteractiveSessionStillSummarizesWhenAlreadyDone(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(textThenFinish("here is your answer", fantasy.FinishReasonStop, fantasy.Usage{InputTokens: 900}), nil)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"summary"}, fantasy.FinishReasonStop), nil).
		Times(1)

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

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID, "an interactive session must still be compacted once it crosses the threshold")
}

// TestRun_SubAgentStillResumesWhenNotDone guards the same fix from the
// other direction: a sub-agent that still has a pending tool call when
// it crosses the compaction threshold has not actually finished, so it
// must still be summarized and resumed exactly like the interactive
// case.
func TestRun_SubAgentStillResumesWhenNotDone(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"done"}, fantasy.FinishReasonStop), nil),
	)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"summary"}, fantasy.FinishReasonStop), nil).
		Times(1)

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        "task",
			Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
			MaxTokens: catwalkCfg.DefaultMaxTokens,
		},
		Compact:        compact,
		SessionID:      sess.ID,
		RunID:          "run-1",
		Prompt:         "do the thing",
		NonInteractive: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID, "a sub-agent turn interrupted mid-tool-use must still be compacted")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var userPrompts []string
	for _, m := range msgs {
		if m.Role == message.User {
			userPrompts = append(userPrompts, m.Content().Text)
		}
	}
	require.Len(t, userPrompts, 2, "the original prompt plus the resumed prompt after compaction")
	require.Contains(t, userPrompts[1], "do the thing", "the resumed prompt must still carry the original request")
}

// TestRun_SubAgentSummarizesOnMaxTokensWithNoToolCalls guards the fix
// against treating a response merely truncated by the output-token
// limit as "done": with no tool calls of its own it looks the same as
// a natural finish, but the turn is not actually complete, so it must
// still go through the compact-and-resume path instead of being
// silently skipped.
func TestRun_SubAgentSummarizesOnMaxTokensWithNoToolCalls(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(textThenFinish("partial answer, cut off", fantasy.FinishReasonLength, fantasy.Usage{InputTokens: 900}), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"...rest of the answer."}, fantasy.FinishReasonStop), nil),
	)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"summary"}, fantasy.FinishReasonStop), nil).
		Times(1)

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        "task",
			Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
			MaxTokens: catwalkCfg.DefaultMaxTokens,
		},
		Compact:        compact,
		SessionID:      sess.ID,
		RunID:          "run-1",
		Prompt:         "do the thing",
		NonInteractive: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID, "a max-tokens cutoff must still be compacted even with no tool calls of its own")
}

// pendingToolCallThenMaxTokens streams a tool call whose input starts
// but never receives its closing event before the step hits the
// output token limit, the same way Anthropic ends a stream when
// max_tokens is reached while a tool_use block is still being
// generated.
func pendingToolCallThenMaxTokens() fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: "call-truncated", ToolCallName: "some_tool"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputDelta, ID: "call-truncated", Delta: `{"arg": "partial`}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonLength})
	}
}

// TestRun_FinalizesToolCallTruncatedByMaxTokens is the regression for a
// bug where a tool call whose arguments were still streaming when the
// model hit its output token limit never fired the provider's
// tool-call-finished event. The call stayed unfinished forever: its
// card was stuck pending in the UI, and the auto-continue follow-up
// would have carried a tool_use with no matching tool_result.
func TestRun_FinalizesToolCallTruncatedByMaxTokens(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	truncated := model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(pendingToolCallThenMaxTokens(), nil)
	finished := model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"done"}, fantasy.FinishReasonStop), nil)
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
	require.Len(t, msgs, 5, "user prompt, truncated reply, synthetic error result, synthetic continue prompt, final reply")

	require.Equal(t, message.User, msgs[0].Role)

	require.Equal(t, message.Assistant, msgs[1].Role)
	require.Equal(t, message.FinishReasonMaxTokens, msgs[1].FinishReason())
	toolCalls := msgs[1].ToolCalls()
	require.Len(t, toolCalls, 1)
	require.True(t, toolCalls[0].Finished, "the truncated tool call must be finalized instead of staying pending forever")

	require.Equal(t, message.Tool, msgs[2].Role)
	var foundResult, resultIsError bool
	for _, tr := range msgs[2].ToolResults() {
		if tr.ToolCallID == toolCalls[0].ID {
			foundResult = true
			resultIsError = tr.IsError
		}
	}
	require.True(t, foundResult, "a synthetic tool_result must exist so the next turn isn't sent with a dangling tool_use")
	require.True(t, resultIsError)

	require.Equal(t, message.User, msgs[3].Role)
	require.Equal(t, autoContinuePrompt, msgs[3].Content().Text)

	require.Equal(t, message.Assistant, msgs[4].Role)
	require.Equal(t, message.FinishReasonEndTurn, msgs[4].FinishReason())
	require.Contains(t, msgs[4].Content().Text, "done")

	_, queued := sa.messageQueue.Get(sess.ID)
	require.False(t, queued, "the queue must be drained once the turn ends cleanly")
}
