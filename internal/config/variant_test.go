package config

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// TestVariantOverridesOnlyTheKeysItNames pins the shallow-merge rule
// that makes variants worth having: a preset states its differences,
// not a whole model config. If unnamed keys did not survive, every
// variant would have to restate the baseline and the N+M saving over
// separate model configs would vanish.
func TestVariantOverridesOnlyTheKeysItNames(t *testing.T) {
	t.Parallel()

	base := SelectedModel{
		Provider:        "anthropic",
		Model:           "claude-opus-4",
		ReasoningEffort: "medium",
		Think:           true,
		MaxTokens:       8000,
		Temperature:     ptr(0.7),
		TopP:            ptr(0.9),
		ProviderOptions: map[string]any{"beta": "on", "cache": "yes"},
		Variants: map[string]SelectedModelOverride{
			"deep": {
				ReasoningEffort: ptr("high"),
				MaxTokens:       ptr(int64(32000)),
				ProviderOptions: map[string]any{"beta": "off"},
			},
		},
	}

	got, ok := base.WithVariant("deep", nil)
	require.True(t, ok)

	// Named keys are overridden.
	require.Equal(t, "high", got.ReasoningEffort)
	require.Equal(t, int64(32000), got.MaxTokens)
	require.Equal(t, "off", got.ProviderOptions["beta"])

	// Unnamed keys survive from the baseline.
	require.True(t, got.Think, "think was not named by the variant")
	require.Equal(t, ptr(0.7), got.Temperature)
	require.Equal(t, ptr(0.9), got.TopP)
	require.Equal(t, "yes", got.ProviderOptions["cache"],
		"provider options merge per key, they do not replace the map")

	// The identity is not a variant's to change.
	require.Equal(t, "anthropic", got.Provider)
	require.Equal(t, "claude-opus-4", got.Model)

	// The baseline itself is untouched, so the next turn resolves clean.
	require.Equal(t, "medium", base.ReasoningEffort)
	require.Equal(t, int64(8000), base.MaxTokens)
	require.Equal(t, "on", base.ProviderOptions["beta"])
}

// TestVariantCanTurnOffWhatTheBaselineTurnedOn pins why the override
// fields are pointers: a "quick" preset over a thinking baseline has to
// be able to say false, which an unset bool cannot express.
func TestVariantCanTurnOffWhatTheBaselineTurnedOn(t *testing.T) {
	t.Parallel()

	base := SelectedModel{
		Think:     true,
		MaxTokens: 8000,
		Variants: map[string]SelectedModelOverride{
			"quick": {Think: ptr(false)},
		},
	}

	got, ok := base.WithVariant("quick", nil)
	require.True(t, ok)
	require.False(t, got.Think)
	require.Equal(t, int64(8000), got.MaxTokens, "max tokens was not named")
}

// TestUnknownVariantDegradesToTheBaseline pins the loose half of
// "strict on switch, loose at resolve": a variant that a provider
// dropped from its catalog mid-session must not brick the turn.
func TestUnknownVariantDegradesToTheBaseline(t *testing.T) {
	t.Parallel()

	base := SelectedModel{ReasoningEffort: "medium"}

	for _, name := range []string{"", "nonexistent"} {
		got, ok := base.WithVariant(name, nil)
		require.False(t, ok, "no overlay applies for %q", name)
		require.Equal(t, base, got)
	}
}

// TestReasoningLevelsSeedVariants pins the abstraction the provider
// catalog buys us: the user picks "high", not reasoning_effort=high.
func TestReasoningLevelsSeedVariants(t *testing.T) {
	t.Parallel()

	cw := &catwalk.Model{
		ID:              "claude-opus-4",
		CanReason:       true,
		ReasoningLevels: []string{"low", "medium", "high"},
	}
	base := SelectedModel{ReasoningEffort: "medium", Think: true}

	got, ok := base.WithVariant("high", cw)
	require.True(t, ok)
	require.Equal(t, "high", got.ReasoningEffort)
	require.True(t, got.Think, "seeding touches reasoning effort only")

	require.Equal(t, []string{"low", "medium", "high"}, base.VariantNames(cw))
	require.Empty(t, base.VariantNames(nil),
		"without a catalog there is nothing to seed from")
}

// TestUserVariantShadowsASeededLevel pins precedence. A user who names
// a variant after a reasoning level meant to redefine that level, not
// to add a second entry under the same name.
func TestUserVariantShadowsASeededLevel(t *testing.T) {
	t.Parallel()

	cw := &catwalk.Model{ReasoningLevels: []string{"low", "high"}}
	base := SelectedModel{
		Variants: map[string]SelectedModelOverride{
			"high":  {ReasoningEffort: ptr("high"), MaxTokens: ptr(int64(64000))},
			"cheap": {MaxTokens: ptr(int64(1000))},
		},
	}

	got, ok := base.WithVariant("high", cw)
	require.True(t, ok)
	require.Equal(t, int64(64000), got.MaxTokens,
		"the user definition won, not the seeded one")

	require.Equal(t, []string{"low", "high", "cheap"}, base.VariantNames(cw),
		"a shadowed level keeps its catalog position and is listed once")
}
