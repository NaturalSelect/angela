package tools

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// TestGetMCPTools_NoServersConfigured pins the base case: with no MCP
// tools currently registered, GetMCPTools returns nothing rather than
// panicking or fabricating entries.
func TestGetMCPTools_NoServersConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	require.Empty(t, GetMCPTools(cfg, t.TempDir()))
}

func TestTool_ProviderOptions(t *testing.T) {
	t.Parallel()

	tool := &Tool{}
	require.Zero(t, tool.ProviderOptions())

	opts := fantasy.ProviderOptions{}
	tool.SetProviderOptions(opts)
	require.Equal(t, opts, tool.ProviderOptions())
}

func TestTool_NameMCPAndMCPToolName(t *testing.T) {
	t.Parallel()

	tool := &Tool{
		mcpName: "git",
		tool:    &mcp.Tool{Name: "commit"},
	}

	require.Equal(t, toolnames.MCPPrefix+"git_commit", tool.Name())
	require.Equal(t, "git", tool.MCP())
	require.Equal(t, "commit", tool.MCPToolName())
}

func TestTool_Info(t *testing.T) {
	t.Parallel()

	t.Run("extracts properties and required as []any", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{
			mcpName: "git",
			tool: &mcp.Tool{
				Name:        "commit",
				Description: "Commit staged changes",
				InputSchema: map[string]any{
					"properties": map[string]any{"message": map[string]any{"type": "string"}},
					"required":   []any{"message"},
				},
			},
		}

		info := tool.Info()
		require.Equal(t, toolnames.MCPPrefix+"git_commit", info.Name)
		require.Equal(t, "Commit staged changes", info.Description)
		require.Contains(t, info.Parameters, "message")
		require.Equal(t, []string{"message"}, info.Required)
	})

	t.Run("accepts required as already-typed []string", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{
			tool: &mcp.Tool{
				InputSchema: map[string]any{"required": []string{"a", "b"}},
			},
		}

		info := tool.Info()
		require.Equal(t, []string{"a", "b"}, info.Required)
	})

	t.Run("defaults to empty when schema is not a map", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{tool: &mcp.Tool{InputSchema: "not-a-map"}}

		info := tool.Info()
		require.Empty(t, info.Parameters)
		require.Empty(t, info.Required)
	})
}

// TestTool_Run_RequiresSession pins that Run refuses to call into the
// MCP layer at all without a session ID on the context.
func TestTool_Run_RequiresSession(t *testing.T) {
	t.Parallel()

	tool := &Tool{mcpName: "git", tool: &mcp.Tool{Name: "commit"}}

	_, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
}

// TestTool_Run_InvalidJSONInput pins that malformed call input is
// reported as a tool error response rather than an internal error,
// without needing any live MCP connection.
func TestTool_Run_InvalidJSONInput(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := &Tool{mcpName: "git", tool: &mcp.Tool{Name: "commit"}, cfg: cfg}

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{not valid json`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "error parsing parameters")
}

// TestTool_Run_MCPNotConfigured pins that calling a tool whose MCP
// server was never initialized comes back as a tool error response
// instead of panicking or hanging, exercising Run without any live
// server connection.
func TestTool_Run_MCPNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := &Tool{mcpName: "nonexistent-mcp-zzz-testonly", tool: &mcp.Tool{Name: "commit"}, cfg: cfg}

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not available")
}
