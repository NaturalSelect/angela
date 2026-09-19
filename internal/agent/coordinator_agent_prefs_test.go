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
    "models": [{"id": "large-model", "name": "Large", "context_window": 8192, "default_max_tokens": 128},
               {"id": "small-model", "name": "Small", "context_window": 8192, "default_max_tokens": 128}]}},
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

// TestAgentModelOverrideOutranksAgentConfig pins that a "switch agent
// model" override reaches buildModel, not just InstantiateAgent in
// isolation: the override must win over the agent's own configured
// slot the same way it does at the config layer.
func TestAgentModelOverrideOutranksAgentConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	// The coder is configured to prefer the chore (small) model.
	require.NoError(t, coord.cfg.SetAgentModelOverride(config.AgentCoder,
		config.SelectedModel{Provider: "mock", Model: "large-model"}))

	model, err := coord.buildModel(context.Background(), instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)
	require.Equal(t, "large-model", model.ModelCfg.Model,
		"the override must outrank the agent's own configured slot")
}

// TestAgentModelOverrideReachesSubagentDispatch pins that the override
// applies to a sub-agent resolved through the agent tool's dispatch
// path, not just a coordinator's own primary agent: resolveSubagent
// calls the same InstantiateAgent a primary turn does, so it must see
// the same override.
func TestAgentModelOverrideReachesSubagentDispatch(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	require.NoError(t, coord.cfg.SetAgentModelOverride(config.AgentExplore,
		config.SelectedModel{Provider: "mock", Model: "large-model"}))

	resolved, err := coord.resolveSubagent(context.Background(), config.AgentExplore, "")
	require.NoError(t, err)
	require.Equal(t, "large-model", resolved.Model.ModelCfg.Model,
		"a sub-agent dispatch must see the same override a primary agent's turn does")
}

// TestSessionModelPickOutranksLaterAgentModelOverride pins the other
// half: a session that already recorded its own model pick keeps it
// even after a process-level override is set on that same agent
// afterward. materializeActiveAgent is exercised directly because it
// is what rebuilds a session's instance from its persisted delta on a
// cold cache — a fresh load, or a new coordinator after a restart —
// which is exactly when a stale override could leak in.
func TestSessionModelPickOutranksLaterAgentModelOverride(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	sessionID := newVariantSession(t, coord)

	// The session explicitly moves off the agent's configured default
	// (chore/small-model) onto the other model.
	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err)

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)

	// A process-level override set afterward targets the model the
	// session just moved away from.
	require.NoError(t, coord.cfg.SetAgentModelOverride(config.AgentCoder,
		config.SelectedModel{Provider: "mock", Model: "small-model"}))

	agentCfg, err := coord.materializeActiveAgent(sessionID, sess.ActiveAgent)
	require.NoError(t, err)
	require.Equal(t, "large-model", agentCfg.Model.Model,
		"a session's own persisted pick must outrank a process-level override set afterward")
}
