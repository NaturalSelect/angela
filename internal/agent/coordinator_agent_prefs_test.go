package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// newModelPrefTestCoordinator builds a coordinator like
// newGateTestCoordinator, but with distinct main/chore model IDs so
// tests can tell which one ended up as the coder agent's model. The
// coder agent is configured to prefer the chore model and carries the
// given temperature override.
func newModelPrefTestCoordinator(t *testing.T, temperature *float64) *coordinator {
	t.Helper()

	env := testEnv(t)

	angelaJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"mock": {"id": "mock", "name": "Mock", "type": "openai",
    "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
    "models": [{"id": "large-model", "name": "Large", "context_window": 8192, "max_tokens": 128},
               {"id": "small-model", "name": "Small", "context_window": 8192, "max_tokens": 128}]}},
  "slots": {"main": {"provider": "mock", "model": "large-model"},
             "chore": {"provider": "mock", "model": "small-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"), []byte(angelaJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	agentCfg := cfg.Config().Agents[config.AgentCoder]
	agentCfg.Slot = config.SlotChore
	agentCfg.Temperature = temperature
	cfg.Config().Agents[config.AgentCoder] = agentCfg

	coord := &coordinator{
		cfg:            cfg,
		sessions:       env.sessions,
		messages:       env.messages,
		permissions:    env.permissions,
		history:        env.history,
		filetracker:    *env.filetracker,
		subagents:      newSubagentRegistry(),
		branches:       newBranchController(),
		subagentRoutes: csync.NewMap[string, subagentRoute](),
	}
	coord.reconcileSubagents()
	coord.currentAgent = coord.buildAgent(agentCfg.ID, false)

	return coord
}

// resolveCoder resolves the coder agent the way a turn does.
func resolveCoder(t *testing.T, coord *coordinator) resolvedAgent {
	t.Helper()
	resolved, err := coord.resolveAgent(context.Background(), instantiate(t, coord, config.AgentCoder), 0)
	require.NoError(t, err)
	return resolved
}

// instantiate builds an agent's session instance straight from config,
// which is what a session's first turn does.
func instantiate(t *testing.T, coord *coordinator, agentID string) config.ActiveAgent {
	t.Helper()
	active, ok := coord.cfg.Config().InstantiateAgent(agentID)
	require.True(t, ok, "agent %q must be configured", agentID)
	return active
}

// TestBuildModelResolvesConfiguredName pins that an agent gets the one
// model its Model name points at, and that an unset name falls back to
// main. There is no second model to cross over to any more: whatever an
// agent names is what it runs on.
func TestBuildModelResolvesConfiguredName(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	// The coder is configured to prefer the chore model.
	chore, err := coord.buildModel(context.Background(), instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)
	require.Equal(t, "small-model", chore.ModelCfg.Model)

	// An agent that names no model at all falls back to main.
	coord.cfg.Config().Agents["unnamed"] = config.Agent{ID: "unnamed"}
	main, err := coord.buildModel(context.Background(), instantiate(t, coord, "unnamed"), false)
	require.NoError(t, err)
	require.Equal(t, "large-model", main.ModelCfg.Model)
}

// TestBuildAgentWiresTitleGenerator pins that buildAgent hands the
// session agent a title generator. Nothing else fails when that wiring
// is dropped — sessions just silently stop getting names.
func TestBuildAgentWiresTitleGenerator(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	agent, ok := coord.currentAgent.(*sessionAgent)
	require.True(t, ok)

	require.NotNil(t, agent.generateTitle,
		"buildAgent must wire a title generator onto the session agent")
}

// TestUpdateModelsRespectsAgentModelPreference reproduces H4's exact
// failure scenario: an agent configured with Model: "chore" must keep
// resolving the chore model turn after turn, not silently fall back to
// main once the coordinator refreshes.
func TestUpdateModelsRespectsAgentModelPreference(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	require.Equal(t, "small-model", resolveCoder(t, coord).Model.ModelCfg.Model,
		"sanity check: the first resolution must select the chore model")

	require.NoError(t, coord.UpdateModels(context.Background()))

	require.Equal(t, "small-model", resolveCoder(t, coord).Model.ModelCfg.Model,
		"a later turn must keep the agent's chore-model preference, not reset it to main")
}

// TestBuildAgentAppliesTemperature pins the M2 fix end to end: an
// agent's configured Temperature must show up on the resulting
// SessionAgent's model, both right after construction and after
// UpdateModels refreshes the models.
func TestBuildAgentAppliesTemperature(t *testing.T) {
	want := 0.1
	coord := newModelPrefTestCoordinator(t, &want)

	first := resolveCoder(t, coord)
	require.NotNil(t, first.Model.CatwalkCfg.Options.Temperature)
	require.Equal(t, want, *first.Model.CatwalkCfg.Options.Temperature)

	require.NoError(t, coord.UpdateModels(context.Background()))

	later := resolveCoder(t, coord)
	require.NotNil(t, later.Model.CatwalkCfg.Options.Temperature)
	require.Equal(t, want, *later.Model.CatwalkCfg.Options.Temperature,
		"every turn must keep applying the agent's Temperature override")
}

// pickModelFor sends the edit "Switch Agent Model" sends: a model for
// agentID, recorded in the session's pick table.
func pickModelFor(t *testing.T, coord *coordinator, sessionID, agentID, model string) {
	t.Helper()
	_, err := coord.EditActiveAgent(t.Context(), sessionID, config.ActiveAgentEdit{
		ForAgent: agentID,
		Model:    &config.SelectedModel{Provider: "mock", Model: model},
	})
	require.NoError(t, err)
}

// TestAgentModelPickOutranksAgentConfig pins that a "switch agent
// model" pick reaches buildModel, and still does after the session's
// instance is dropped and rebuilt from its record, which is what a
// restart does.
func TestAgentModelPickOutranksAgentConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	sessionID := newVariantSession(t, coord)

	// The coder is configured to prefer the chore (small) model.
	pickModelFor(t, coord, sessionID, config.AgentCoder, "large-model")

	for _, stage := range []string{"live", "restored"} {
		if stage == "restored" {
			coord.active.forget(sessionID)
		}
		active, err := coord.activeAgentFor(t.Context(), sessionID)
		require.NoError(t, err)
		model, err := coord.buildModel(context.Background(), active, false)
		require.NoError(t, err)
		require.Equal(t, "large-model", model.ModelCfg.Model,
			"the pick must outrank the agent's own configured slot (%s)", stage)
	}
}

