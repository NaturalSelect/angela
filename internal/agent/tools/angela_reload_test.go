package tools

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestAngelaReloadTool_ReturnsSummary(t *testing.T) {
	t.Parallel()

	tool := NewAngelaReloadTool(func(context.Context) (string, error) {
		return "Reloaded from disk.", nil
	})
	require.Equal(t, toolnames.AngelaReload, tool.Info().Name)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Equal(t, "Reloaded from disk.", resp.Content)
}

func TestAngelaReloadTool_ReportsFailure(t *testing.T) {
	t.Parallel()

	tool := NewAngelaReloadTool(func(context.Context) (string, error) {
		return "", errors.New("failed to reload config: bad json")
	})

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "bad json")
}

// TestAngelaReloadAccessAlwaysAsks pins that reloading is execution with no
// command, which the permission ladder never treats as in scope.
func TestAngelaReloadAccessAlwaysAsks(t *testing.T) {
	t.Parallel()

	access, ok := AccessOf(toolnames.AngelaReload, `{}`, "/work")
	require.True(t, ok)
	require.Equal(t, permission.ActionExecute, access.Action)
	require.Empty(t, access.Command)
	require.Empty(t, access.Path)

	preview := PreviewOf(toolnames.AngelaReload, `{}`, "/work")
	require.Contains(t, preview.Description, "MCP server")
}
