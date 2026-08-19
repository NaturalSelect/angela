package agent

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// setChoreVariants declares variants on the chore model config, which
// newModelPrefTestCoordinator points the coder agent at.
func setChoreVariants(t *testing.T, coord *coordinator, variants map[string]config.SelectedModelOverride) {
	t.Helper()
	cfg := coord.cfg.Config()
	modelCfg := cfg.Models[config.ModelChore]
	modelCfg.Variants = variants
	cfg.Models[config.ModelChore] = modelCfg
}

// setCoderVariant points the coder agent at a variant.
func setCoderVariant(t *testing.T, coord *coordinator, variant string) {
	t.Helper()
	cfg := coord.cfg.Config()
	agentCfg := cfg.Agents[config.AgentCoder]
	agentCfg.Variant = variant
	cfg.Agents[config.AgentCoder] = agentCfg
}

func ptrTo[T any](v T) *T { return &v }

// TestAgentVariantReachesTheResolvedModel pins the wiring: declaring a
// variant in config does nothing unless the agent's Variant field
// actually selects it during resolution.
func TestAgentVariantReachesTheResolvedModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000)), ReasoningEffort: ptrTo("high")},
	})
	setCoderVariant(t, coord, "deep")

	model, err := coord.buildModel(context.Background(),
		instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)

	require.Equal(t, "deep", model.Variant)
	require.Equal(t, int64(32000), model.ModelCfg.MaxTokens)
	require.Equal(t, "high", model.ModelCfg.ReasoningEffort)
	require.Equal(t, "small-model", model.ModelCfg.Model,
		"a variant keeps the model identity")
}

// TestUnknownAgentVariantStillResolves pins the loose half of the
// validation rule at the layer that matters: a turn must survive a
// variant name that no longer exists.
func TestUnknownAgentVariantStillResolves(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setCoderVariant(t, coord, "vanished")

	model, err := coord.buildModel(context.Background(),
		instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)

	require.Empty(t, model.Variant, "no variant was actually applied")
	require.Equal(t, "small-model", model.ModelCfg.Model)
}
