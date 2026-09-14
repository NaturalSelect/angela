package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// streamOf builds a fantasy.StreamResponse that yields chunks as many
// small text deltas, the way a real SSE response arrives, then finishes
// with the given reason.
func streamOf(chunks []string, finish fantasy.FinishReason) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		for _, c := range chunks {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: c}) {
				return
			}
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: finish})
	}
}

// newMockLanguageModel builds a gomock LanguageModel with Provider/Model
// stubbed for any number of calls, so a test only needs to script Stream.
func newMockLanguageModel(t *testing.T) *MockLanguageModel {
	t.Helper()

	m := NewMockLanguageModel(gomock.NewController(t))
	m.EXPECT().Provider().Return("fake").AnyTimes()
	m.EXPECT().Model().Return("fake-model").AnyTimes()
	return m
}

// summarizeGomockEnv wires a sessionAgent against a real (temp-dir SQLite)
// session/message store, so a test can inspect what actually got
// persisted after Summarize returns.
func summarizeGomockEnv(t *testing.T) (*sessionAgent, fakeEnv) {
	t.Helper()

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

	return sa, env
}

// seedSession creates a session with one prior message: Summarize returns
// early on an empty session, before it ever claims one.
func seedSession(t *testing.T, sa *sessionAgent, env fakeEnv) string {
	t.Helper()

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	seedModel := newMockLanguageModel(t)
	seedModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"hello"}, fantasy.FinishReasonStop), nil)

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}
	_, err = sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        config.AgentCoder,
			Model:     Model{Model: seedModel, CatwalkCfg: catwalkCfg},
			MaxTokens: catwalkCfg.DefaultMaxTokens,
		},
		SessionID: sess.ID,
		RunID:     "run-seed",
		Prompt:    "seed",
	})
	require.NoError(t, err)
	return sess.ID
}

// longMultiSectionSummary builds a summary shaped like the real compact
// prompt output (see internal/agent/templates/summary.md): wrapped in
// <summary> tags, with numbered "## N. ..." sections split into
// hundreds of small chunks so a test exercises the debounce/coalescing
// write path realistically.
func longMultiSectionSummary() (chunks []string, want string) {
	var sections []string
	sections = append(sections, "## 1. Primary Request and Intent\n")
	for i := range 200 {
		sections = append(sections, fmt.Sprintf("word%d ", i))
	}
	sections = append(sections, "\n## 2. Key Technical Concepts\n")
	for i := range 200 {
		sections = append(sections, fmt.Sprintf("term%d ", i))
	}
	sections = append(sections, "\n## 3. Files and Code Sections\n")
	for i := range 200 {
		sections = append(sections, fmt.Sprintf("file%d.go ", i))
	}
	sections = append(sections, "\n## 4. Errors and Fixes\nnone.\n")
	chunks = append([]string{summaryTagOpen}, sections...)
	chunks = append(chunks, summaryTagClose)
	return chunks, strings.TrimSpace(strings.Join(sections, ""))
}

// TestSummarizePreservesFullContentOnCleanFinish reproduces the "compact
// output gets truncated mid-section" report with a model that streams a
// long, multi-section summary across hundreds of small deltas and
// finishes with a clean FinishReasonStop — never hitting an output token
// limit. It proves the debounce/coalescing message-write path in
// internal/message does not drop a trailing chunk, independently of the
// FinishReasonLength guard added to Summarize.
func TestSummarizePreservesFullContentOnCleanFinish(t *testing.T) {
	t.Parallel()

	chunks, want := longMultiSectionSummary()
	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf(chunks, fantasy.FinishReasonStop), nil)

	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
		SystemPrompt: "summarize",
	}
	require.NoError(t, sa.Summarize(t.Context(), sessID, compact, nil, nil))

	updated, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)

	summaryMsg, err := env.messages.Get(t.Context(), updated.SummaryMessageID)
	require.NoError(t, err)
	require.Equal(t, want, summaryMsg.Content().Text,
		"the persisted summary must contain every streamed chunk, not just a prefix")
	require.Contains(t, summaryMsg.Content().Text, "## 3. Files and Code Sections")
	require.Contains(t, summaryMsg.Content().Text, "## 4. Errors and Fixes",
		"the section after the one the report says got cut must have survived")
}

// TestSummarizeRejectsOutputThatHitTheTokenLimit is the regression for the
// actual root cause: fantasy's Anthropic provider defaults MaxTokens to
// 4096 when the caller sets no explicit cap (providers/anthropic/anthropic.go),
// and Summarize used to send no cap at all, and always recorded
// FinishReasonEndTurn regardless of what the provider actually reported.
// A summary cut off by the token limit was therefore accepted as
// SummaryMessageID, silently discarding every message before it. A model
// that stops with FinishReasonLength must now fail the summarize call
// instead of being adopted as the session's memory.
func TestSummarizeRejectsOutputThatHitTheTokenLimit(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	truncated := []string{
		"## 1. Primary Request and Intent\n",
		"the user asked for ",
		"X and then the model ran out of output budget before finishing",
	}
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf(truncated, fantasy.FinishReasonLength), nil)

	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 4096}}},
		SystemPrompt: "summarize",
		MaxTokens:    4096,
	}
	err := sa.Summarize(t.Context(), sessID, compact, nil, nil)
	require.Error(t, err, "a summary truncated by the token limit must not be accepted")

	updated, getErr := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, getErr)
	require.Empty(t, updated.SummaryMessageID,
		"a truncated summary must never become the session's SummaryMessageID — that would discard everything before it")
}

