package agent

import (
	"context"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestTitleMaxTokens pins the three-way precedence: a reasoning model
// always gets its full default budget (thinking needs the room), a
// non-reasoning model honors an explicit agent override, and otherwise
// falls back to the model's own default.
func TestTitleMaxTokens(t *testing.T) {
	t.Parallel()

	reasoning := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{
		CanReason:        true,
		DefaultMaxTokens: 8000,
	}}}
	require.Equal(t, int64(8000), titleMaxTokens(config.Agent{MaxTokens: ptrTo(int64(40))}, reasoning),
		"a reasoning model must keep its full budget even with an agent override")

	withOverride := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{DefaultMaxTokens: 8000}}}
	require.Equal(t, int64(40), titleMaxTokens(config.Agent{MaxTokens: ptrTo(int64(40))}, withOverride),
		"a non-reasoning model must honor the agent's own cap")

	require.Equal(t, int64(8000), titleMaxTokens(config.Agent{}, withOverride),
		"with no override, the model's own default applies")

	require.Equal(t, int64(8000), titleMaxTokens(config.Agent{MaxTokens: ptrTo(int64(0))}, withOverride),
		"a zero override must not disable the model default")
}

// TestGenerateSessionTitleEmptyPromptIsANoOp pins that an empty user
// prompt never touches the session at all, not even the fallback name.
func TestGenerateSessionTitleEmptyPromptIsANoOp(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}

	sess, err := env.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	coord.generateSessionTitle(t.Context(), sess.ID, "")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestGenerateSessionTitleFallsBackWhenTitleAgentIsNotConfigured pins
// that a session still gets a usable name when the title agent cannot
// be resolved at all (e.g. no coder agent configured).
func TestGenerateSessionTitleFallsBackWhenTitleAgentIsNotConfigured(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}

	sess, err := env.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	coord.generateSessionTitle(t.Context(), sess.ID, "help me fix a bug")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, DefaultSessionName, after.Title)
}

// TestGenerateSessionTitleFallsBackWhenTheProviderIsUnreachable pins
// the deferred-fallback guarantee end to end: the title agent resolves
// cleanly (model, system prompt, everything), but the completion call
// itself fails, and the session must still land on the default title
// rather than being left titleless.
func TestGenerateSessionTitleFallsBackWhenTheProviderIsUnreachable(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	// The mock provider points at an unreachable address; bound the call
	// so the SDK's connection-retry backoff doesn't stall the test.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	coord.generateSessionTitle(ctx, sess.ID, "help me fix a bug")

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, DefaultSessionName, after.Title)
}
