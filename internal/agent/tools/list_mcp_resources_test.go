package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestListMCPResourcesTool_RequiresMCPName pins that a blank mcp_name
// is rejected before any session or MCP lookup happens.
func TestListMCPResourcesTool_RequiresMCPName(t *testing.T) {
	t.Parallel()

	tool := NewListMCPResourcesTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "mcp_name parameter is required")
}

// TestListMCPResourcesTool_RequiresSession pins that a call outside a
// session comes back as a plain error rather than a tool response.
func TestListMCPResourcesTool_RequiresSession(t *testing.T) {
	t.Parallel()

	tool := NewListMCPResourcesTool(nil)
	input, err := json.Marshal(ListMCPResourcesParams{MCPName: "git"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
	require.Zero(t, resp)
}

// TestListMCPResourcesTool_MCPNotConfigured pins that listing
// resources from a server that was never initialized reports a tool
// error instead of hanging, without needing any live MCP connection.
func TestListMCPResourcesTool_MCPNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := NewListMCPResourcesTool(cfg)

	input, err := json.Marshal(ListMCPResourcesParams{MCPName: "nonexistent-mcp-zzz-testonly"})
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not available")
}

// TestListMCPResourcesTool_TrimsMCPName pins that surrounding
// whitespace in mcp_name does not silently produce a different (and
// therefore always-unconfigured) server name.
func TestListMCPResourcesTool_TrimsMCPName(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := NewListMCPResourcesTool(cfg)

	input, err := json.Marshal(ListMCPResourcesParams{MCPName: "  nonexistent-mcp-zzz-testonly  "})
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "nonexistent-mcp-zzz-testonly")
	require.NotContains(t, resp.Content, "  nonexistent")
}
