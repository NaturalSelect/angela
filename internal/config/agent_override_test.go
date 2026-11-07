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

// TestInstantiateLayeredSkipsAPickThatNoLongerResolves pins the reload
// behavior: a pick whose model left the catalog must not brick the
// agent, so it falls back to the configured model, but the entry stays
// in the table so it takes effect again once the model comes back.
func TestInstantiateLayeredSkipsAPickThatNoLongerResolves(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	variant := "careful"
	picks := map[string]AgentModelPick{
		"coder": {Model: SelectedModel{Provider: "ghost", Model: "gone"}, Variant: &variant},
	}
	applied, ok := cfg.InstantiateLayered("coder", AgentModelPick{}, picks, ActiveAgent{})
	require.True(t, ok)

	require.Equal(t, "claude", applied.Model.Model, "the agent must fall back to its configured model")
	require.Nil(t, applied.ModelPick)
	require.Nil(t, applied.VariantPick, "a preset only means something on the model it was chosen for")
	require.Equal(t, picks, applied.AgentPicks, "the entry must stay in the table")
}

// TestInstantiateLayeredAppliesAPresetOnlyPick pins that a pick naming
// only a preset moves the preset and leaves the model to the layers
// below it.
func TestInstantiateLayeredAppliesAPresetOnlyPick(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	variant := "careful"
	applied, ok := cfg.InstantiateLayered("coder", AgentModelPick{}, map[string]AgentModelPick{"coder": {Variant: &variant}}, ActiveAgent{})
	require.True(t, ok)

	require.Equal(t, "claude", applied.Model.Model)
	require.Nil(t, applied.ModelPick)
	require.Equal(t, "careful", applied.EffectiveVariant())
}

// TestRowsFromBeforeTheModelStackDecodeToNoPicks pins the upgrade rule:
// the keys earlier layouts wrote are dropped, not migrated, so such a
// row loads with its agent and thinking flag and starts on the config's
// models in both pick layers.
func TestRowsFromBeforeTheModelStackDecodeToNoPicks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  string
	}{
		{
			name: "top-level model and preset",
			row:  `{"agent":"reviewer","model":{"model":"claude","provider":"anthropic"},"variant":"careful","think":true}`,
		},
		{
			name: "per-agent table",
			row:  `{"agent":"reviewer","agent_models":{"reviewer":{"model":{"model":"claude","provider":"anthropic"}}},"think":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var state ActiveAgentState
			require.NoError(t, json.Unmarshal([]byte(tt.row), &state))

			think := true
			require.Equal(t, ActiveAgentState{Agent: "reviewer", Think: &think}, state)
			require.True(t, state.SessionModel.IsZero())
			require.Empty(t, state.AgentPicks)
		})
	}
}

// TestActiveAgentStateWritesBothPickLayersUnderTheirOwnKeys pins the
// wire format, and that an empty session model is left out entirely.
func TestActiveAgentStateWritesBothPickLayersUnderTheirOwnKeys(t *testing.T) {
	t.Parallel()

	variant := "careful"
	written, err := json.Marshal(ActiveAgentState{
		Agent:        "reviewer",
		SessionModel: AgentModelPick{Model: SelectedModel{Provider: "anthropic", Model: "claude"}, Variant: &variant},
		AgentPicks: map[string]AgentModelPick{
			"scout": {Model: SelectedModel{Provider: "groq", Model: "llama"}},
		},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"agent": "reviewer",
		"session_model": {"model": {"model": "claude", "provider": "anthropic"}, "variant": "careful"},
		"agent_picks": {"scout": {"model": {"model": "llama", "provider": "groq"}}}
	}`, string(written))

	written, err = json.Marshal(ActiveAgentState{Agent: "reviewer"})
	require.NoError(t, err)
	require.JSONEq(t, `{"agent": "reviewer"}`, string(written))
}
