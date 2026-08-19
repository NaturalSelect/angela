package config

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvalidVariantsAreDroppedBeforeUse(t *testing.T) {
	t.Parallel()

	ptr := func(f float64) *float64 { return &f }
	tokens := func(n int64) *int64 { return &n }

	tests := []struct {
		name     string
		override SelectedModelOverride
		keep     bool
	}{
		{"NaN temperature", SelectedModelOverride{Temperature: ptr(math.NaN())}, false},
		{"infinite temperature", SelectedModelOverride{Temperature: ptr(math.Inf(1))}, false},
		{"temperature above one", SelectedModelOverride{Temperature: ptr(1.5)}, false},
		{"negative temperature", SelectedModelOverride{Temperature: ptr(-0.1)}, false},
		{"top_p above one", SelectedModelOverride{TopP: ptr(2)}, false},
		{"NaN top_p", SelectedModelOverride{TopP: ptr(math.NaN())}, false},
		{"NaN frequency penalty", SelectedModelOverride{FrequencyPenalty: ptr(math.NaN())}, false},
		{"infinite presence penalty", SelectedModelOverride{PresencePenalty: ptr(math.Inf(-1))}, false},
		{"negative max tokens", SelectedModelOverride{MaxTokens: tokens(-1)}, false},

		{"temperature at zero", SelectedModelOverride{Temperature: ptr(0)}, true},
		{"temperature at one", SelectedModelOverride{Temperature: ptr(1)}, true},
		{"large negative penalty", SelectedModelOverride{FrequencyPenalty: ptr(-2)}, true},
		{"zero max tokens", SelectedModelOverride{MaxTokens: tokens(0)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{
				Options: &Options{},
				Models: map[ModelConfigName]SelectedModel{
					ModelMain: {
						Provider: "anthropic",
						Model:    "claude",
						Variants: map[string]SelectedModelOverride{"custom": tt.override},
					},
				},
			}

			dropInvalidVariants(cfg)

			_, present := cfg.Models[ModelMain].Variants["custom"]
			require.Equal(t, tt.keep, present)

			// A dropped variant must not be offered anywhere, or the
			// UI lets a user select a name that cannot be applied.
			names := cfg.Models[ModelMain].VariantNames(nil)
			require.Equal(t, tt.keep, len(names) == 1)
		})
	}
}

func TestValidVariantsSurviveConfigPreparation(t *testing.T) {
	t.Parallel()

	bad := math.NaN()
	good := 0.4

	cfg := &Config{
		Options: &Options{},
		Models: map[ModelConfigName]SelectedModel{
			ModelMain: {
				Provider: "anthropic",
				Model:    "claude",
				Variants: map[string]SelectedModelOverride{
					"careful": {Temperature: &good},
					"broken":  {Temperature: &bad},
				},
			},
		},
	}

	prepareResolvedConfig(cfg)

	require.Equal(t, []string{"careful"}, cfg.Models[ModelMain].VariantNames(nil))

	// The surviving variant still applies its parameters.
	applied, ok := cfg.Models[ModelMain].WithVariant("careful", nil)
	require.True(t, ok)
	require.InDelta(t, 0.4, *applied.Temperature, 0.0001)

	// The dropped one degrades to the baseline instead of poisoning it.
	baseline, ok := cfg.Models[ModelMain].WithVariant("broken", nil)
	require.False(t, ok)
	require.Nil(t, baseline.Temperature)
}
