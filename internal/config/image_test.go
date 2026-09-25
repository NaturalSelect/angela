package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad_ImageSlotResolvesWithoutCatalogModel pins that the "image"
// slot is a plain named model configuration: ModelForSlot returns
// whatever the config named it, even though the model it names
// ("gpt-image-1") is an image-generation model that never appears in
// any provider's chat-completion catalog. Only main and chore get
// catalog-aware fallback resolution (see resolveSelectedModels); every
// other slot — including the one the built-in image tools read — must
// pass through untouched, loaded via the real config pipeline rather
// than a hand-built Config.
func TestLoad_ImageSlotResolvesWithoutCatalogModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	body := `{
		"slots": {
			"main": {"provider": "openai", "model": "gpt-4"},
			"image": {"provider": "openai", "model": "gpt-image-1"}
		},
		"providers": {
			"openai": {
				"api_key": "test-key",
				"models": [{"id": "gpt-4", "name": "GPT-4"}]
			}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(body), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)

	cfg := store.Config()

	model, ok := cfg.ModelForSlot(SlotImage)
	require.True(t, ok, "the image slot must resolve even though gpt-image-1 is absent from openai's chat model catalog")
	require.Equal(t, "openai", model.Provider)
	require.Equal(t, "gpt-image-1", model.Model)

	require.Contains(t, cfg.Agents, AgentImage)
	require.True(t, cfg.Agents[AgentImage].IsHidden(), "the image agent must stay hidden from dispatch/completion")
}

// TestConfigStore_ImageToolsDisabled covers the OR between the
// persisted options.disable_image_tools setting and the runtime
// --disable-image-tools override, and that a Config with a nil
// Options (never defaulted) does not panic.
func TestConfigStore_ImageToolsDisabled(t *testing.T) {
	t.Run("false by default", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{}}}
		require.False(t, store.ImageToolsDisabled())
	})

	t.Run("persisted option disables", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{DisableImageTools: true}}}
		require.True(t, store.ImageToolsDisabled())
	})

	t.Run("runtime override disables", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{}}}
		store.Overrides().DisableImageTools = true
		require.True(t, store.ImageToolsDisabled())
	})

	t.Run("nil Options does not panic", func(t *testing.T) {
		store := &ConfigStore{config: &Config{}}
		require.False(t, store.ImageToolsDisabled())
	})
}
