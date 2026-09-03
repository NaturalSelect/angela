package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// newSplitProviderCoordinator builds a coordinator whose coder agent
// and compact agent sit on different providers, which is the
// configuration that tells the two apart.
func newSplitProviderCoordinator(t *testing.T) *coordinator {
	t.Helper()

	// A key that has to be re-resolved gives the other provider a
	// credential-refresh mechanism, which the running model's provider
	// deliberately lacks.
	t.Setenv("OTHER_KEY", "other-key")

	env := testEnv(t)

	angelaJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {
    "mock": {"id": "mock", "name": "Mock", "type": "openai",
      "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
      "models": [{"id": "large-model", "name": "Large", "context_window": 8192, "default_max_tokens": 128}]},
    "other": {"id": "other", "name": "Other", "type": "openai",
      "base_url": "http://127.0.0.1:10/v1", "api_key": "other-key",
      "models": [{"id": "tiny-model", "name": "Tiny", "context_window": 8192, "default_max_tokens": 128}]}},
  "slots": {"main": {"provider": "mock", "model": "large-model"},
             "chore": {"provider": "other", "model": "tiny-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"), []byte(angelaJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	// The coder runs on main, compaction on chore, so the two resolve
	// to different providers.
	coderCfg := cfg.Config().Agents[config.AgentCoder]
	coderCfg.Slot = config.SlotMain
	cfg.Config().Agents[config.AgentCoder] = coderCfg

	compactCfg := cfg.Config().Agents[config.AgentCompact]
	compactCfg.Slot = config.SlotChore
	cfg.Config().Agents[config.AgentCompact] = compactCfg

	// A key stored as a template is what gives a provider something to
	// re-resolve after a 401. Only the compact provider gets one, so a
	// refresh callback can only have come from it.
	other, ok := cfg.Config().Providers.Get("other")
	require.True(t, ok)
	other.APIKeyTemplate = "$OTHER_KEY"
	cfg.Config().Providers.Set("other", other)

	coord := &coordinator{
		cfg:            cfg,
		sessions:       env.sessions,
		messages:       env.messages,
		permissions:    env.permissions,
		history:        env.history,
		filetracker:    *env.filetracker,
		subagents:      newSubagentRegistry(),
		branches:       newBranchController(),
		subagentRoutes: csync.NewMap[string, subagentRoute](),
	}
	coord.reconcileSubagents()
	coord.currentAgent = coord.buildAgent(coderCfg.ID, false)

	return coord
}

// TestCompactSettingsComeFromTheCompactProvider is the C4 regression.
// Auto-compaction used to be handed the running model's provider
// options and credential-refresh callback, so a compact agent on
// another provider received options meant for a different API and,
// on a 401, refreshed the running model's credentials while its own
// stayed stale.
func TestCompactSettingsComeFromTheCompactProvider(t *testing.T) {
	coord := newSplitProviderCoordinator(t)

	host := instantiate(t, coord, config.AgentCoder)
	require.Equal(t, "mock", host.Model.Provider, "precondition: the turn runs on mock")

	compact := coord.compactFor(t.Context(), "session", host)
	require.True(t, compact.ready, "the compact agent must resolve against its own provider")
	require.Equal(t, "other", compact.agent.Model.ModelCfg.Provider,
		"precondition: compaction runs on the other provider")
	require.Equal(t, "other", compact.provider.ID,
		"the options and auth refresh must come from the provider compaction actually talks to")

	// Only the other provider re-resolves its key, so a non-nil
	// callback here can only have been built from that provider — the
	// running model's would have been nil.
	mainProvider, ok := coord.cfg.Config().Providers.Get(host.Model.Provider)
	require.True(t, ok)
	require.Nil(t, coord.makeAuthRefreshCallback(mainProvider),
		"precondition: the running model's provider has nothing to refresh")
	require.NotNil(t, compact.onAuthRefresh,
		"a 401 from the compact model must refresh the compact model's credentials")
}

// TestCompactSettingsAreAbsentWhenCompactionCannotResolve pins that a
// turn still runs when compaction could not be prepared. Refusing to
// start would be worse than starting without a recovery path.
func TestCompactSettingsAreAbsentWhenCompactionCannotResolve(t *testing.T) {
	coord := newSplitProviderCoordinator(t)
	delete(coord.cfg.Config().Agents, config.AgentCompact)

	compact := coord.compactFor(t.Context(), "session", instantiate(t, coord, config.AgentCoder))
	require.False(t, compact.ready)
	require.False(t, compact.agent.Available())
	require.Nil(t, compact.onAuthRefresh,
		"no provider was resolved, so there is nothing to refresh credentials for")
}
