package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// TestSymbolsTool_RequiresFilePath pins that a missing file_path is
// rejected before the LSP manager is touched.
func TestSymbolsTool_RequiresFilePath(t *testing.T) {
	t.Parallel()

	tool := NewSymbolsTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "file_path is required")
}

// TestSymbolsTool_NoLSPClientHandlesFile pins the error path reached
// when no running LSP client claims the target file.
func TestSymbolsTool_NoLSPClientHandlesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("package a\n"), 0o644))

	tool := NewSymbolsTool(newLSPManagerWithNoClients(t))

	input, err := json.Marshal(SymbolsParams{FilePath: path})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no LSP client handles file")
}

// TestFormatSymbols pins the indentation and per-line shape of a
// symbol outline: kind, name, 1-based line number, and children
// nested one level deeper than their parent.
func TestFormatSymbols(t *testing.T) {
	t.Parallel()

	symbols := []protocol.DocumentSymbolResult{
		&protocol.DocumentSymbol{
			Name:  "Widget",
			Kind:  protocol.Struct,
			Range: protocol.Range{Start: protocol.Position{Line: 0}},
			Children: []protocol.DocumentSymbol{
				{Name: "Render", Kind: protocol.Method, Range: protocol.Range{Start: protocol.Position{Line: 2}}},
			},
		},
		&protocol.SymbolInformation{
			Name:     "globalVar",
			Kind:     protocol.Variable,
			Location: protocol.Location{Range: protocol.Range{Start: protocol.Position{Line: 9}}},
		},
	}

	got := formatSymbols(symbols, 0)

	require.Contains(t, got, "Struct Widget (line 1)\n")
	require.Contains(t, got, "  Method Render (line 3)\n")
	require.Contains(t, got, "Variable globalVar (line 10)\n")
}

func TestFormatSymbols_Empty(t *testing.T) {
	t.Parallel()

	require.Empty(t, formatSymbols(nil, 0))
}

// TestFormatDocumentSymbolChildren pins that grandchildren nest two
// levels deep.
func TestFormatDocumentSymbolChildren(t *testing.T) {
	t.Parallel()

	children := []protocol.DocumentSymbol{
		{
			Name:  "Outer",
			Kind:  protocol.Class,
			Range: protocol.Range{Start: protocol.Position{Line: 0}},
			Children: []protocol.DocumentSymbol{
				{Name: "Inner", Kind: protocol.Field, Range: protocol.Range{Start: protocol.Position{Line: 1}}},
			},
		},
	}

	got := formatDocumentSymbolChildren(children, 1)
	require.Equal(t, "  Class Outer (line 1)\n    Field Inner (line 2)\n", got)
}

func TestSymbolKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sym  protocol.DocumentSymbolResult
		want string
	}{
		{"known DocumentSymbol kind", &protocol.DocumentSymbol{Kind: protocol.Function}, "Function"},
		{"known SymbolInformation kind", &protocol.SymbolInformation{Kind: protocol.Interface}, "Interface"},
		{"unknown kind falls back to Symbol", &protocol.DocumentSymbol{Kind: protocol.SymbolKind(9999)}, "Symbol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, symbolKindString(tt.sym))
		})
	}
}
