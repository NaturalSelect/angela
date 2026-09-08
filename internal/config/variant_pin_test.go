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

// reviewerAgentConfig is twoProviderConfig plus a second, custom primary
// agent. DefaultAgentID falls back to AgentCoder whenever the override
// names no primary agent, so a test that only ever picks "coder" would
// pass whether or not the override actually took hold; picking a
// different primary agent rules that out.
func reviewerAgentConfig(provider, model string) string {
	return `{
		"slots": {
			"main": {"provider": "` + provider + `", "model": "` + model + `"}
		},
		"providers": {
			"openai": {
				"api_key": "test-key",
				"models": [{"id": "gpt-4", "name": "GPT-4"}]
			},
			"anthropic": {
				"api_key": "test-key-2",
				"models": [{"id": "claude-3", "name": "Claude 3"}]
			}
		},
		"agents": {
			"reviewer": {"description": "Reviews code", "mode": "primary"}
		}
	}`
}

// TestDefaultAgentSurvivesPeerWrite is the default-agent counterpart to
// TestAgentVariantSurvivesPeerWrite: a primary agent picked in this
// instance before any session exists to scope the pick to must not be
// clobbered by a reload triggered by an unrelated write to the shared
// config file.
func TestDefaultAgentSurvivesPeerWrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "angela.json")

	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(configPath, []byte(reviewerAgentConfig("openai", "gpt-4")), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)
	store.globalDataPath = configPath
	store.CaptureStalenessSnapshot([]string{configPath})

	require.NoError(t, store.OverrideDefaultAgent("reviewer"))
	require.Equal(t, "reviewer", store.Config().DefaultAgentID())

	// A sibling instance writes to the shared file; the reload this
	// triggers must not clobber the default agent picked in this
	// instance.
	require.NoError(t, os.WriteFile(configPath, []byte(reviewerAgentConfig("openai", "gpt-4")), 0o600))
	require.NoError(t, store.ReloadFromDisk(context.Background()))

	require.Equal(t, "reviewer", store.Config().DefaultAgentID())
}

// TestOverrideDefaultAgent_UnknownAgent covers the validation error for
// an agent ID the UI cannot offer in practice but the store must still
// reject if asked.
func TestOverrideDefaultAgent_UnknownAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(twoProviderConfig("openai", "gpt-4")), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)

	err = store.OverrideDefaultAgent("no-such-agent")
	require.ErrorContains(t, err, "unknown agent")
}

// TestOverrideDefaultAgent_RejectsSubagent covers the check that has no
// counterpart in OverrideAgentVariant: a subagent can never drive a
// session, so it must never become the default a new session starts on
// either.
func TestOverrideDefaultAgent_RejectsSubagent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(twoProviderConfig("openai", "gpt-4")), 0o600))

	store, err := Load(dir, dir, false)
	require.NoError(t, err)

	err = store.OverrideDefaultAgent(AgentExplore)
	require.ErrorContains(t, err, "subagent")
	require.Equal(t, AgentCoder, store.Config().DefaultAgentID(),
		"a rejected pick must not change what a new session starts on")
}
