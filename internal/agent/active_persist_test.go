package agent

import (
	"context"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

// editActive applies an edit and keeps only its error, for the tests
// that assert on what was persisted rather than on what came back.
func editActive(t *testing.T, coord *coordinator, sessionID string, edit config.ActiveAgentEdit) error {
	t.Helper()
	_, err := coord.EditActiveAgent(t.Context(), sessionID, edit)
	return err
}

// TestActiveAgentSurvivesARestart is the core assertion of persisting
// the instance: the model the user picked for this session is still
// there after the in-memory store is gone.
func TestActiveAgentSurvivesARestart(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))

	// Drop everything held in memory, as a restart would.
	coord.active.forget(sess.ID)

	restored, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, restored.Agent.ID)
	require.Equal(t, "large-model", restored.Model.Model)
}

// TestRestartTakesDefinitionsFromConfigAndModelFromTheSession pins the
// split that makes persisting safe. A session must not carry a frozen
// copy of its prompt and tools across a restart, or editing the config
// would stop reaching sessions that already exist.
func TestRestartTakesDefinitionsFromConfigAndModelFromTheSession(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))

	// The user edits the agent's prompt in the config while the session
	// is not loaded, then comes back to it.
	coord.active.forget(sess.ID)
	cfg := coord.cfg.Config()
	reviewer := cfg.Agents[testReviewerAgent]
	reviewer.Prompt = "You are new."
	cfg.Agents[testReviewerAgent] = reviewer

	restored, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "You are new.", restored.Agent.Prompt,
		"the definition must come from the config as it stands now")
	require.Equal(t, "large-model", restored.Model.Model,
		"the model must come from the session's own record")
}

// TestVariantSurvivesARestart covers the preset specifically: it lives
// on the agent definition, which is re-read from config, so it only
// persists if the state carries it explicitly.
func TestVariantSurvivesARestart(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchVariant(t.Context(), sess.ID, "deep"))

	coord.active.forget(sess.ID)

	restored, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "deep", restored.Agent.Variant)
}

// TestClearingTheVariantSurvivesARestart is the other half: backing out
// of a preset is an edit too, and a restart must not resurrect it.
func TestClearingTheVariantSurvivesARestart(t *testing.T) {
	coord := newVariantTestCoordinator(t)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchVariant(t.Context(), sess.ID, "deep"))
	require.NoError(t, coord.SwitchVariant(t.Context(), sess.ID, ""))

	coord.active.forget(sess.ID)

	restored, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, restored.Agent.Variant)
}

// TestAnUnpickedVariantKeepsFollowingTheConfig is the A1 regression.
// Agent.Variant also carries config-derived defaults, so persisting it
// on an edit that never touched the preset would freeze it — the user
// would then change the default in the config and find that sessions
// which merely switched model never picked it up.
func TestAnUnpickedVariantKeepsFollowingTheConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
		"high": {MaxTokens: ptrTo(int64(16000))},
	})
	setCoderVariant(t, coord, "high")

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// An edit that is not about the preset at all.
	require.NoError(t, editActive(t, coord, sess.ID, config.ActiveAgentEdit{
		Think: ptrTo(true),
	}))

	record, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Nil(t, record.ActiveAgent.Variant,
		"an edit that touched no preset must record no preset pick")

	// The user now changes the configured default and comes back.
	setCoderVariant(t, coord, "deep")
	coord.active.forget(sess.ID)

	restored, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "deep", restored.Agent.Variant,
		"a preset the user never picked must follow the config")
}

// TestAPickedVariantOutranksTheConfig is the converse, and the reason
// the pick is recorded separately at all.
func TestAPickedVariantOutranksTheConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
		"high": {MaxTokens: ptrTo(int64(16000))},
	})
	setCoderVariant(t, coord, "high")

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchVariant(t.Context(), sess.ID, "deep"))

	coord.active.forget(sess.ID)

	restored, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "deep", restored.Agent.Variant,
		"a preset the user picked must survive a config change")
}

// TestAFreshSessionKeepsFollowingTheConfiguredModel pins that reading a
// session does not pin its model. A session that never chose anything
// must pick up a change to the configured default.
func TestAFreshSessionKeepsFollowingTheConfiguredModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	before, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "small-model", before.Model.Model)

	cfg := coord.cfg.Config()
	cfg.Models[config.ModelChore] = config.SelectedModel{Provider: "mock", Model: "large-model"}

	after, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "large-model", after.Model.Model,
		"a session that never picked a model must follow the config")
}

