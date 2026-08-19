package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestZAIProviderDoesNotMutateSharedConfig pins that resolving a turn
// leaves the published config untouched. The ZAI branch has to set
// tool_stream on the request, and the ProviderConfig it gets is a value
// copy — but a value copy still aliases the map inside it, which every
// other session is concurrently reading.
func TestZAIProviderDoesNotMutateSharedConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	providerCfg := config.ProviderConfig{
		ID:      string(catwalk.InferenceProviderZAI),
		Type:    catwalk.TypeOpenAICompat,
		BaseURL: "http://127.0.0.1:9/v1",
		APIKey:  "test-key",
		ExtraBody: map[string]any{
			"user_supplied": "keep me",
		},
	}
	coord.cfg.Config().Providers.Set(providerCfg.ID, providerCfg)

	published, ok := coord.cfg.Config().Providers.Get(providerCfg.ID)
	require.True(t, ok)

	model := config.SelectedModel{Provider: providerCfg.ID, Model: "large-model"}
	_, err := coord.buildProvider(published, model, false)
	require.NoError(t, err)

	after, ok := coord.cfg.Config().Providers.Get(providerCfg.ID)
	require.True(t, ok)
	require.NotContains(t, after.ExtraBody, "tool_stream",
		"the turn wrote into the config every other session reads")
	require.Equal(t, "keep me", after.ExtraBody["user_supplied"])
}