// TestAgentModelPickReachesSubagentDispatch pins that a pick made for
// a sub-agent applies when the agent tool dispatches it under that
// session, not just to the session's own primary agent: resolveSubagent
// rebuilds the parent from its record and resolves the child under it.
func TestAgentModelPickReachesSubagentDispatch(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	childID := persistedChildSession(t, coord, config.AgentExplore)
	child, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)

	pickModelFor(t, coord, child.ParentSessionID, config.AgentExplore, "large-model")

	resolved, err := coord.resolveSubagent(context.Background(), config.AgentExplore, childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", resolved.Model.ModelCfg.Model,
		"a sub-agent dispatch must see the pick its session made for it")
}

// TestPickMadeInAChildSessionReachesItsNextTurn pins that a model pick
// made while viewing a child session, as a user does in a branch,
// changes what that child's next turn runs on and not only what the UI
// shows.
func TestPickMadeInAChildSessionReachesItsNextTurn(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	childID := persistedChildSession(t, coord, config.AgentExplore)

	before, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", before.resolved.Model.ModelCfg.Model,
		"test premise: explore is configured on the main model")

	_, err = coord.EditActiveAgent(t.Context(), childID, switchModelEdit("small-model"))
	require.NoError(t, err)

	after, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", after.resolved.Model.ModelCfg.Model,
		"a pick made in the child session must change the child's next turn")
}

// TestChildPickOutranksItsDispatchersPick pins the precedence between
// the two tables a child resolves from: when both name the same agent,
// the child's own entry wins.
func TestChildPickOutranksItsDispatchersPick(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	childID := persistedChildSession(t, coord, config.AgentExplore)
	child, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)

	pickModelFor(t, coord, child.ParentSessionID, config.AgentExplore, "small-model")
	// Written straight to the row: through an edit, picking the model the
	// child already shows is a no-op, and large-model is what explore
	// shows by default.
	require.NoError(t, coord.sessions.UpdateActiveAgent(t.Context(), childID, config.ActiveAgentState{
		Agent: config.AgentExplore,
		AgentModels: map[string]config.AgentModelPick{
			config.AgentExplore: {Model: config.SelectedModel{Provider: "mock", Model: "large-model"}},
		},
	}))

	resolved, err := coord.resolveSubagent(t.Context(), config.AgentExplore, childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", resolved.Model.ModelCfg.Model,
		"the child's own pick must outrank the one its dispatcher made for the same agent")
}

// TestAgentModelPickStaysInItsSession pins the scope of a pick: it is
// part of one session's record, so a second session keeps resolving the
// agent's configured model.
func TestAgentModelPickStaysInItsSession(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	picked := newVariantSession(t, coord)
	other := newVariantSession(t, coord)

	pickModelFor(t, coord, picked, config.AgentCoder, "large-model")

	otherAgent, err := coord.activeAgentFor(t.Context(), other)
	require.NoError(t, err)
	require.Equal(t, "small-model", otherAgent.Model.Model,
		"a pick made in one session must not reach another")
	require.Empty(t, otherAgent.AgentModels)
}

// TestSessionModelPickSurvivesAColdCache pins that the table in the
// session record alone is enough to rebuild the pick. materializeActiveAgent
// is what a fresh load, or a new coordinator after a restart, goes
// through.
func TestSessionModelPickSurvivesAColdCache(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	sessionID := newVariantSession(t, coord)

	// The session explicitly moves off the agent's configured default
	// (chore/small-model) onto the other model.
	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err)

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "large-model", sess.ActiveAgent.AgentModels[config.AgentCoder].Model.Model,
		"switching the running agent's model must land in the same table as switching another agent's")

	agentCfg, err := coord.materializeActiveAgent(sessionID, sess.ActiveAgent)
	require.NoError(t, err)
	require.Equal(t, "large-model", agentCfg.Model.Model)
}
