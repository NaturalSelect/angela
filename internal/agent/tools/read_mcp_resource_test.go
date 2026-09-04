package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestReadMCPResourceTool_RequiresMCPName pins that a blank mcp_name
// is rejected before any session or MCP lookup happens.
func TestReadMCPResourceTool_RequiresMCPName(t *testing.T) {
	t.Parallel()

	tool := NewReadMCPResourceTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"uri":"file:///a.txt"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "mcp_name parameter is required")
}

// TestReadMCPResourceTool_RequiresURI pins that a blank uri is
// rejected before any session or MCP lookup happens.
func TestReadMCPResourceTool_RequiresURI(t *testing.T) {
	t.Parallel()

	tool := NewReadMCPResourceTool(nil)
	input, err := json.Marshal(ReadMCPResourceParams{MCPName: "git"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "uri parameter is required")
}

// TestReadMCPResourceTool_RequiresSession pins that a call outside a
// session comes back as a plain error rather than a tool response,
// mirroring the other session-scoped tools.
func TestReadMCPResourceTool_RequiresSession(t *testing.T) {
	t.Parallel()

	tool := NewReadMCPResourceTool(nil)
	input, err := json.Marshal(ReadMCPResourceParams{MCPName: "git", URI: "file:///a.txt"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
	require.Zero(t, resp)
}

// TestReadMCPResourceTool_MCPNotConfigured pins that reading from a
// server that was never initialized reports a tool error instead of
// hanging, without needing any live MCP connection.
func TestReadMCPResourceTool_MCPNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := NewReadMCPResourceTool(cfg)

	input, err := json.Marshal(ReadMCPResourceParams{MCPName: "nonexistent-mcp-zzz-testonly", URI: "file:///a.txt"})
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not available")
}
