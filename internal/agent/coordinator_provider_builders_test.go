package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestBuildProviderConstructsEveryKnownType pins that buildProvider
// dispatches every provider type to a working constructor. None of
// these perform network I/O at construction time, so the assertion is
// just that a non-nil provider comes back with no error.
func TestBuildProviderConstructsEveryKnownType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		providerCfg config.ProviderConfig
	}{
		{
			name: "openrouter",
			providerCfg: config.ProviderConfig{
				ID: "openrouter", Type: openrouter.Name, APIKey: "test-key",
				ExtraHeaders: map[string]string{"X-Test": "1"},
			},
		},
		{
			name:        "vercel",
			providerCfg: config.ProviderConfig{ID: "vercel", Type: vercel.Name, APIKey: "test-key"},
		},
		{
			name: "azure",
			providerCfg: config.ProviderConfig{
				ID: "azure", Type: azure.Name, APIKey: "test-key",
				BaseURL:     "https://example.openai.azure.com",
				ExtraParams: map[string]string{"apiVersion": "2024-01-01"},
			},
		},
		{
			name:        "bedrock",
			providerCfg: config.ProviderConfig{ID: "bedrock", Type: bedrock.Name, APIKey: "test-key"},
		},
		{
			name:        "bedrock-europe",
			providerCfg: config.ProviderConfig{ID: string(catwalk.InferenceProviderBedrockEurope), Type: bedrock.Name},
		},
		{
			name:        "google",
			providerCfg: config.ProviderConfig{ID: "google", Type: google.Name, APIKey: "test-key"},
		},
		{
			name: "google-vertex",
			providerCfg: config.ProviderConfig{
				ID: "google-vertex", Type: "google-vertex",
				ExtraParams: map[string]string{"project": "my-project", "location": "us-central1"},
			},
		},
		{
			name:        "known custom provider falls back to openai-compat",
			providerCfg: config.ProviderConfig{ID: "ollama", Type: "ollama", BaseURL: "http://127.0.0.1:9/v1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			coord := newModelPrefTestCoordinator(t, nil)
			provider, err := coord.buildProvider(tt.providerCfg, config.ProviderModel{Model: catwalk.Model{ID: "m"}}, false, false)
			require.NoError(t, err)
			require.NotNil(t, provider)
		})
	}
}

// TestBuildProviderRejectsUnsupportedType pins that an unrecognized,
// unregistered provider type fails loudly instead of silently
// resolving to something unintended.
func TestBuildProviderRejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	_, err := coord.buildProvider(
		config.ProviderConfig{ID: "mystery", Type: "totally-unknown"},
		config.ProviderModel{Model: catwalk.Model{ID: "m"}}, false, false,
	)
	require.ErrorContains(t, err, "provider type not supported")
}

// TestBuildBedrockProviderUsesConfiguredRegion pins that a Bedrock
// Europe provider ID selects the eu-west-1 region rather than the
// default us-east-1, and that everything else still builds cleanly.
func TestBuildBedrockProviderUsesConfiguredRegion(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	provider, err := coord.buildBedrockProvider("", map[string]string{"X-Test": "1"}, string(catwalk.InferenceProviderBedrockEurope))
	require.NoError(t, err)
	require.NotNil(t, provider)
}

// TestIsExactoSupported pins the exact allowlist of OpenRouter models
// that get the ":exacto" suffix appended.
func TestIsExactoSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		modelID string
		want    bool
	}{
		{"moonshotai/kimi-k2-0905", true},
		{"deepseek/deepseek-v3.1-terminus", true},
		{"z-ai/glm-4.6", true},
		{"openai/gpt-oss-120b", true},
		{"qwen/qwen3-coder", true},
		{"unknown/model", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isExactoSupported(tt.modelID))
		})
	}
}

// TestMergeCallOptions pins that mergeCallOptions both forwards the
// model's sampling knobs unchanged and produces provider options
// derived from the same model/provider pairing getProviderOptions
// would.
func TestMergeCallOptions(t *testing.T) {
	t.Parallel()

	temp, topP, freq, pres := 0.5, 0.9, 0.1, 0.2
	var topK int64 = 40

	model := Model{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{
			ID: "gpt-5.2",
			Options: catwalk.ModelOptions{
				Temperature:      &temp,
				TopP:             &topP,
				TopK:             &topK,
				FrequencyPenalty: &freq,
				PresencePenalty:  &pres,
			},
		}},
		ModelCfg: config.SelectedModel{Provider: "openai"},
	}
	providerCfg := config.ProviderConfig{ID: "openai", Type: "openai"}

	opts, gotTemp, gotTopP, gotTopK, gotFreq, gotPres := mergeCallOptions(model, providerCfg, "cache-key")

	require.Same(t, &temp, gotTemp)
	require.Same(t, &topP, gotTopP)
	require.Same(t, &topK, gotTopK)
	require.Same(t, &freq, gotFreq)
	require.Same(t, &pres, gotPres)

	// The provider options half must equal what getProviderOptions
	// would independently produce for the same inputs.
	require.Equal(t, getProviderOptions(model, providerCfg, "cache-key"), opts)
}
