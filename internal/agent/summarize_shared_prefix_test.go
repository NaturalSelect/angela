package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// messageText joins the text parts of a message.
func messageText(m fantasy.Message) string {
	var sb strings.Builder
	for _, part := range m.Content {
		if text, ok := part.(fantasy.TextPart); ok {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

// streamOfToolCall builds a response in which the model calls a tool
// instead of answering.
func streamOfToolCall(name string) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: "tool-1", ToolCallName: name}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: "tool-1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "tool-1", ToolCallName: name, ToolCallInput: "{}"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
	}
}

// sharedPrefixCompact builds a compact agent that borrows its host's
// prefix, the shape compactFor produces when both run on one model.
func sharedPrefixCompact(model *MockLanguageModel, tools ...fantasy.AgentTool) resolvedAgent {
	return resolvedAgent{
		Model:               Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
		Tools:               tools,
		SystemPrompt:        "HOST SYSTEM PROMPT",
		SummaryInstructions: "SUMMARY INSTRUCTIONS",
		SharesHostPrefix:    true,
	}
}

// TestSummarizeSharedPrefixSendsTheHostsRequestShape pins what makes the
// summary request hit the host's prompt cache: the host's system prompt,
// its tool definitions and its breakpoints, with the compact prompt as
// the last user message.
func TestSummarizeSharedPrefixSendsTheHostsRequestShape(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	var runs atomic.Int32
	var got fantasy.Call
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			got = call
			return streamOf([]string{summaryTagOpen, "the summary", summaryTagClose}, fantasy.FinishReasonStop), nil
		})

	compact := sharedPrefixCompact(compactModel, newCountingTool("counter", &runs))
	require.NoError(t, sa.Summarize(t.Context(), sessID, compact, nil, nil))

	var system strings.Builder
	for _, m := range got.Prompt {
		if m.Role == fantasy.MessageRoleSystem {
			system.WriteString(messageText(m))
		}
	}
	require.Equal(t, "HOST SYSTEM PROMPT", system.String(),
		"the system prompt must be the host's, with nothing appended")

	require.Len(t, got.Tools, 1)
	tool, ok := got.Tools[0].(fantasy.FunctionTool)
	require.True(t, ok)
	require.Equal(t, "counter", tool.Name)
	require.Contains(t, tool.ProviderOptions, anthropic.Name,
		"the last tool carries the cache breakpoint, as it does on a normal turn")
	require.NotNil(t, got.ToolChoice)
	require.Equal(t, fantasy.ToolChoiceAuto, *got.ToolChoice,
		"tool_choice must match the host's turns, or Anthropic drops the message cache")

	last := got.Prompt[len(got.Prompt)-1]
	require.Equal(t, fantasy.MessageRoleUser, last.Role)
	text := messageText(last)
	require.Contains(t, text, "SUMMARY INSTRUCTIONS")
	require.Contains(t, text, summaryToolsDisabledNote)
	require.Contains(t, text, summaryTagOpen)
	require.Less(t, strings.Index(text, "SUMMARY INSTRUCTIONS"), strings.Index(text, summaryToolsDisabledNote))
	require.True(t, strings.HasSuffix(text, summaryToolsDisabledNote),
		"the ban on tools is the last thing the model reads before it answers")
	require.Contains(t, last.ProviderOptions, anthropic.Name,
		"the tip of the request carries a cache breakpoint")
	require.Zero(t, runs.Load())

	updated, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
	summaryMsg, err := env.messages.Get(t.Context(), updated.SummaryMessageID)
	require.NoError(t, err)
	require.Equal(t, "the summary", summaryMsg.Content().Text)
}

// TestSummarizeSharedPrefixToolCallIsRefusedAndSummaryContinues pins that
// a model which calls a tool anyway is told in the tool result that tools
// are disabled and then writes the summary in the next step, instead of
// the whole compaction failing.
func TestSummarizeSharedPrefixToolCallIsRefusedAndSummaryContinues(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	var runs atomic.Int32
	var calls []fantasy.Call
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			calls = append(calls, call)
			return streamOfToolCall("counter"), nil
		}).Times(1)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			calls = append(calls, call)
			return streamOf([]string{summaryTagOpen, "the summary", summaryTagClose}, fantasy.FinishReasonStop), nil
		}).Times(1)

	compact := sharedPrefixCompact(compactModel, newCountingTool("counter", &runs))
	require.NoError(t, sa.Summarize(t.Context(), sessID, compact, nil, nil))
	require.Zero(t, runs.Load(), "the host's tools must not run while summarizing")

	require.Len(t, calls, 2)
	for _, call := range calls {
		require.NotNil(t, call.ToolChoice)
		require.Equal(t, fantasy.ToolChoiceAuto, *call.ToolChoice,
			"the follow-up step keeps the host's tool_choice")
	}

	var refusal string
	for _, m := range calls[1].Prompt {
		for _, part := range m.Content {
			result, ok := part.(fantasy.ToolResultPart)
			if !ok {
				continue
			}
			require.Equal(t, "tool-1", result.ToolCallID)
			failure, ok := result.Output.(fantasy.ToolResultOutputContentError)
			require.True(t, ok, "the refusal is returned as an error result")
			refusal = failure.Error.Error()
		}
	}
	require.Equal(t, inertToolMessage, refusal,
		"the model is told in the tool result that tools are disabled")

	updated, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
	summaryMsg, err := env.messages.Get(t.Context(), updated.SummaryMessageID)
	require.NoError(t, err)
	require.Equal(t, "the summary", summaryMsg.Content().Text)
	require.Empty(t, summaryMsg.ToolCalls())
}

// TestSummarizeSharedPrefixNeverRunsTools pins that a model which keeps
// calling tools despite being told they are disabled gets a bounded number
// of steps per attempt, never runs one, and is rejected like any response
// without the sentinel tags.
func TestSummarizeSharedPrefixNeverRunsTools(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	var runs atomic.Int32
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOfToolCall("counter"), nil).Times(summaryMaxAttempts * summaryMaxSteps)

	compact := sharedPrefixCompact(compactModel, newCountingTool("counter", &runs))
	err := sa.Summarize(t.Context(), sessID, compact, nil, nil)
	require.Error(t, err)
	require.Zero(t, runs.Load(), "the host's tools must not run while summarizing")

	updated, getErr := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, getErr)
	require.Empty(t, updated.SummaryMessageID)
}
