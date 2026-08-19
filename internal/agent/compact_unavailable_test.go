package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestUnavailableCompactAgentIsReportedNotPanicked pins that a compact
// agent that failed to resolve is carried as an explicit error rather
// than as a zero value. The zero value holds a nil LanguageModel, which
// fantasy dereferences.
func TestUnavailableCompactAgentIsReportedNotPanicked(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	// Point the compact agent at a provider that is not configured,
	// the way removing a provider from angelarc would.
	cfg := coord.cfg.Config()
	broken := cfg.Models[config.ModelChore]
	broken.Provider = "provider-that-was-removed"
	cfg.Models[config.ModelChore] = broken

	compactCfg := cfg.Agents[config.AgentCompact]
	compactCfg.Model = config.ModelChore
	cfg.Agents[config.AgentCompact] = compactCfg

	compact := coord.buildCompactAgent(t.Context(), config.ActiveAgent{})
	require.False(t, compact.Available())
	require.Error(t, compact.Err)
	require.Nil(t, compact.Model.Model)

	// Summarize must refuse rather than hand the nil model to fantasy.
	err := coord.currentAgent.Summarize(t.Context(), "session-does-not-matter", compact, nil, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrSessionBusy)
	require.Contains(t, err.Error(), "compact agent unavailable")
}
