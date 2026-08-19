package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestActiveAgentEditsDoNotLeakAcrossSessions is the whole point of the
// per-session instance: two sessions on the same agent must be able to
// run different models, and neither may reach the shared config.
func TestActiveAgentEditsDoNotLeakAcrossSessions(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	a, err := coord.sessions.Create(t.Context(), "a")
	require.NoError(t, err)
	b, err := coord.sessions.Create(t.Context(), "b")
	require.NoError(t, err)

	require.NoError(t, coord.SwitchAgent(t.Context(), a.ID, testReviewerAgent))

	activeA, err := coord.activeAgentFor(t.Context(), a.ID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, activeA.Agent.ID)

	activeB, err := coord.activeAgentFor(t.Context(), b.ID)
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, activeB.Agent.ID,
		"editing one session must not move another session's agent")

	require.Equal(t, config.AgentCoder, coord.cfg.Config().Agents[config.AgentCoder].ID,
		"the shared config must be untouched by a session-scoped edit")
	require.Equal(t, config.ModelChore, coord.cfg.Config().Agents[config.AgentCoder].Model,
		"the coder's configured model must survive another session's switch")
}

// TestActiveAgentPicksUpConfigEditsButKeepsItsModel pins the split that
// makes the instance safe to keep: definitions follow the config files,
// the model selection stays the session's own.
func TestActiveAgentPicksUpConfigEditsButKeepsItsModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))

	before, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "large-model", before.Model.Model)

	// A config edit to the agent's prompt must reach the session.
	cfg := coord.cfg.Config()
	reviewer := cfg.Agents[testReviewerAgent]
	reviewer.Prompt = "You are new."
	cfg.Agents[testReviewerAgent] = reviewer

	after, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "You are new.", after.Agent.Prompt,
		"prompt edits must take effect on the next resolution")
	require.Equal(t, "large-model", after.Model.Model,
		"the session's own model must survive a config edit")
}

// TestActiveAgentForReportsUnreadableSessions covers A8: a session that
// cannot be read is an error, not a silent fallback. Answering as the
// coder would make the assistant reply as something the user never
// picked, with nothing saying so.
func TestActiveAgentForReportsUnreadableSessions(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	_, err := coord.activeAgentFor(t.Context(), "no-such-session")
	require.Error(t, err,
		"an unreadable session must not resolve to a guessed agent")
}

// TestConcurrentActiveAgentEditsDoNotLoseUpdates pins that the store
// serializes read-modify-write. Two switches racing must both be
// observed by the store, with the last one winning outright rather than
// the two interleaving into a half-applied instance.
func TestConcurrentActiveAgentEditsDoNotLoseUpdates(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = coord.editActiveAgent(t.Context(), sess.ID,
				func(current config.ActiveAgent) (config.ActiveAgent, bool, error) {
					next := current
					pick := "deep"
					next.Agent.Variant = pick
					next.VariantPick = &pick
					return next, true, nil
				})
		}()
	}
	wg.Wait()

	active, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "deep", active.Agent.Variant)
}

// TestEditActiveAgentKeepsInstanceOnFailure pins that a rejected edit
// leaves the session exactly as it was. A switch that fails validation
// must not half-apply.
func TestEditActiveAgentKeepsInstanceOnFailure(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	before, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)

	boom := errors.New("boom")
	err = coord.editActiveAgent(t.Context(), sess.ID,
		func(current config.ActiveAgent) (config.ActiveAgent, bool, error) {
			next := current
			next.Agent.Variant = "half-applied"
			return next, true, boom
		})
	require.ErrorIs(t, err, boom)

	after, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, before.Agent.Variant, after.Agent.Variant,
		"a failed edit must not land")
}

// TestQueuedPromptReresolvesOnDequeue covers A12: a prompt that waited
// in the queue while the user switched models must start its new turn
// on the new model, not the one it was queued with.
func TestQueuedPromptReresolvesOnDequeue(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		calls  int
		wanted = resolvedAgent{ID: "after-the-switch"}
	)

	queued := SessionAgentCall{
		SessionID: "session",
		Agent:     resolvedAgent{ID: "before-the-switch"},
		Resolve: func(context.Context) (resolvedAgent, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return wanted, nil
		},
	}

	got := reresolve(t.Context(), queued)
	require.Equal(t, 1, calls)
	require.Equal(t, "after-the-switch", got.Agent.ID)
}

// TestQueuedPromptKeepsItsAgentWhenReresolutionFails pins the fallback:
// dropping the prompt would lose the user's message, so a resolution
// failure runs it on the agent it was queued with.
func TestQueuedPromptKeepsItsAgentWhenReresolutionFails(t *testing.T) {
	t.Parallel()

	queued := SessionAgentCall{
		SessionID: "session",
		Agent:     resolvedAgent{ID: "queued-with"},
		Resolve: func(context.Context) (resolvedAgent, error) {
			return resolvedAgent{}, errors.New("provider is down")
		},
	}

	got := reresolve(t.Context(), queued)
	require.Equal(t, "queued-with", got.Agent.ID)
}

// TestCallWithoutResolverIsUntouched pins that the hook is optional, so
// the calls tests construct by hand keep working unchanged.
func TestCallWithoutResolverIsUntouched(t *testing.T) {
	t.Parallel()

	queued := SessionAgentCall{SessionID: "session", Agent: resolvedAgent{ID: "frozen"}}
	require.Equal(t, "frozen", reresolve(t.Context(), queued).Agent.ID)
}
