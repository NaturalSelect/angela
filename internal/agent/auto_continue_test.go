package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
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
		Return(streamOf([]string{"<summary>summary</summary>"}, fantasy.FinishReasonStop), nil).
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
	require.Len(t, userPrompts, 5, "the original prompt, plus a resume reminder and a resumed prompt per compaction")
	require.Equal(t, "hello", userPrompts[0])

	var wrapped []string
	for _, p := range userPrompts {
		if strings.Contains(p, "The previous session was interrupted") {
			wrapped = append(wrapped, p)
		}
	}
	require.Len(t, wrapped, 2, "one resumed prompt per compaction")
	require.Contains(t, wrapped[0], "hello", "the resumed prompt must still carry the original request")
	require.Equal(t, wrapped[0], wrapped[1],
		"a second compaction of the same queued turn must not wrap the resume prompt again")
	require.Equal(t, 1, strings.Count(wrapped[1], "The previous session was interrupted"),
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
		Return(streamOf([]string{"<summary>summary</summary>"}, fantasy.FinishReasonStop), nil).
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
		Return(streamOf([]string{"<summary>summary</summary>"}, fantasy.FinishReasonStop), nil).
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
	require.Len(t, userPrompts, 3, "the original prompt, a resume reminder, and the resumed prompt after compaction")
	require.Contains(t, userPrompts[2], "do the thing", "the resumed prompt must still carry the original request")
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
		Return(streamOf([]string{"<summary>summary</summary>"}, fantasy.FinishReasonStop), nil).
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

// TestRun_ResumeAfterCompactionUsesLatestFoldedMessage is the
// regression for a reported bug: a follow-up message queued while a
// turn is busy can be folded into that same turn once it reaches its
// next step (see drainQueueForStep's fold in PrepareStep), landing in
// the persisted transcript as a real user message instead of sitting
// in the queue. If that same turn is then interrupted by
// auto-compaction mid-tool-use, the wrapped resume prompt used to
// always quote call.Prompt — the turn's very first message — even
// though the folded-in message is the user's actual latest request.
func TestRun_ResumeAfterCompactionUsesLatestFoldedMessage(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// Queued before the turn even starts, so drainQueueForStep folds it
	// into the very first step — the same way a message sent while an
	// earlier step of this turn was still streaming would land there.
	sa.enqueueCall(SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "actually, focus on the auth bug instead",
	})

	model := newMockLanguageModel(t)
	gomock.InOrder(
		// The only step of the original turn: high usage trips
		// auto-compaction immediately, leaving this tool call pending
		// (never executed) and interrupting the turn.
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil),
		// The turn resumed after compaction.
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"done"}, fantasy.FinishReasonStop), nil),
	)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"<summary>summary</summary>"}, fantasy.FinishReasonStop), nil)

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
		Prompt:    "seed task",
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

	var resumed string
	for _, p := range userPrompts {
		if strings.Contains(p, "The previous session was interrupted") {
			resumed = p
		}
	}
	require.NotEmpty(t, resumed, "the interrupted turn must resume with a wrapped prompt")
	require.Contains(t, resumed, "actually, focus on the auth bug instead",
		"the resumed prompt must carry the message folded into the turn, which is the user's actual latest request")
	require.NotContains(t, resumed, "`seed task`",
		"the resumed prompt must not still quote the turn's original message once a later one was folded into the same turn")
}

// TestRun_EscPopDuringSummarizeKeepsResume pins the fix for the queue
// visibility bug: while auto-summarize is in flight, the internal
// resume-after-summarize entry it queued ahead of itself must be
// invisible to the user-facing queue accessors, and taking back a user
// prompt queued behind it (the way Esc does) must leave the internal
// entry alone so the interrupted turn still resumes once summarize
// completes.
func TestRun_EscPopDuringSummarizeKeepsResume(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	gomock.InOrder(
		// The only step of the original turn: high usage trips
		// auto-compaction immediately, leaving this tool call pending.
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil),
		// The turn resumed after compaction.
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"done"}, fantasy.FinishReasonStop), nil),
	)

	compactModel := &gatedStreamModel{
		text:    "<summary>summary</summary>",
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
				MaxTokens: catwalkCfg.DefaultMaxTokens,
			},
			Compact:   compact,
			SessionID: sess.ID,
			RunID:     "run-1",
			Prompt:    "seed task",
		})
		mainDone <- runErr
	}()

	select {
	case <-compactModel.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("summarize never entered Stream")
	}

	// While summarize is in flight, the internal resume entry it
	// enqueued ahead of itself must be invisible to the user-facing
	// queue accessors.
	require.Zero(t, sa.QueuedPrompts(sess.ID), "the internal resume entry must not surface as a queued prompt")
	require.Nil(t, sa.QueuedPromptsList(sess.ID))

	// Queue a user follow-up behind the busy session, exactly like a
	// prompt typed while the auto-triggered summarize is running.
	res, err := sa.Run(context.Background(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "user follow-up",
	})
	require.NoError(t, err)
	require.Nil(t, res, "a busy-session follow-up must enqueue and return (nil, nil)")
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID))

	// Esc: take the user prompt back. The internal entry must stay so
	// summarize still resumes the interrupted turn once it completes.
	taken := sa.TakeQueuedPrompts(sess.ID)
	require.Len(t, taken, 1)
	require.Equal(t, "user follow-up", taken[0].Prompt)
	require.Zero(t, sa.QueuedPrompts(sess.ID))

	close(compactModel.gate)
	require.NoError(t, <-mainDone)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var resumed bool
	for _, m := range msgs {
		if m.Role != message.User {
			continue
		}
		text := m.Content().Text
		if strings.Contains(text, "The previous session was interrupted") {
			resumed = true
		}
		require.NotEqual(t, "user follow-up", text,
			"the taken-back prompt must not still run as its own turn")
	}
	require.True(t, resumed, "the interrupted turn must still resume once the taken-back prompt is gone")
}

