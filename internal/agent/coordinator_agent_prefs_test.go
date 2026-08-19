package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestPrimaryModelType pins the model-slot decision on its own: an
// agent configured with Model: "small" runs on the small slot, anything
// else (including unset) on the large one.
func TestPrimaryModelType(t *testing.T) {
	t.Parallel()

	require.Equal(t, config.SelectedModelTypeLarge, primaryModelType(config.Agent{}))
	require.Equal(t, config.SelectedModelTypeLarge,
		primaryModelType(config.Agent{Model: config.SelectedModelTypeLarge}))
	require.Equal(t, config.SelectedModelTypeSmall,
		primaryModelType(config.Agent{Model: config.SelectedModelTypeSmall}))
}

// newModelPrefTestCoordinator builds a coordinator like
// newGateTestCoordinator, but with distinct large/small model IDs so
// tests can tell which one ended up as the coder agent's primary model.
// The coder agent is configured to prefer the small model and carries
// the given temperature override.
func newModelPrefTestCoordinator(t *testing.T, temperature *float64) *coordinator {
	t.Helper()

	env := testEnv(t)

	angelaJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"mock": {"id": "mock", "name": "Mock", "type": "openai",
    "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
    "models": [{"id": "large-model", "name": "Large", "context_window": 8192, "default_max_tokens": 128},
               {"id": "small-model", "name": "Small", "context_window": 8192, "default_max_tokens": 128}]}},
  "models": {"large": {"provider": "mock", "model": "large-model"},
             "small": {"provider": "mock", "model": "small-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"), []byte(angelaJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	agentCfg := cfg.Config().Agents[config.AgentCoder]
	agentCfg.Model = config.SelectedModelTypeSmall
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

	p, err := coderPrompt(prompt.WithWorkingDir(env.workingDir))
	require.NoError(t, err)

	agent, err := coord.buildAgent(context.Background(), p, agentCfg, false)
	require.NoError(t, err)
	coord.currentAgent = agent

	return coord
}

// TestBuildAgentModelsCrossesSecondary pins the pairing: an agent on the
// small slot must get the large model as its secondary, not a second
// copy of small. The secondary is what title generation and
// summarization run on, so collapsing the two would silently move that
// work onto the wrong model.
func TestBuildAgentModelsCrossesSecondary(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	primary, secondary, err := coord.buildAgentModels(context.Background(),
		config.Agent{Model: config.SelectedModelTypeSmall}, false)
	require.NoError(t, err)
	require.Equal(t, "small-model", primary.ModelCfg.Model)
	require.Equal(t, "large-model", secondary.ModelCfg.Model)

	primary, secondary, err = coord.buildAgentModels(context.Background(), config.Agent{}, false)
	require.NoError(t, err)
	require.Equal(t, "large-model", primary.ModelCfg.Model)
	require.Equal(t, "small-model", secondary.ModelCfg.Model)
}

// TestBuildAgentUsesSecondaryModel asserts the production path hands the
// resolved secondary to the SessionAgent. The previous test only checked
// what the helper returned, so it stayed green while buildAgent threw
// the secondary away and passed the small model in every case — for a
// small-preferring agent that left both slots on the same model.
func TestBuildAgentUsesSecondaryModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	agent, ok := coord.currentAgent.(*sessionAgent)
	require.True(t, ok)

	require.Equal(t, "small-model", agent.Model().ModelCfg.Model)
	require.Equal(t, "large-model", agent.smallModel.Get().ModelCfg.Model,
		"a small-preferring agent's secondary must be the large model")

	require.NoError(t, coord.UpdateModels(context.Background()))

	require.Equal(t, "small-model", agent.Model().ModelCfg.Model)
	require.Equal(t, "large-model", agent.smallModel.Get().ModelCfg.Model,
		"UpdateModels must preserve the crossed pairing")
}

// TestUpdateModelsRespectsAgentModelPreference reproduces H4's exact
// failure scenario: an agent configured with Model: "small" must keep
// resolving the small model as primary after UpdateModels refreshes the
// coordinator's models, not silently fall back to the large model.
func TestUpdateModelsRespectsAgentModelPreference(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	require.Equal(t, "small-model", coord.currentAgent.Model().ModelCfg.Model,
		"sanity check: buildAgent must select the small model as primary")

	require.NoError(t, coord.UpdateModels(context.Background()))

	require.Equal(t, "small-model", coord.currentAgent.Model().ModelCfg.Model,
		"UpdateModels must keep the agent's small-model preference, not reset it to large")
}

// TestBuildAgentAppliesTemperature pins the M2 fix end to end: an
// agent's configured Temperature must show up on the resulting
// SessionAgent's primary model, both right after construction and
// after UpdateModels refreshes the models.
func TestBuildAgentAppliesTemperature(t *testing.T) {
	want := 0.1
	coord := newModelPrefTestCoordinator(t, &want)

	require.NotNil(t, coord.currentAgent.Model().ModelCfg.Temperature)
	require.Equal(t, want, *coord.currentAgent.Model().ModelCfg.Temperature)

	require.NoError(t, coord.UpdateModels(context.Background()))

	require.NotNil(t, coord.currentAgent.Model().ModelCfg.Temperature)
	require.Equal(t, want, *coord.currentAgent.Model().ModelCfg.Temperature,
		"UpdateModels must keep applying the agent's Temperature override")
}
