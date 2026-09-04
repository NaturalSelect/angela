package tools

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestCallHierarchyTool_RequiresSymbol pins that an empty symbol is
// rejected before the LSP manager is touched.
func TestCallHierarchyTool_RequiresSymbol(t *testing.T) {
	t.Parallel()

	tool := NewCallHierarchyTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"direction":"incoming"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "symbol is required")
}

// TestCallHierarchyTool_RequiresValidDirection pins that a direction
// outside {incoming, outgoing} is rejected before the LSP manager is
// touched.
func TestCallHierarchyTool_RequiresValidDirection(t *testing.T) {
	t.Parallel()

	tool := NewCallHierarchyTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"symbol":"Foo","direction":"sideways"}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "direction must be 'incoming' or 'outgoing'")
}

// TestCallHierarchyTool_SymbolNotFound pins the error path reached
// when the symbol cannot be resolved to any LSP-backed position.
func TestCallHierarchyTool_SymbolNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := NewCallHierarchyTool(newLSPManagerWithNoClients(t))

	input, err := json.Marshal(CallHierarchyParams{Symbol: "NoSuchSymbolXYZ", Direction: "incoming", Path: dir})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not found")
}
