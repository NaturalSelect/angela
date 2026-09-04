package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// TestReferencesTool_RequiresSymbol pins that an empty symbol is
// rejected before the LSP manager is touched.
func TestReferencesTool_RequiresSymbol(t *testing.T) {
	t.Parallel()

	tool := NewReferencesTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "symbol is required")
}

// TestReferencesTool_SymbolNotFound pins that an unresolvable symbol
// comes back as a plain (non-error) "not found" response, since the
// model should read it as "nothing to show" rather than a failure.
func TestReferencesTool_SymbolNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := NewReferencesTool(newLSPManagerWithNoClients(t))

	input, err := json.Marshal(ReferencesParams{Symbol: "NoSuchSymbolXYZ", Path: dir})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "not found")
}

// TestGroupByFilename pins that locations are bucketed by their file
// path, preserving each file's reference order.
func TestGroupByFilename(t *testing.T) {
	t.Parallel()

	locA1 := protocol.Location{URI: protocol.DocumentURI("file:///a.go"), Range: protocol.Range{Start: protocol.Position{Line: 1}}}
	locA2 := protocol.Location{URI: protocol.DocumentURI("file:///a.go"), Range: protocol.Range{Start: protocol.Position{Line: 5}}}
	locB1 := protocol.Location{URI: protocol.DocumentURI("file:///b.go"), Range: protocol.Range{Start: protocol.Position{Line: 2}}}

	got := groupByFilename([]protocol.Location{locA1, locB1, locA2})

	require.Equal(t, map[string][]protocol.Location{
		"/a.go": {locA1, locA2},
		"/b.go": {locB1},
	}, got)
}

func TestGroupByFilename_SkipsUnparsableURI(t *testing.T) {
	t.Parallel()

	got := groupByFilename([]protocol.Location{{URI: protocol.DocumentURI("not-a-file-uri")}})
	require.Empty(t, got)
}

// TestFormatReferences pins the rendered shape: a total count header,
// files listed alphabetically, and 1-based line/column numbers per
// reference.
func TestFormatReferences(t *testing.T) {
	t.Parallel()

	locations := []protocol.Location{
		{URI: protocol.DocumentURI("file:///b.go"), Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}}},
		{URI: protocol.DocumentURI("file:///a.go"), Range: protocol.Range{Start: protocol.Position{Line: 9, Character: 4}}},
		{URI: protocol.DocumentURI("file:///a.go"), Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 2}}},
	}

	got := formatReferences(locations)

	require.Contains(t, got, "Found 3 reference(s) in 2 file(s):")

	// /a.go must be listed before /b.go (alphabetical file order).
	aIdx := strings.Index(got, "/a.go (2 reference(s)):")
	bIdx := strings.Index(got, "/b.go (1 reference(s)):")
	require.GreaterOrEqual(t, aIdx, 0)
	require.GreaterOrEqual(t, bIdx, 0)
	require.Less(t, aIdx, bIdx)

	require.Contains(t, got, "Line 10, Column 5")
	require.Contains(t, got, "Line 1, Column 3")
	require.Contains(t, got, "Line 1, Column 1")
}

func TestFormatReferences_Empty(t *testing.T) {
	t.Parallel()

	got := formatReferences(nil)
	require.Contains(t, got, "Found 0 reference(s) in 0 file(s):")
}