// TestEditingAModelStopsFollowingTheConfig is the converse: once the
// user picks, the config no longer overrides them.
func TestEditingAModelStopsFollowingTheConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	picked := config.SelectedModel{Provider: "mock", Model: "large-model"}
	require.NoError(t, editActive(t, coord, sess.ID, config.ActiveAgentEdit{
		ModelName: config.ModelChore,
		Model:     &picked,
	}))

	cfg := coord.cfg.Config()
	cfg.Models[config.ModelChore] = config.SelectedModel{Provider: "mock", Model: "small-model"}

	active, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "large-model", active.Model.Model,
		"a model the user picked must survive a config change")
}

// TestEditKeepsTheAgentsSlotWhenTheCallerOmitsIt covers an API client
// that sends only a provider and model. The slot is what
// InstantiateFor matches on to decide whether an internal agent
// inherits this model, so losing it would quietly send compaction back
// to the global default.
func TestEditKeepsTheAgentsSlotWhenTheCallerOmitsIt(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	before, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)

	picked := config.SelectedModel{Provider: "mock", Model: "large-model"}
	require.NoError(t, editActive(t, coord, sess.ID, config.ActiveAgentEdit{
		Model: &picked,
	}))

	active, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "large-model", active.Model.Model)
	require.Equal(t, before.ModelName, active.ModelName,
		"an omitted slot must keep the one the agent runs on")
}

// TestEditRejectsASlotTheAgentDoesNotRunOn is the other half: a caller
// that names the wrong slot is reporting a bug, and taking the name
// would break the same inheritance silently.
func TestEditRejectsASlotTheAgentDoesNotRunOn(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	before, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, config.ModelChore, before.ModelName,
		"this coordinator's agent is expected to run on the chore slot")

	picked := config.SelectedModel{Provider: "mock", Model: "large-model"}
	err = editActive(t, coord, sess.ID, config.ActiveAgentEdit{
		ModelName: config.ModelMain,
		Model:     &picked,
	})
	require.ErrorIs(t, err, ErrModelSlotMismatch)

	active, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, before.Model.Model, active.Model.Model,
		"a rejected edit must leave the instance alone")
	require.Equal(t, config.ModelChore, active.ModelName)
}

// TestEditActiveAgentMovesEverythingAtOnce covers the combined edit the
// merged route exists for.
func TestEditActiveAgentMovesEverythingAtOnce(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	think := true
	require.NoError(t, editActive(t, coord, sess.ID, config.ActiveAgentEdit{
		Agent: testReviewerAgent,
		Think: &think,
	}))

	active, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, active.Agent.ID)
	require.True(t, active.Model.Think)

	require.False(t, coord.cfg.Config().Models[config.ModelMain].Think,
		"the thinking flag must land on the session, never on the global config")
}

// TestAZeroEditChangesNothing keeps an empty request from writing a
// trail message or touching the record.
func TestAZeroEditChangesNothing(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, editActive(t, coord, sess.ID, config.ActiveAgentEdit{}))

	msgs, err := coord.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, msgs)
}

// TestEditActiveAgentRejectsSubagents keeps a session from being
// pointed at an agent that only exists to be dispatched.
func TestEditActiveAgentRejectsSubagents(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	cfg := coord.cfg.Config()
	cfg.Agents["helper"] = config.Agent{
		ID:    "helper",
		Mode:  config.AgentModeSubagent,
		Model: config.ModelChore,
	}

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	err = editActive(t, coord, sess.ID, config.ActiveAgentEdit{Agent: "helper"})
	require.ErrorIs(t, err, ErrAgentNotAvailable)
}

// TestEditingADeletedSessionFails pins half of A3: the write matched no
// row and reported success anyway, so the caller was told its edit
// landed while nothing was persisted.
func TestEditingADeletedSessionFails(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))
	require.NoError(t, coord.sessions.Delete(t.Context(), sess.ID))

	err = coord.SwitchAgent(t.Context(), sess.ID, config.AgentCoder)
	require.ErrorIs(t, err, session.ErrSessionNotFound)

	_, err = coord.activeAgentFor(t.Context(), sess.ID)
	require.Error(t, err, "the rejected write must not leave the session cached")
}

// TestDeletingASessionForgetsItsAgent pins the other half: nothing
// evicted the cache, so a session that had been read before it was
// deleted went on being answered for from memory — a ghost agent on a
// row that no longer exists, and on a long-lived server an entry that
// is never reclaimed.
func TestDeletingASessionForgetsItsAgent(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go coord.forgetDeletedSessions(ctx, coord.sessions.Subscribe(ctx))

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))

	cached, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, cached.Agent.ID, "precondition: the session is cached")

	require.NoError(t, coord.sessions.Delete(t.Context(), sess.ID))

	require.Eventually(t, func() bool {
		_, err := coord.activeAgentFor(t.Context(), sess.ID)
		return err != nil
	}, 2*time.Second, 5*time.Millisecond,
		"a deleted session must stop being answered for")
}
