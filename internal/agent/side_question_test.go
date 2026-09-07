package agent

import (
	"context"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func sideQuestionAgent(model fantasy.LanguageModel) resolvedAgent {
	return resolvedAgent{
		Model:        Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
		SystemPrompt: "side",
	}
}

// TestSideQuestion_SendsNoToolsAndDoesNotPersist is the core contract of
// a side question: it answers from the session's existing messages, but
// the model receives no tools to call, and neither the question nor the
// answer is written back to the session's message history.
func TestSideQuestion_SendsNoToolsAndDoesNotPersist(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	before, err := env.messages.List(t.Context(), sessID)
	require.NoError(t, err)

	var captured fantasy.Call
	sideModel := newMockLanguageModel(t)
	sideModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			captured = call
			return streamOf([]string{"the answer"}, fantasy.FinishReasonStop), nil
		})

	answer, err := sa.SideQuestion(t.Context(), sessID, "what did we just talk about?", sideQuestionAgent(sideModel), nil)
	require.NoError(t, err)
	require.Equal(t, "the answer", answer)
	require.Empty(t, captured.Tools, "a side question must never be given tools to call")

	after, err := env.messages.List(t.Context(), sessID)
	require.NoError(t, err)
	require.Len(t, after, len(before), "a side question must not write to the session's message history")
}

// TestSideQuestion_RunsConcurrentlyWithActiveTurn proves SideQuestion
// takes no lock and registers nowhere: it must complete while another
// turn on the same session is still blocked inside Stream, something
// Run and Summarize cannot do (they'd deadlock or return ErrSessionBusy).
func TestSideQuestion_RunsConcurrentlyWithActiveTurn(t *testing.T) {
	t.Parallel()

	turn := &gatedStreamModel{text: "main turn output", gate: make(chan struct{}), entered: make(chan struct{})}
	sa, env, resolvedModel := summarizeTestAgent(t, turn)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     resolvedModel,
				MaxTokens: resolvedModel.CatwalkCfg.DefaultMaxTokens,
			},
			SessionID: sess.ID,
			RunID:     "run-main",
			Prompt:    "main",
		})
		mainDone <- runErr
	}()

	select {
	case <-turn.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	sideModel := newMockLanguageModel(t)
	sideModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"side answer"}, fantasy.FinishReasonStop), nil)

	answer, err := sa.SideQuestion(t.Context(), sess.ID, "what are we doing?", sideQuestionAgent(sideModel), nil)
	require.NoError(t, err, "a side question must not be refused or blocked while a turn is active")
	require.Equal(t, "side answer", answer)

	close(turn.gate)
	require.NoError(t, <-mainDone)
}

// TestSideQuestion_EmptySessionReturnsError verifies a session with no
// prior messages is rejected before ever reaching the model: there is no
// context for a side question to answer from.
func TestSideQuestion_EmptySessionReturnsError(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	_, err = sa.SideQuestion(t.Context(), sess.ID, "anything?", sideQuestionAgent(newMockLanguageModel(t)), nil)
	require.ErrorContains(t, err, "no conversation context yet")
}

// TestSideQuestion_UnavailableAgentReturnsError verifies an agent that
// failed to resolve is rejected immediately, without touching the
// session store.
func TestSideQuestion_UnavailableAgentReturnsError(t *testing.T) {
	t.Parallel()

	sa, _ := summarizeGomockEnv(t)
	_, err := sa.SideQuestion(t.Context(), "does-not-exist", "hi", resolvedAgent{}, nil)
	require.ErrorContains(t, err, "agent unavailable")
}

// TestSideQuestion_EmptyAnswerReturnsError verifies a model that streams
// no text is reported as an error rather than silently answered with
// nothing.
func TestSideQuestion_EmptyAnswerReturnsError(t *testing.T) {
	t.Parallel()

	sa, env := summarizeGomockEnv(t)
	sessID := seedSession(t, sa, env)

	sideModel := newMockLanguageModel(t)
	sideModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{""}, fantasy.FinishReasonStop), nil)

	_, err := sa.SideQuestion(t.Context(), sessID, "?", sideQuestionAgent(sideModel), nil)
	require.ErrorContains(t, err, "empty answer")
}
