package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
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
  "models": {"main": {"provider": "mock", "model": "large-model"},
             "chore": {"provider": "mock", "model": "small-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"), []byte(angelaJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	agentCfg := cfg.Config().Agents[config.AgentCoder]
	agentCfg.Model = config.ModelChore
	agentCfg.Temperature = temperature
	cfg.Config().Agents[config.AgentCoder] = agentCfg

	coord := &coordinator{
		cfg:         cfg,
		sessions:    env.sessions,
		messages:    env.messages,
		permissions: env.permissions,
		history:     env.history,
		filetracker: *env.filetracker,
		subagents:   newSubagentRegistry(),
	}
	coord.reconcileSubagents()
	coord.currentAgent = coord.buildAgent(agentCfg.ID, false)

	return coord
}

// resolveCoder resolves the coder agent the way a turn does.
func resolveCoder(t *testing.T, coord *coordinator) resolvedAgent {
	t.Helper()
	resolved, err := coord.resolveAgent(context.Background(),
		coord.cfg.Config().Agents[config.AgentCoder], false)
	require.NoError(t, err)
	return resolved
}

// TestBuildModelResolvesConfiguredName pins that an agent gets the one
// model its Model name points at, and that an unset name falls back to
// main. There is no second model to cross over to any more: whatever an
// agent names is what it runs on.
func TestBuildModelResolvesConfiguredName(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	chore, err := coord.buildModel(context.Background(),
		config.Agent{Model: config.ModelChore}, false)
	require.NoError(t, err)
	require.Equal(t, "small-model", chore.ModelCfg.Model)

	main, err := coord.buildModel(context.Background(), config.Agent{}, false)
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
	require.NotNil(t, first.Model.ModelCfg.Temperature)
	require.Equal(t, want, *first.Model.ModelCfg.Temperature)

	require.NoError(t, coord.UpdateModels(context.Background()))

	later := resolveCoder(t, coord)
	require.NotNil(t, later.Model.ModelCfg.Temperature)
	require.Equal(t, want, *later.Model.ModelCfg.Temperature,
		"every turn must keep applying the agent's Temperature override")
}