// TestRun_CancelDuringSummarizeEmitsOneRunComplete pins two fixes at
// once: cancelling while auto-summarize is in flight must cancel
// Summarize itself (not just leave it running unattended), and the
// originating turn's RunID must receive exactly one terminal
// RunComplete — not one from the cancel's queue drop and a second from
// the outer Run's own defer, and not a resumed turn running after all.
func TestRun_CancelDuringSummarizeEmitsOneRunComplete(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

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

	model := newMockLanguageModel(t)
	// A single Stream call: high usage trips auto-compaction
	// immediately, leaving this tool call pending. No second
	// expectation — a resumed turn must never run once summarize is
	// cancelled.
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil)

	compactModel := &gatedStreamModel{
		text:    "<summary>summary</summary>",
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
				MaxTokens: catwalkCfg.DefaultMaxTokens,
			},
			Compact:   compact,
			SessionID: sess.ID,
			RunID:     "run-1",
			Prompt:    "seed task",
		})
		mainDone <- runErr
	}()

	select {
	case <-compactModel.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("summarize never entered Stream")
	}

	// The second esc press: cancel while summarize is still running.
	// This cancels Summarize's own genCtx, which unblocks compactModel
	// via its ctx.Done() branch instead of the gate.
	sa.Cancel(sess.ID)

	var runErr error
	select {
	case runErr = <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run never returned after Cancel")
	}
	require.Error(t, runErr, "a cancelled summarize must surface as an error from Run")

	require.Zero(t, sa.QueuedPrompts(sess.ID))
	_, queued := sa.messageQueue.Get(sess.ID)
	require.False(t, queued, "the internal resume entry must not survive a cancelled summarize")

	var got []notify.RunComplete
	deadline := time.After(300 * time.Millisecond)
collect:
	for {
		select {
		case ev := <-events:
			got = append(got, ev.Payload)
		case <-deadline:
			break collect
		}
	}
	require.Len(t, got, 1, "run-1 must publish exactly one terminal RunComplete, not two")
	require.Equal(t, "run-1", got[0].RunID)
	require.True(t, got[0].Cancelled)
}

// TestRun_SummarizeFailureDropsResumeKeepsUserPrompts pins that when
// auto-summarize itself fails (as opposed to being cancelled), the
// internal resume entry is dropped rather than left to fire as a stray
// turn later, while any real user prompts queued behind the failed
// summarize are left for the caller (the UI) to take back with their
// content intact.
func TestRun_SummarizeFailureDropsResumeKeepsUserPrompts(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	// A single Stream call: high usage trips auto-compaction
	// immediately, leaving this tool call pending. No second
	// expectation — a resumed turn must never run once summarize fails.
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(toolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil)

	// The compact model ignores the compact prompt and replies in
	// plain prose, never wrapped in <summary> tags — Summarize rejects
	// this as a failure (see TestSummarizeRejectsOutputMissingSummaryTags).
	compactModel := &gatedStreamModel{
		text:    "the model just kept doing the task instead of summarizing",
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
				MaxTokens: catwalkCfg.DefaultMaxTokens,
			},
			Compact:   compact,
			SessionID: sess.ID,
			RunID:     "run-1",
			Prompt:    "seed task",
		})
		mainDone <- runErr
	}()

	select {
	case <-compactModel.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("summarize never entered Stream")
	}

	// A real user prompt queued behind the about-to-fail summarize.
	res, err := sa.Run(context.Background(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "queued while summarize was failing",
	})
	require.NoError(t, err)
	require.Nil(t, res)
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID))

	close(compactModel.gate)

	var runErr error
	select {
	case runErr = <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run never returned")
	}
	require.Error(t, runErr, "a summarize that fails to produce a valid summary must surface as an error from Run")

	// The internal resume entry must be gone, but the real user prompt
	// must still be there, intact, for the caller to take back.
	taken := sa.TakeQueuedPrompts(sess.ID)
	require.Len(t, taken, 1)
	require.Equal(t, "queued while summarize was failing", taken[0].Prompt)

	remaining, ok := sa.messageQueue.Get(sess.ID)
	require.False(t, ok || len(remaining) > 0, "nothing must be left queued once the user prompt is taken")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	for _, m := range msgs {
		if m.Role == message.User {
			require.NotContains(t, m.Content().Text, "The previous session was interrupted",
				"a failed summarize must never resume the interrupted turn")
		}
	}
}
