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

	last := got.Prompt[len(got.Prompt)-1]
	require.Equal(t, fantasy.MessageRoleUser, last.Role)
	text := messageText(last)
	require.Contains(t, text, "SUMMARY INSTRUCTIONS")
	require.Contains(t, text, summaryToolsDisabledNote)
	require.Contains(t, text, summaryTagOpen)
	require.Less(t, strings.Index(text, "SUMMARY INSTRUCTIONS"), strings.Index(text, summaryToolsDisabledNote))
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

// TestSummarizeSharedPrefixNeverRunsTools pins that a model which reaches
// for a tool during a summary is refused and the summary is rejected like
// any response without the sentinel tags.
func TestSummarizeSharedPrefixNeverRunsTools(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	var runs atomic.Int32
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
			return func(yield func(fantasy.StreamPart) bool) {
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: "tool-1", ToolCallName: "counter"}) {
					return
				}
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: "tool-1"}) {
					return
				}
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "tool-1", ToolCallName: "counter", ToolCallInput: "{}"}) {
					return
				}
				yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
			}, nil
		}).Times(summaryMaxAttempts)

	compact := sharedPrefixCompact(compactModel, newCountingTool("counter", &runs))
	err := sa.Summarize(t.Context(), sessID, compact, nil, nil)
	require.Error(t, err)
	require.Zero(t, runs.Load(), "the host's tools must not run while summarizing")

	updated, getErr := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, getErr)
	require.Empty(t, updated.SummaryMessageID)
}
