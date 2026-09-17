package agent

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// setChoreVariants declares variants on the chore slot's catalog model
// entry, which newModelPrefTestCoordinator points the coder agent at.
func setChoreVariants(t *testing.T, coord *coordinator, variants map[string]config.SelectedModelOverride) {
	t.Helper()
	cfg := coord.cfg.Config()
	slot := cfg.Slots[config.SlotChore]
	providerCfg, ok := cfg.Providers.Get(slot.Provider)
	require.True(t, ok)
	for i, m := range providerCfg.Models {
		if m.ID == slot.Model {
			providerCfg.Models[i].Variants = variants
		}
	}
	cfg.Providers.Set(slot.Provider, providerCfg)
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
	require.Equal(t, int64(32000), model.CatwalkCfg.DefaultMaxTokens)
	require.Equal(t, "high", model.CatwalkCfg.DefaultReasoningEffort)
	require.Equal(t, "small-model", model.ModelCfg.Model,
		"a variant keeps the model identity")
}

// setChoreSlotVariant points the chore slot's own SelectedModel at a
// default variant, the fallback an agent with no Variant of its own
// picks up.
func setChoreSlotVariant(t *testing.T, coord *coordinator, variant string) {
	t.Helper()
	cfg := coord.cfg.Config()
	slot := cfg.Slots[config.SlotChore]
	slot.Variant = variant
	cfg.Slots[config.SlotChore] = slot
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

// TestSlotVariantAppliesWhenAgentVariantIsUnset pins the new
// fallback: a slot's own Variant reaches the resolved model when the
// agent running on it names none of its own.
func TestSlotVariantAppliesWhenAgentVariantIsUnset(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000)), ReasoningEffort: ptrTo("high")},
	})
	setChoreSlotVariant(t, coord, "deep")

	model, err := coord.buildModel(context.Background(),
		instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)

	require.Equal(t, "deep", model.Variant)
	require.Equal(t, int64(32000), model.CatwalkCfg.DefaultMaxTokens)
	require.Equal(t, "small-model", model.ModelCfg.Model,
		"a variant keeps the model identity")
}

// TestAgentVariantOutranksSlotVariant pins the priority order: when
// both the agent and its slot name a variant, the agent's own wins.
func TestAgentVariantOutranksSlotVariant(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
		"high": {MaxTokens: ptrTo(int64(16000))},
	})
	setChoreSlotVariant(t, coord, "deep")
	setCoderVariant(t, coord, "high")

	model, err := coord.buildModel(context.Background(),
		instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)

	require.Equal(t, "high", model.Variant,
		"the agent's own variant must outrank the slot's default")
	require.Equal(t, int64(16000), model.CatwalkCfg.DefaultMaxTokens)
}
