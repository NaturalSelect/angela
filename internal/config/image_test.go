package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	t.Setenv("ANGELA_CACHE_DIR", dir)
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

// TestConfigStore_ImageToolsEnabled covers the tri-state resolution of
// options.enable_image_tools and the runtime --enable-image-tools
// override (the override wins when set), and that a Config with a nil
// Options (never defaulted) does not panic.
func TestConfigStore_ImageToolsEnabled(t *testing.T) {
	t.Run("nil by default", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{}}}
		require.Nil(t, store.ImageToolsEnabled())
	})

	t.Run("persisted true", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{EnableImageTools: new(true)}}}
		got := store.ImageToolsEnabled()
		require.NotNil(t, got)
		require.True(t, *got)
	})

	t.Run("persisted false", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{EnableImageTools: new(false)}}}
		got := store.ImageToolsEnabled()
		require.NotNil(t, got)
		require.False(t, *got)
	})

	t.Run("override false beats persisted true", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{EnableImageTools: new(true)}}}
		store.Overrides().EnableImageTools = new(false)
		got := store.ImageToolsEnabled()
		require.NotNil(t, got)
		require.False(t, *got)
	})

	t.Run("override true beats persisted false", func(t *testing.T) {
		store := &ConfigStore{config: &Config{Options: &Options{EnableImageTools: new(false)}}}
		store.Overrides().EnableImageTools = new(true)
		got := store.ImageToolsEnabled()
		require.NotNil(t, got)
		require.True(t, *got)
	})

	t.Run("nil Options does not panic", func(t *testing.T) {
		store := &ConfigStore{config: &Config{}}
		require.Nil(t, store.ImageToolsEnabled())
	})
}

// TestToolImage_GetTimeout covers the default (10 minutes) applied
// when options.tools.image.timeout is unset, and that a configured
// value overrides it.
func TestToolImage_GetTimeout(t *testing.T) {
	t.Run("defaults to 10 minutes", func(t *testing.T) {
		require.Equal(t, 10*time.Minute, ToolImage{}.GetTimeout())
	})

	t.Run("uses the configured value", func(t *testing.T) {
		timeout := 90 * time.Second
		require.Equal(t, timeout, ToolImage{Timeout: &timeout}.GetTimeout())
	})
}
