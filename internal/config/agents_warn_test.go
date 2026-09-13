package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWarnUnknownAgents pins the three branches of warnUnknownAgents:
// a nil set is a silent no-op, a set naming only known agents logs
// nothing, and an unrecognized ID is called out without rejecting the
// config.
func TestWarnUnknownAgents(t *testing.T) {
	known := map[string]Agent{"explore": {ID: "explore"}}

	t.Run("nil set logs nothing", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownAgents("coder", nil, known)
		require.Empty(t, buf.String())
	})

	t.Run("every id known logs nothing", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownAgents("coder", &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"explore"}}, known)
		require.Empty(t, buf.String())
	})

	t.Run("unknown id is reported", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownAgents("coder", &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"typo-agent"}}, known)
		require.Contains(t, buf.String(), "unknown agent id")
		require.Contains(t, buf.String(), "typo-agent")
	})
}

// TestWarnUnknownCompactAgent pins all four branches of
// warnUnknownCompactAgent: an unset field, a compact-mode agent
// naming its own compact_agent, a reference to an agent that does not
// exist, and a reference to an agent that exists but is not in
// compact mode. None of these reject the config, so the only
// observable effect is the warning itself.
func TestWarnUnknownCompactAgent(t *testing.T) {
	known := map[string]Agent{
		"my-compact": {ID: "my-compact", Mode: AgentModeCompact},
		"coder":      {ID: "coder", Mode: AgentModePrimary},
	}

	t.Run("unset field logs nothing", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownCompactAgent("coder", AgentModePrimary, "", known)
		require.Empty(t, buf.String())
	})

	t.Run("a compact agent cannot itself use compact_agent", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownCompactAgent("my-compact", AgentModeCompact, "other-compact", known)
		require.Contains(t, buf.String(), "cannot itself use compact_agent")
	})

	t.Run("unknown compact_agent id is reported", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownCompactAgent("coder", AgentModePrimary, "does-not-exist", known)
		require.Contains(t, buf.String(), "unknown compact_agent id")
	})

	t.Run("compact_agent resolving to a non-compact agent is reported", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownCompactAgent("coder", AgentModePrimary, "coder", known)
		require.Contains(t, buf.String(), "does not resolve to a compact-mode agent")
	})

	t.Run("a valid compact_agent reference logs nothing", func(t *testing.T) {
		buf := captureWarnings(t)
		warnUnknownCompactAgent("coder", AgentModePrimary, "my-compact", known)
		require.Empty(t, buf.String())
	})
}
