package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAgentModelOverrideAcceptsKnownAgentModelAndVariant(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	err := cfg.ValidateAgentModelOverride("coder", SelectedModel{Provider: "anthropic", Model: "claude", Variant: "careful"})
	require.NoError(t, err)
}

func TestValidateAgentModelOverrideAcceptsEmptyVariantAsBaseline(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	err := cfg.ValidateAgentModelOverride("coder", SelectedModel{Provider: "groq", Model: "llama"})
	require.NoError(t, err)
}

func TestValidateAgentModelOverrideRejectsUnknownAgent(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	err := cfg.ValidateAgentModelOverride("ghost", SelectedModel{Provider: "anthropic", Model: "claude"})
	require.ErrorIs(t, err, ErrAgentOverrideUnknownAgent)
}

func TestValidateAgentModelOverrideRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	err := cfg.ValidateAgentModelOverride("coder", SelectedModel{Provider: "anthropic", Model: "does-not-exist"})
	require.ErrorIs(t, err, ErrAgentOverrideUnknownModel)
}

// TestValidateAgentModelOverrideRejectsDisabledProvider pins that a
// disabled provider is rejected even though its models are still
// listed in the catalog, matching IsModelAvailable's stricter check.
func TestValidateAgentModelOverrideRejectsDisabledProvider(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	provider, ok := cfg.Providers.Get("groq")
	require.True(t, ok)
	provider.Disable = true
	cfg.Providers.Set("groq", provider)

	err := cfg.ValidateAgentModelOverride("coder", SelectedModel{Provider: "groq", Model: "llama"})
	require.ErrorIs(t, err, ErrAgentOverrideUnknownModel)
}

func TestValidateAgentModelOverrideRejectsUnknownVariant(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	err := cfg.ValidateAgentModelOverride("coder", SelectedModel{Provider: "anthropic", Model: "claude", Variant: "ghost"})
	require.ErrorIs(t, err, ErrAgentOverrideUnknownVariant)
}

// TestApplyPicksSkipsAPickThatNoLongerResolves pins the reload
// behavior: a pick whose model left the catalog must not brick the
// agent, so it falls back to the configured model, but the entry stays
// in the table so it takes effect again once the model comes back.
func TestApplyPicksSkipsAPickThatNoLongerResolves(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)

	variant := "careful"
	picks := map[string]AgentModelPick{
		"coder": {Model: SelectedModel{Provider: "ghost", Model: "gone"}, Variant: &variant},
	}
	applied, pinned := cfg.ApplyPicks(active, picks)

	require.False(t, pinned)
	require.Equal(t, "claude", applied.Model.Model, "the agent must fall back to its configured model")
	require.Nil(t, applied.ModelPick)
	require.Nil(t, applied.VariantPick, "a preset only means something on the model it was chosen for")
	require.Equal(t, picks, applied.AgentModels, "the entry must stay in the table")
}

// TestApplyPicksAppliesAPresetOnlyPick pins that a pick naming only a
// preset moves the preset and leaves the model to the config, and is
// not reported as a pinned model.
func TestApplyPicksAppliesAPresetOnlyPick(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)

	variant := "careful"
	applied, pinned := cfg.ApplyPicks(active, map[string]AgentModelPick{"coder": {Variant: &variant}})

	require.False(t, pinned, "no model was picked, so inheritance must still apply")
	require.Equal(t, "claude", applied.Model.Model)
	require.Nil(t, applied.ModelPick)
	require.Equal(t, "careful", applied.EffectiveVariant())
}

// TestActiveAgentStateReadsTheLayoutBeforePicksWereKeyedByAgent pins
// the upgrade path: a row written when the running agent's model and
// preset sat at the top level decodes into that agent's entry, and is
// written back in the current layout only.
func TestActiveAgentStateReadsTheLayoutBeforePicksWereKeyedByAgent(t *testing.T) {
	t.Parallel()

	legacy := `{"agent":"reviewer","model":{"model":"claude","provider":"anthropic"},"variant":"careful","think":true}`

	var state ActiveAgentState
	require.NoError(t, json.Unmarshal([]byte(legacy), &state))

	variant := "careful"
	think := true
	require.Equal(t, ActiveAgentState{
		Agent: "reviewer",
		AgentModels: map[string]AgentModelPick{
			"reviewer": {Model: SelectedModel{Provider: "anthropic", Model: "claude"}, Variant: &variant},
		},
		Think: &think,
	}, state)

	written, err := json.Marshal(state)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"agent": "reviewer",
		"agent_models": {"reviewer": {"model": {"model": "claude", "provider": "anthropic"}, "variant": "careful"}},
		"think": true
	}`, string(written))
}

// TestActiveAgentStateKeepsTheNewLayoutEntryOverTheLegacyFields pins
// that when a row somehow carries both layouts, the keyed entry wins.
func TestActiveAgentStateKeepsTheNewLayoutEntryOverTheLegacyFields(t *testing.T) {
	t.Parallel()

	both := `{
		"agent": "reviewer",
		"model": {"model": "old", "provider": "old"},
		"agent_models": {"reviewer": {"model": {"model": "claude", "provider": "anthropic"}}}
	}`

	var state ActiveAgentState
	require.NoError(t, json.Unmarshal([]byte(both), &state))
	require.Equal(t, "claude", state.AgentModels["reviewer"].Model.Model)
}
