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

// TestDefinitionTool_RequiresSymbol pins that an empty symbol is
// rejected before the LSP manager is ever touched — passing a nil
// manager here would panic if that invariant broke.
func TestDefinitionTool_RequiresSymbol(t *testing.T) {
	t.Parallel()

	tool := NewDefinitionTool(nil)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "symbol is required")
}

// TestDefinitionTool_SymbolNotFound pins that a symbol resolveSymbol
// cannot place — because nothing in the search root mentions it —
// surfaces as a "not found" response rather than an internal error.
func TestDefinitionTool_SymbolNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := NewDefinitionTool(newLSPManagerWithNoClients(t))

	input, err := json.Marshal(DefinitionParams{Symbol: "NoSuchSymbolXYZ", Path: dir})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not found")
}

// TestFormatDefinitions pins the rendered shape of a definition
// listing: a count header, one file:line block per location with a
// source snippet, and metadata carrying the first hit for the
// renderer.
func TestFormatDefinitions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	content := "package a\n\nfunc First() {}\n\nfunc Second() {}\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	locations := []protocol.Location{
		{URI: protocol.URIFromPath(path), Range: protocol.Range{Start: protocol.Position{Line: 4, Character: 5}}},
		{URI: protocol.URIFromPath(path), Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 5}}},
	}

	text, meta := formatDefinitions(locations)

	require.Contains(t, text, "Found 2 definition(s):")
	require.Contains(t, text, path)
	require.Contains(t, text, "func First()")
	require.Contains(t, text, "func Second()")

	require.NotNil(t, meta)
	require.Equal(t, path, meta.FilePath)
	// cleanupLocations sorts ascending by line, so line 2 (0-indexed)
	// comes first.
	require.Equal(t, 2, meta.Line)
	require.Contains(t, meta.Content, "func First()")
}

// TestFormatDefinitions_SkipsUnparsableURI pins that a location whose
// URI cannot be converted to a path is dropped instead of aborting
// the whole listing.
func TestFormatDefinitions_SkipsUnparsableURI(t *testing.T) {
	t.Parallel()

	locations := []protocol.Location{
		{URI: protocol.DocumentURI("not-a-file-uri")},
	}

	text, meta := formatDefinitions(locations)
	require.Contains(t, text, "Found 1 definition(s):")
	require.Nil(t, meta)
}

func TestReadSourceContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644))

	t.Run("marks the target line and clamps the window to file bounds", func(t *testing.T) {
		t.Parallel()
		got := readSourceContext(path, 0, 3)
		require.Contains(t, got, "> ")
		require.Contains(t, got, "one")
		require.Contains(t, got, "four")
		require.NotContains(t, got, "five")
	})

	t.Run("missing file returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, readSourceContext(filepath.Join(dir, "missing.go"), 0, 3))
	})
}

func TestReadSourceLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644))

	t.Run("returns raw lines without markers", func(t *testing.T) {
		t.Parallel()
		got := readSourceLines(path, 2, 1)
		require.Equal(t, "two\nthree\nfour", got)
		require.NotContains(t, got, ">")
	})

	t.Run("missing file returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, readSourceLines(filepath.Join(dir, "missing.go"), 0, 3))
	})
}
