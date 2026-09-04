package tools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// newLSPManagerWithNoClients returns an *lsp.Manager with no clients
// and a working directory that matches no path, so Start is always a
// no-op and no real language server ever gets spawned.
func newLSPManagerWithNoClients(t *testing.T) *lsp.Manager {
	t.Helper()
	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{},
	})
	return lsp.NewManager(cfg)
}

func TestGetSymbolOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		symbol string
		want   int
	}{
		{"bare symbol", "Bar", 0},
		{"dot qualified", "foo.Bar", 4},
		{"double colon qualified", "Class::method", 7},
		{"backslash qualified", `ns\Func`, 3},
		{"nested dots", "a.b.C", 4},
		{"empty", "", 0},
		{"single char", "x", 0},
		{"dot at end", "foo.", 4},
		{"colon at end", "foo::", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getSymbolOffset(tt.symbol)
			require.Equal(t, tt.want, got, "getSymbolOffset(%q)", tt.symbol)
		})
	}
}

// TestGetSymbolOffset_DoesNotOvershoot verifies that the offset lands
// on the start of the final component, never past it.
func TestGetSymbolOffset_DoesNotOvershoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		symbol   string
		expected string // the substring starting at the offset
	}{
		{"foo.Bar", "Bar"},
		{"Class::method", "method"},
		{`ns\Func`, "Func"},
		{"a.b.c.D", "D"},
		{"Bar", "Bar"},
	}

	for _, tc := range cases {
		offset := getSymbolOffset(tc.symbol)
		require.LessOrEqual(t, offset, len(tc.symbol),
			"offset %d exceeds symbol length %d for %q", offset, len(tc.symbol), tc.symbol)
		got := tc.symbol[offset:]
		require.Equal(t, tc.expected, got,
			"getSymbolOffset(%q) = %d, remainder = %q, want %q",
			tc.symbol, offset, got, tc.expected)
	}
}

func TestIsNoIdentifierError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"matching error", errors.New("no identifier found at position"), true},
		{"non-matching error", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isNoIdentifierError(tt.err))
		})
	}
}

// TestCollectAffectedFiles pins that every shape a WorkspaceEdit can
// carry a file reference in — the Changes map and each variant of the
// DocumentChanges union — contributes to the affected-file list, with
// duplicates collapsed regardless of which shape names them.
func TestCollectAffectedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.go")
	pathB := filepath.Join(dir, "b.go")
	pathC := filepath.Join(dir, "c.go")
	pathD := filepath.Join(dir, "d.go")
	pathE := filepath.Join(dir, "e.go")

	edit := &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentURI][]protocol.TextEdit{
			protocol.URIFromPath(pathA): nil,
		},
		DocumentChanges: []protocol.DocumentChange{
			{TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.URIFromPath(pathB)},
				},
			}},
			{CreateFile: &protocol.CreateFile{URI: protocol.URIFromPath(pathC)}},
			{RenameFile: &protocol.RenameFile{OldURI: protocol.URIFromPath(pathD), NewURI: protocol.URIFromPath(pathE)}},
			// Duplicates pathA, already seen via Changes.
			{DeleteFile: &protocol.DeleteFile{URI: protocol.URIFromPath(pathA)}},
		},
	}

	got := collectAffectedFiles(edit)
	require.Equal(t, []string{pathA, pathB, pathC, pathD, pathE}, got)
}

func TestCollectAffectedFiles_Empty(t *testing.T) {
	t.Parallel()

	require.Empty(t, collectAffectedFiles(&protocol.WorkspaceEdit{}))
}

// TestCleanupLocations pins that references come back sorted by
// file then position and with exact duplicates collapsed, so the
// same reference reported by more than one LSP query is not shown
// twice.
func TestCleanupLocations(t *testing.T) {
	t.Parallel()

	loc := func(uri string, line, char uint32) protocol.Location {
		return protocol.Location{
			URI:   protocol.DocumentURI(uri),
			Range: protocol.Range{Start: protocol.Position{Line: line, Character: char}},
		}
	}

	locations := []protocol.Location{
		loc("file:///b.go", 5, 2),
		loc("file:///a.go", 10, 0),
		loc("file:///a.go", 1, 3),
		loc("file:///a.go", 1, 3), // exact duplicate
		loc("file:///b.go", 5, 2), // exact duplicate
	}

	got := cleanupLocations(locations)

	require.Equal(t, []protocol.Location{
		loc("file:///a.go", 1, 3),
		loc("file:///a.go", 10, 0),
		loc("file:///b.go", 5, 2),
	}, got)
}

func TestFindLSPClient_NoClientsReturnsNil(t *testing.T) {
	t.Parallel()

	mgr := newLSPManagerWithNoClients(t)
	require.Nil(t, findLSPClient(mgr, "/some/file.go"))
}

// TestResolveSymbolResults_SymbolNotFoundInGrep pins the error path
// that needs no LSP client at all: nothing in the search root even
// mentions the symbol.
func TestResolveSymbolResults_SymbolNotFoundInGrep(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mgr := newLSPManagerWithNoClients(t)

	_, err := resolveSymbolResults(t.Context(), mgr, "NoSuchSymbolXYZ", dir)
	require.ErrorContains(t, err, "not found in grep results")
}

// TestResolveSymbolResults_NoLSPClientHandlesMatch pins the case
// where grep finds the symbol but no running LSP client claims the
// file it lives in.
func TestResolveSymbolResults_NoLSPClientHandlesMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc TargetSymbol() {}\n"), 0o644))

	mgr := newLSPManagerWithNoClients(t)

	_, err := resolveSymbolResults(t.Context(), mgr, "TargetSymbol", dir)
	require.ErrorContains(t, err, "no LSP client handles any file matching")
}

// TestResolveSymbol_PropagatesResolveSymbolResultsError pins that
// resolveSymbol surfaces resolveSymbolResults' error verbatim rather
// than masking it, since it never reaches the per-candidate loop
// without at least one result.
func TestResolveSymbol_PropagatesResolveSymbolResultsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mgr := newLSPManagerWithNoClients(t)

	_, err := resolveSymbol(t.Context(), mgr, "NoSuchSymbolXYZ", dir)
	require.ErrorContains(t, err, "not found in grep results")
}
