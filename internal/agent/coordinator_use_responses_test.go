package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy/providers/openai"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestUseResponsesForcesTheResponsesShape covers the case the setting
// exists for: a gateway alias that fantasy's model-ID matcher cannot
// recognize, which would otherwise be stuck on chat completions.
func TestUseResponsesForcesTheResponsesShape(t *testing.T) {
	t.Parallel()

	model := Model{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "gpt-codex-sol"}},
		ModelCfg:   config.SelectedModel{Provider: "openai"},
	}
	providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name, UseResponses: boolPtr(true)}

	opts := getProviderOptions(model, providerCfg, "")

	raw, ok := opts[openai.Name]
	require.True(t, ok)
	_, ok = raw.(*openai.ResponsesProviderOptions)
	require.True(t, ok, "use_responses=true must produce Responses options")
}

// TestUseResponsesFalseKeepsChatCompletions pins the other direction: a
// model ID that fantasy would route to Responses stays on chat
// completions when the provider says so.
func TestUseResponsesFalseKeepsChatCompletions(t *testing.T) {
	t.Parallel()

	model := Model{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "gpt-5.2"}},
		ModelCfg:   config.SelectedModel{Provider: "openai"},
	}
	providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name, UseResponses: boolPtr(false)}

	opts := getProviderOptions(model, providerCfg, "")

	raw, ok := opts[openai.Name]
	require.True(t, ok)
	_, ok = raw.(*openai.ProviderOptions)
	require.True(t, ok, "use_responses=false must produce chat completions options")
}

// TestUseResponsesUnsetLeavesTheModelIDInCharge keeps the default
// behaviour unchanged for everyone who never sets the field.
func TestUseResponsesUnsetLeavesTheModelIDInCharge(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		modelID   string
		responses bool
	}{
		"recognized model":   {modelID: "gpt-5.2", responses: true},
		"unrecognized model": {modelID: "gpt-codex-sol", responses: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			model := Model{
				CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: tc.modelID}},
				ModelCfg:   config.SelectedModel{Provider: "openai"},
			}
			providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name}

			raw, ok := getProviderOptions(model, providerCfg, "")[openai.Name]
			require.True(t, ok)
			_, isResponses := raw.(*openai.ResponsesProviderOptions)
			require.Equal(t, tc.responses, isResponses)
		})
	}
}

// TestAConfiguredEffortReachesTheRequest pins the reasoning fix: a model
// typed in by hand has no catalog entry, so CanReason is false and the
// effort used to be dropped before it ever reached the provider.
// TestAConfiguredEffortReachesTheRequest pins the reasoning fix: a
// provider's catalog entry for a model can declare reasoning support
// and an effort itself (e.g. a user-added entry for a gateway alias
// with no built-in registry match), and that effort must reach the
// request rather than being dropped.
func TestAConfiguredEffortReachesTheRequest(t *testing.T) {
	t.Parallel()

	model := Model{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{
			ID:                     "gpt-codex-sol",
			CanReason:              true,
			ReasoningLevels:        []string{"max"},
			DefaultReasoningEffort: "max",
		}},
		ModelCfg: config.SelectedModel{Provider: "openai"},
	}
	providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name}

	raw, ok := getProviderOptions(model, providerCfg, "")[openai.Name]
	require.True(t, ok)
	parsed, ok := raw.(*openai.ProviderOptions)
	require.True(t, ok)
	require.NotNil(t, parsed.ReasoningEffort)
	require.Equal(t, "max", string(*parsed.ReasoningEffort))
}

// TestAnAbsentEffortStaysAbsent keeps the fix from turning reasoning on
// for models nobody asked to reason.
func TestAnAbsentEffortStaysAbsent(t *testing.T) {
	t.Parallel()

	model := Model{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ID: "gpt-codex-sol"}},
		ModelCfg:   config.SelectedModel{Provider: "openai"},
	}
	providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name}

	raw, ok := getProviderOptions(model, providerCfg, "")[openai.Name]
	require.True(t, ok)
	parsed, ok := raw.(*openai.ProviderOptions)
	require.True(t, ok)
	require.Nil(t, parsed.ReasoningEffort)
}
