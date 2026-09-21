package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgentsWithUnknownFieldsAreDropped pins that a typo in an agent's
// configuration is loud and disqualifying. The dangerous case is a
// misspelled permission key: the agent parses, its restriction never
// applies, and it silently ends up with the coder's whole tool set.
func TestAgentsWithUnknownFieldsAreDropped(t *testing.T) {
	t.Parallel()

	t.Run("a misspelled permission key drops the agent", func(t *testing.T) {
		t.Parallel()
		buf := captureWarnings(t)

		paths := writeLayers(t,
			`{"agents": {"restricted": {"id": "restricted", "allowed_tool": ["read"]}}}`,
		)
		cfg, _, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		require.NotContains(t, cfg.AgentConfigs, "restricted",
			"an agent whose restriction never parsed must not run unrestricted")
		require.Contains(t, buf.String(), "restricted")
	})

	t.Run("a well-formed agent is kept", func(t *testing.T) {
		t.Parallel()

		paths := writeLayers(t,
			`{"agents": {"restricted": {"id": "restricted", "allowed_tools": ["read"]}}}`,
		)
		cfg, _, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		agent, ok := cfg.AgentConfigs["restricted"]
		require.True(t, ok)
		require.Equal(t, []string{"read"}, agent.AllowedTools.Tools)
	})

	t.Run("compact_agent is a recognized field and does not drop the agent", func(t *testing.T) {
		t.Parallel()

		paths := writeLayers(t,
			`{"agents": {"reviewer": {"id": "reviewer", "compact_agent": "my-compact"}}}`,
		)
		cfg, _, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		agent, ok := cfg.AgentConfigs["reviewer"]
		require.True(t, ok)
		require.Equal(t, "my-compact", agent.CompactAgent)
	})

	t.Run("one broken agent does not take down its neighbours", func(t *testing.T) {
		t.Parallel()

		paths := writeLayers(t, `{"agents": {
			"broken": {"id": "broken", "tempratur": 0.5},
			"fine":   {"id": "fine", "temperature": 0.5}
		}}`)
		cfg, _, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		require.NotContains(t, cfg.AgentConfigs, "broken")
		require.Contains(t, cfg.AgentConfigs, "fine")
	})
}
