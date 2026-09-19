package config

import (
	"context"
	"os"
	"path/filepath"
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

func TestConfigStore_SetAgentModelOverride_ValidatesBeforeApplying(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{config: activeTestConfig()}

	err := store.SetAgentModelOverride("ghost", SelectedModel{Provider: "anthropic", Model: "claude"})
	require.ErrorIs(t, err, ErrAgentOverrideUnknownAgent)
	require.Empty(t, store.Config().AgentModelOverrides, "a failed validation must not apply the override")
}

// TestConfigStore_SetAgentModelOverride_PublishesWithoutMutatingPreviousConfig
// mirrors TestConfigStore_SetupAgentsDoesNotMutatePublishedConfig: a mutator
// must publish a new Config value rather than writing through the pointer
// every other reader still holds.
func TestConfigStore_SetAgentModelOverride_PublishesWithoutMutatingPreviousConfig(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{config: activeTestConfig()}
	before := store.Config()

	require.NoError(t, store.SetAgentModelOverride("coder", SelectedModel{Provider: "groq", Model: "llama"}))

	after := store.Config()
	require.NotSame(t, before, after, "SetAgentModelOverride must publish a new Config value, not mutate the live one")
	require.Empty(t, before.AgentModelOverrides, "the previously published Config must be left untouched")

	active, ok := after.InstantiateAgent("coder")
	require.True(t, ok)
	require.Equal(t, "groq", active.Model.Provider)
	require.Equal(t, "llama", active.Model.Model)
}

func TestConfigStore_ClearAgentModelOverrides(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{config: activeTestConfig()}
	require.NoError(t, store.SetAgentModelOverride("coder", SelectedModel{Provider: "groq", Model: "llama"}))
	require.NotEmpty(t, store.Config().AgentModelOverrides)

	store.ClearAgentModelOverrides()

	require.Empty(t, store.Config().AgentModelOverrides)
	active, ok := store.Config().InstantiateAgent("coder")
	require.True(t, ok)
	require.Equal(t, "claude", active.Model.Model, "clearing the override must restore the agent's configured model")
}

// TestAgentModelOverrideSurvivesReloadFromDisk is the agent-override
// counterpart to TestModelSelectionSurvivesPeerWrite: a reload triggered by
// an unrelated write to the shared config file (a sibling instance, a token
// refresh) must not drop a pin this instance made.
func TestAgentModelOverrideSurvivesReloadFromDisk(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "angela.json")

	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(configPath, []byte(twoProviderConfig("openai", "gpt-4")), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)
	store.globalDataPath = configPath
	store.CaptureStalenessSnapshot([]string{configPath})

	require.NoError(t, store.SetAgentModelOverride(AgentCoder, SelectedModel{Provider: "anthropic", Model: "claude-3"}))

	// An unrelated write to the shared config file must not drop the pin.
	require.NoError(t, os.WriteFile(configPath, []byte(twoProviderConfig("openai", "gpt-4")), 0o600))
	require.NoError(t, store.ReloadFromDisk(context.Background()))

	active, ok := store.Config().InstantiateAgent(AgentCoder)
	require.True(t, ok)
	require.Equal(t, "anthropic", active.Model.Provider)
	require.Equal(t, "claude-3", active.Model.Model)
}

// TestAgentModelOverrideDroppedWhenItNoLongerResolvesAfterReload pins the
// other half: a pin that reload can no longer validate — here because the
// provider it named disappeared from the file — must not survive as a
// pin to nothing.
func TestAgentModelOverrideDroppedWhenItNoLongerResolvesAfterReload(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "angela.json")

	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(configPath, []byte(twoProviderConfig("openai", "gpt-4")), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)
	store.globalDataPath = configPath
	store.CaptureStalenessSnapshot([]string{configPath})

	require.NoError(t, store.SetAgentModelOverride(AgentCoder, SelectedModel{Provider: "anthropic", Model: "claude-3"}))

	// The config file is rewritten without the anthropic provider at all,
	// as if the user removed it while this instance still held the pin.
	openAIOnly := `{
		"slots": {"main": {"provider": "openai", "model": "gpt-4"}},
		"providers": {"openai": {"api_key": "test-key", "models": [{"id": "gpt-4", "name": "GPT-4"}]}}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(openAIOnly), 0o600))
	require.NoError(t, store.ReloadFromDisk(context.Background()))

	require.Empty(t, store.Config().AgentModelOverrides,
		"an override that no longer resolves must be dropped, not silently kept")
	active, ok := store.Config().InstantiateAgent(AgentCoder)
	require.True(t, ok)
	require.Equal(t, "openai", active.Model.Provider, "the agent must fall back to its configured model")
}
