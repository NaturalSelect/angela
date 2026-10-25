package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openrouter"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// pricedCompactAgent bills 10 per million input tokens and 20 per million
// output tokens, so a test can predict the cost of the usage it scripts.
func pricedCompactAgent(model *MockLanguageModel) resolvedAgent {
	return resolvedAgent{
		Model: Model{
			Model: model,
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{
				ContextWindow:    200000,
				DefaultMaxTokens: 10000,
				CostPer1MIn:      10,
				CostPer1MOut:     20,
			}},
		},
		SystemPrompt: "summarize",
	}
}

func TestSummarizeChargesTheAttemptsItRetried(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)
	before, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)

	compactModel := newMockLanguageModel(t)
	gomock.InOrder(
		compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(textThenFinish("plain prose, no tags", fantasy.FinishReasonStop, fantasy.Usage{InputTokens: 1000, OutputTokens: 100}), nil),
		compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(textThenFinish("still no tags", fantasy.FinishReasonStop, fantasy.Usage{InputTokens: 2000, OutputTokens: 200}), nil),
		compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(textThenFinish("<summary>done</summary>", fantasy.FinishReasonStop, fantasy.Usage{InputTokens: 3000, OutputTokens: 300}), nil),
	)

	require.NoError(t, sa.Summarize(t.Context(), sessID, pricedCompactAgent(compactModel), nil, nil))

	after, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	// 6000 input tokens at 10 and 600 output tokens at 20 per million.
	require.InDelta(t, 0.072, after.Cost-before.Cost, 1e-9,
		"the cost must cover all three attempts, not only the one that was adopted")
}

func TestSummarizeChargesAttemptsWhenEveryOneIsRejected(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)
	before, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(textThenFinish("plain prose, no tags", fantasy.FinishReasonStop, fantasy.Usage{InputTokens: 1000, OutputTokens: 100}), nil).
		Times(summaryMaxAttempts)

	require.Error(t, sa.Summarize(t.Context(), sessID, pricedCompactAgent(compactModel), nil, nil))

	after, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	require.Empty(t, after.SummaryMessageID)
	perAttempt := 1000*10/1e6 + 100*20/1e6
	require.InDelta(t, float64(summaryMaxAttempts)*perAttempt, after.Cost-before.Cost, 1e-9,
		"requests that produced no summary were still paid for")
}

func TestSummarizeChargesTheAttemptThatHitTheTokenLimit(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)
	before, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(textThenFinish("<summary>cut off", fantasy.FinishReasonLength, fantasy.Usage{InputTokens: 1000, OutputTokens: 100}), nil).
		Times(1)

	require.Error(t, sa.Summarize(t.Context(), sessID, pricedCompactAgent(compactModel), nil, nil))

	after, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	require.InDelta(t, 0.012, after.Cost-before.Cost, 1e-9)
}

func TestSummarizeLeavesTheCostAloneWhenNothingWasUsed(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)
	before, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(textThenFinish("plain prose, no tags", fantasy.FinishReasonStop, fantasy.Usage{}), nil).
		Times(summaryMaxAttempts)

	require.Error(t, sa.Summarize(t.Context(), sessID, pricedCompactAgent(compactModel), nil, nil))

	after, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	require.Equal(t, before.Cost, after.Cost)
}

func TestAddUsage(t *testing.T) {
	t.Parallel()

	got := addUsage(
		fantasy.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3, ReasoningTokens: 4, CacheCreationTokens: 5, CacheReadTokens: 6},
		fantasy.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ReasoningTokens: 40, CacheCreationTokens: 50, CacheReadTokens: 60},
	)
	require.Equal(t, fantasy.Usage{InputTokens: 11, OutputTokens: 22, TotalTokens: 33, ReasoningTokens: 44, CacheCreationTokens: 55, CacheReadTokens: 66}, got)
}

func TestAddOpenrouterCosts(t *testing.T) {
	t.Parallel()

	step := func(cost float64) fantasy.StepResult {
		return fantasy.StepResult{Response: fantasy.Response{ProviderMetadata: fantasy.ProviderMetadata{
			openrouter.Name: &openrouter.ProviderMetadata{Usage: openrouter.UsageAccounting{Cost: cost}},
		}}}
	}

	require.Nil(t, addOpenrouterCosts(nil, []fantasy.StepResult{{}}),
		"steps without a reported cost must leave the total unset so token pricing still applies")

	got := addOpenrouterCosts(nil, []fantasy.StepResult{step(0.25), {}, step(0.5)})
	require.NotNil(t, got)
	require.InDelta(t, 0.75, *got, 1e-9)

	carried := 1.0
	got = addOpenrouterCosts(&carried, []fantasy.StepResult{step(0.5)})
	require.InDelta(t, 1.5, *got, 1e-9)
	require.InDelta(t, 1.0, carried, 1e-9, "the input total must not be modified")
}
