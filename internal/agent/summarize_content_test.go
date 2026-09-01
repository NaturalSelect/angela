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

	catwalkCfg := catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}
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
// prompt output (see internal/agent/templates/summary.md): numbered
// "## N. ..." sections, split into hundreds of small chunks so a test
// exercises the debounce/coalescing write path realistically.
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
	return sections, strings.Join(sections, "")
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
		Model:        Model{Model: compactModel, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
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
		Model:        Model{Model: compactModel, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 4096}},
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
			return streamOf([]string{"a short summary"}, fantasy.FinishReasonStop), nil
		})

	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 9000}},
		SystemPrompt: "summarize",
		MaxTokens:    9000,
	}
	require.NoError(t, sa.Summarize(t.Context(), sessID, compact, nil, nil))

	require.NotNil(t, gotMaxTokens, "Summarize must set an explicit output cap rather than leaving the provider default")
	require.EqualValues(t, 9000, *gotMaxTokens)
}
