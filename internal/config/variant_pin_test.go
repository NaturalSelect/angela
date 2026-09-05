package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgentVariantSurvivesPeerWrite is the variant-pick counterpart to
// TestModelSelectionSurvivesPeerWrite: a variant chosen in this
// instance must not be clobbered by a reload triggered by an
// unrelated write to the shared config file.
func TestAgentVariantSurvivesPeerWrite(t *testing.T) {
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

	require.NoError(t, store.OverrideAgentVariant(AgentCoder, "fast"))
	require.Equal(t, "fast", store.Config().Agents[AgentCoder].Variant)

	// A sibling instance writes to the shared file; the reload this
	// triggers must not clobber the variant picked in this instance.
	require.NoError(t, os.WriteFile(configPath, []byte(twoProviderConfig("openai", "gpt-4")), 0o600))
	require.NoError(t, store.ReloadFromDisk(context.Background()))

	require.Equal(t, "fast", store.Config().Agents[AgentCoder].Variant)
}

// TestOverrideAgentVariant_UnknownAgent covers the validation error
// for an agent ID the UI cannot offer in practice but the store must
// still reject if asked.
func TestOverrideAgentVariant_UnknownAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(twoProviderConfig("openai", "gpt-4")), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)

	err = store.OverrideAgentVariant("no-such-agent", "fast")
	require.ErrorContains(t, err, "unknown agent")
}