// TestSummarizeSendsAnExplicitOutputCap pins that Summarize always tells
// the provider how much room it has, instead of relying on a
// provider-specific hardcoded default. gomock's strict argument
// verification (via a Do callback) fails the test if MaxOutputTokens
// arrives unset.
func TestSummarizeSendsAnExplicitOutputCap(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	var gotMaxTokens *int64
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			gotMaxTokens = call.MaxOutputTokens
			return streamOf([]string{"<summary>a short summary</summary>"}, fantasy.FinishReasonStop), nil
		})

	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 9000}}},
		SystemPrompt: "summarize",
		MaxTokens:    9000,
	}
	require.NoError(t, sa.Summarize(t.Context(), sessID, compact, nil, nil))

	require.NotNil(t, gotMaxTokens, "Summarize must set an explicit output cap rather than leaving the provider default")
	require.EqualValues(t, 9000, *gotMaxTokens)
}

// TestSummarizeRejectsOutputMissingSummaryTags is the regression for a
// second, distinct failure mode from the token-limit one above: a
// model can ignore the compact prompt entirely and just keep doing the
// task in plain prose, still finishing with a clean FinishReasonStop.
// Nothing about the finish reason distinguishes that from an actual
// summary, so Summarize must inspect the content itself for the
// <summary> wrapper it demanded and refuse to adopt anything else as
// SummaryMessageID.
func TestSummarizeRejectsOutputMissingSummaryTags(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{
			"The article content was fetched successfully. ",
			"Now adding it to the mcp configuration in angela.json.",
		}, fantasy.FinishReasonStop), nil)

	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
		SystemPrompt: "summarize",
	}
	err := sa.Summarize(t.Context(), sessID, compact, nil, nil)
	require.Error(t, err, "output that was never wrapped in <summary> tags must not be accepted as a summary")

	updated, getErr := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, getErr)
	require.Empty(t, updated.SummaryMessageID,
		"a rejected summary must never become the session's SummaryMessageID — that would discard everything before it")
}

// TestSummarizeExtractsContentBetweenSummaryTags pins that a compliant
// answer has its wrapper tags (and anything a model added outside
// them) stripped before being persisted: the tags are a detection
// sentinel for Summarize, not part of the conversation memory the
// resumed turn, or a later compaction, should see.
func TestSummarizeExtractsContentBetweenSummaryTags(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{
			"Sure, here it is.\n", summaryTagOpen, "the actual summary content", summaryTagClose, "\nHope that helps!",
		}, fantasy.FinishReasonStop), nil)

	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
		SystemPrompt: "summarize",
	}
	require.NoError(t, sa.Summarize(t.Context(), sessID, compact, nil, nil))

	updated, err := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, err)
	summaryMsg, err := env.messages.Get(t.Context(), updated.SummaryMessageID)
	require.NoError(t, err)
	require.Equal(t, "the actual summary content", summaryMsg.Content().Text,
		"only the content between the tags must survive, not any preamble or trailing chatter around them")
}

// TestSummarizeAppendsSummaryTagInstructionForAnyCompactAgent pins that
// the <summary> requirement is enforced by Summarize itself, not left
// to summary.md alone: a host agent's CompactAgent can point at any
// agent ID, built-in or custom, whose own system prompt knows nothing
// about the sentinel. Both the instruction and the rejection must
// still apply to it.
func TestSummarizeAppendsSummaryTagInstructionForAnyCompactAgent(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	var gotSystemPrompt string
	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			for _, m := range call.Prompt {
				if m.Role != fantasy.MessageRoleSystem {
					continue
				}
				for _, part := range m.Content {
					if text, ok := part.(fantasy.TextPart); ok {
						gotSystemPrompt += text.Text
					}
				}
			}
			// This agent ignores the instruction and just keeps
			// doing the task instead of summarizing it.
			return streamOf([]string{"done installing the thing, no summary here"}, fantasy.FinishReasonStop), nil
		})

	// A custom compact agent unrelated to the built-in summary.md
	// template, standing in for a host agent whose CompactAgent points
	// at some other agent ID entirely.
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
		SystemPrompt: "You are a pirate. Translate everything into pirate speak.",
	}
	err := sa.Summarize(t.Context(), sessID, compact, nil, nil)
	require.Error(t, err, "a custom compact agent's output must be held to the same <summary> requirement")

	require.Contains(t, gotSystemPrompt, "You are a pirate.",
		"the custom agent's own system prompt must still be sent")
	require.Contains(t, gotSystemPrompt, summaryTagOpen,
		"Summarize must append the <summary> requirement regardless of which agent supplies the base system prompt")

	updated, getErr := env.sessions.Get(t.Context(), sessID)
	require.NoError(t, getErr)
	require.Empty(t, updated.SummaryMessageID)
}
