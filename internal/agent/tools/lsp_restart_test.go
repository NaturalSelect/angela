package tools

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestLSPRestartTool_NoClientsConfigured pins that restarting "all"
// clients against a manager with none configured reports a plain
// (non-error) status rather than failing.
func TestLSPRestartTool_NoClientsConfigured(t *testing.T) {
	t.Parallel()

	tool := NewLSPRestartTool(newLSPManagerWithNoClients(t))
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "No LSP clients to restart")
}

// TestLSPRestartTool_NamedClientNotFound pins that naming a client
// the manager does not know about is reported as an error rather than
// silently treated as "nothing to do".
func TestLSPRestartTool_NamedClientNotFound(t *testing.T) {
	t.Parallel()

	tool := NewLSPRestartTool(newLSPManagerWithNoClients(t))
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"name":"doesnotexist"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "LSP client 'doesnotexist' not found")
}
