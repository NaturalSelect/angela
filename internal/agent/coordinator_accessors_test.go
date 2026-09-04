package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestNewCoordinatorFailsWithoutACoderAgent pins the one hard
// requirement NewCoordinator enforces up front: every deployment needs
// a coder agent to fall back on, so construction refuses to proceed
// without one instead of leaving a coordinator that panics on first
// use.
func TestNewCoordinatorFailsWithoutACoderAgent(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	coord, err := NewCoordinator(t.Context(), CoordinatorOptions{Config: cfg})
	require.ErrorIs(t, err, errCoderAgentNotConfigured)
	require.Nil(t, coord)
}

// TestNewCoordinatorBuildsAReadyCoordinator pins that a coordinator
// built through the real constructor (not a hand-assembled struct
// literal) comes out immediately usable: the coder agent resolves to
// the model configured for it.
func TestNewCoordinatorBuildsAReadyCoordinator(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	angelaJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"mock": {"id": "mock", "name": "Mock", "type": "openai",
    "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
    "models": [{"id": "mock-model", "name": "Mock", "context_window": 8192, "default_max_tokens": 128}]}},
  "slots": {"main": {"provider": "mock", "model": "mock-model"},
             "chore": {"provider": "mock", "model": "mock-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"), []byte(angelaJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	coord, err := NewCoordinator(t.Context(), CoordinatorOptions{
		Config:      cfg,
		Sessions:    env.sessions,
		Messages:    env.messages,
		Permissions: env.permissions,
	})
	require.NoError(t, err)
	require.NotNil(t, coord)

	active, model, err := coord.ActiveAgent(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, active.Agent.ID)
	require.Equal(t, "mock-model", model.ModelCfg.Model)
}

// TestDefaultModelResolvesConfiguredCoder pins that DefaultModel answers
// with whatever the coder agent is configured to run on, independent of
// any session.
func TestDefaultModelResolvesConfiguredCoder(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	model := coord.DefaultModel()
	require.Equal(t, "small-model", model.ModelCfg.Model, "the coder prefers the chore slot")
}

// TestDefaultModelReturnsZeroValueWhenCoderNotConfigured pins the
// degraded path: with no coder agent in config at all, DefaultModel
// reports a zero Model instead of panicking.
func TestDefaultModelReturnsZeroValueWhenCoderNotConfigured(t *testing.T) {
	t.Parallel()

	coord := &coordinator{cfg: config.NewTestStore(&config.Config{})}
	require.Equal(t, Model{}, coord.DefaultModel())
}

// TestActiveAgentWithEmptySessionIDReturnsConfiguredDefault pins that
// an empty sessionID is the landing-page case: it answers with the
// coder resolved straight from config, the same as DefaultModel.
func TestActiveAgentWithEmptySessionIDReturnsConfiguredDefault(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	active, model, err := coord.ActiveAgent(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, active.Agent.ID)
	require.Equal(t, "small-model", model.ModelCfg.Model)
}

// TestActiveAgentWithEmptySessionIDFailsWhenCoderNotConfigured pins
// that the empty-sessionID path surfaces errCoderAgentNotConfigured
// rather than silently returning a zero agent.
func TestActiveAgentWithEmptySessionIDFailsWhenCoderNotConfigured(t *testing.T) {
	t.Parallel()

	coord := &coordinator{cfg: config.NewTestStore(&config.Config{})}
	active, model, err := coord.ActiveAgent(t.Context(), "")
	require.ErrorIs(t, err, errCoderAgentNotConfigured)
	require.Equal(t, config.ActiveAgent{}, active)
	require.Equal(t, Model{}, model)
}

// TestActiveAgentWithRealSessionResolvesThatSessionsAgent pins that a
// non-empty sessionID routes through activeAgentFor rather than always
// answering with the coder default.
func TestActiveAgentWithRealSessionResolvesThatSessionsAgent(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	active, model, err := coord.ActiveAgent(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, active.Agent.ID)
	require.Equal(t, "small-model", model.ModelCfg.Model)
}

// TestGenerateTitleDelegatesToGenerateSessionTitle pins that the public
// Coordinator method is a plain pass-through: an empty prompt is a
// no-op, exactly like calling generateSessionTitle directly.
func TestGenerateTitleDelegatesToGenerateSessionTitle(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{})}

	sess, err := env.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	coord.GenerateTitle(t.Context(), sess.ID, "")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestCoordinatorLockSessionSerializesConcurrentLockers pins that the
// coordinator-level LockSession delegates to the resolved executor's
// own per-session mutex: a second locker must wait for the first to
// release before it can proceed.
func TestCoordinatorLockSessionSerializesConcurrentLockers(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	unlock, ok := coord.LockSession(t.Context(), sess.ID)
	require.True(t, ok)
	require.NotNil(t, unlock)

	type lockResult struct {
		ok     bool
		unlock func()
	}
	acquired := make(chan lockResult, 1)
	go func() {
		second, ok := coord.LockSession(t.Context(), sess.ID)
		acquired <- lockResult{ok: ok, unlock: second}
	}()

	select {
	case <-acquired:
		t.Fatal("a second LockSession must wait for the first to release")
	case <-time.After(100 * time.Millisecond):
	}

	unlock()

	select {
	case res := <-acquired:
		require.True(t, res.ok)
		res.unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("LockSession did not unblock after release")
	}
}

// TestCoordinatorLockSessionOnAnUnresumableChildSessionFails pins that
// LockSession reports failure the same way BeginAccepted does when a
// child session's route cannot be rebuilt, instead of locking nothing
// and reporting success.
func TestCoordinatorLockSessionOnAnUnresumableChildSessionFails(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	childID := persistedChildSession(t, coord, "an-agent-that-was-deleted")

	unlock, ok := coord.LockSession(t.Context(), childID)
	require.False(t, ok)
	require.Nil(t, unlock)
}
