package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

func testModelRef(provider, model string) Model {
	return Model{
		ModelCfg:   config.SelectedModel{Provider: provider, Model: model},
		CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000},
	}
}

func TestRecordSessionAgentStampsSession(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	c := &coordinator{sessions: env.sessions}

	sess, err := env.sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	require.Empty(t, sess.Agent)

	c.recordSessionAgent(t.Context(), sess.ID, "coder", testModelRef("anthropic", "claude-sonnet-4"))

	got, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "coder", got.Agent)
	require.Equal(t, "anthropic", got.Model.Provider)
	require.Equal(t, "claude-sonnet-4", got.Model.Model)
}

func TestRecordSessionAgentStampsSubSession(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	c := &coordinator{sessions: env.sessions}

	parent, err := env.sessions.Create(t.Context(), "parent")
	require.NoError(t, err)

	subID := env.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	sub, err := env.sessions.CreateTaskSession(t.Context(), subID, parent.ID, "sub task")
	require.NoError(t, err)

	c.recordSessionAgent(t.Context(), sub.ID, "explore", testModelRef("openai", "gpt-5"))

	got, err := env.sessions.Get(t.Context(), sub.ID)
	require.NoError(t, err)
	require.Equal(t, "explore", got.Agent)
	require.Equal(t, "openai", got.Model.Provider)
	require.Equal(t, "gpt-5", got.Model.Model)

	// The parent must not be stamped by its child's dispatch.
	gotParent, err := env.sessions.Get(t.Context(), parent.ID)
	require.NoError(t, err)
	require.Empty(t, gotParent.Agent)
}

func TestRecordSessionAgentSurvivesMissingSession(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	c := &coordinator{sessions: env.sessions}

	// Audit data must never take down a turn: an unknown session is
	// logged and swallowed, not panicked on.
	require.NotPanics(t, func() {
		c.recordSessionAgent(t.Context(), "no-such-session", "coder", testModelRef("anthropic", "claude-sonnet-4"))
	})
}
