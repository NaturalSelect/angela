package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// TestReplaceSymbolRecordsBothSides pins that applying a symbol
// replacement leaves both the old and the new content in history.
// Recording only the old side means the timeline can say what a file
// was but not what it became, so the file viewer shows a change that
// appears to go nowhere.
//
// The plan phase needs a live language server, so this drives the apply
// closure directly — that is where the history write lives.
func TestReplaceSymbolRecordsBothSides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))

	files, recorded := newRecordingHistoryService(t, "", true)
	tool := NewReplaceSymbolTool(nil, files, newFileTracker(t, time.Now())).(*replaceSymbolTool)

	resp, err := tool.apply(t.Context(),
		ReplaceSymbolParams{Symbol: "Foo", FilePath: path},
		"replace", "s1", "old\n", "new\n", 0, 0)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	require.Equal(t, []string{"old\n", "new\n"}, *recorded,
		"history must hold both sides of the change")

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(written))
}

// TestReplaceSymbolRecordsAdditionsAndRemovals pins that, like Edit,
// Write and MultiEdit, the applied change carries a precomputed +N/-M
// count in its metadata. The renderer's summary line reads these fields
// directly rather than diffing at render time.
func TestReplaceSymbolRecordsAdditionsAndRemovals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))

	tool := NewReplaceSymbolTool(nil, newHistoryService(t, "", true), newFileTracker(t, time.Now())).(*replaceSymbolTool)

	resp, err := tool.apply(t.Context(),
		ReplaceSymbolParams{Symbol: "Foo", FilePath: path},
		"replace", "s1", "old\n", "new\n", 0, 0)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	var meta ReplaceSymbolResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 1, meta.Additions)
	require.Equal(t, 1, meta.Removals)
}

// TestReplaceSymbolKeepsContentChangedOutsideTheSession pins that a file
// edited behind Angela's back keeps that state in the timeline, rather
// than having it overwritten as if it had never existed.
func TestReplaceSymbolKeepsContentChangedOutsideTheSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	files, recorded := newRecordingHistoryService(t, "what history last saw", false)
	tool := NewReplaceSymbolTool(nil, files, newFileTracker(t, time.Now())).(*replaceSymbolTool)

	_, err := tool.apply(t.Context(),
		ReplaceSymbolParams{Symbol: "Foo", FilePath: path},
		"replace", "s1", "what is on disk now", "new", 0, 0)
	require.NoError(t, err)

	require.Equal(t, []string{"what is on disk now", "new"}, *recorded)
}

// TestSpliceSymbol pins the four ways a symbol's range can be rewritten.
// The line arithmetic is off-by-one prone and silently produces a valid
// but wrong file, which no compiler catches.
func TestSpliceSymbol(t *testing.T) {
	t.Parallel()

	lines := []string{"a", "b", "c", "d"}

	cases := map[string]struct {
		action string
		want   string
	}{
		"replace swaps the range":     {"replace", "a\nX\nd"},
		"add_before keeps the symbol": {"add_before", "a\nX\nb\nc\nd"},
		"add_after keeps the symbol":  {"add_after", "a\nb\nc\nX\nd"},
		"delete removes the range":    {"delete", "a\nd"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, spliceSymbol(lines, tc.action, "X", 1, 2))
		})
	}
}

// TestReplaceSymbolPlanRejectsBadInput pins that input problems are
// settled during planning, so the user is never asked to approve a call
// that cannot run.
func TestReplaceSymbolPlanRejectsBadInput(t *testing.T) {
	t.Parallel()

	tool := NewReplaceSymbolTool(nil, nil, nil).(*replaceSymbolTool)

	cases := map[string]ReplaceSymbolParams{
		"no symbol":            {FilePath: "a.go"},
		"no file":              {Symbol: "Foo"},
		"unknown action":       {Symbol: "Foo", FilePath: "a.go", Action: "rewrite"},
		"replace needs a body": {Symbol: "Foo", FilePath: "a.go", Action: "replace"},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, err := tool.plan(context.Background(), params)
			require.NoError(t, err)
			require.NotNil(t, plan.Response, "bad input is settled, not prompted")
			require.True(t, plan.Response.IsError)
			require.Nil(t, plan.Apply)
		})
	}
}

// TestReplaceSymbolPlans pins that the tool reaches the gate as a
// planner, which is how its diff gets into the dialog.
func TestReplaceSymbolPlans(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*Planner)(nil), NewReplaceSymbolTool(nil, nil, nil))
}

// TestReplaceSymbolToolRun_PropagatesPlanResponse pins that run()
// returns whatever plan() settled on directly, without invoking
// Apply.
func TestReplaceSymbolToolRun_PropagatesPlanResponse(t *testing.T) {
	t.Parallel()

	tool := NewReplaceSymbolTool(nil, nil, nil).(*replaceSymbolTool)

	resp, err := tool.run(t.Context(), ReplaceSymbolParams{FilePath: "a.go"}, fantasy.ToolCall{})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "symbol is required")
}

// TestReplaceSymbolToolPlanCapitalized pins that the exported Plan
// method decodes the call's JSON input before delegating to plan(),
// and reports malformed input instead of panicking on it.
func TestReplaceSymbolToolPlanCapitalized(t *testing.T) {
	t.Parallel()

	tool := NewReplaceSymbolTool(nil, nil, nil).(*replaceSymbolTool)

	t.Run("decodes valid input", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(ReplaceSymbolParams{Symbol: "Foo"})
		require.NoError(t, err)

		plan, err := tool.Plan(t.Context(), fantasy.ToolCall{Input: string(input)})
		require.NoError(t, err)
		require.NotNil(t, plan.Response)
		require.True(t, plan.Response.IsError)
		require.Contains(t, plan.Response.Content, "file_path is required")
	})

	t.Run("rejects malformed input", func(t *testing.T) {
		t.Parallel()
		_, err := tool.Plan(t.Context(), fantasy.ToolCall{Input: `{not valid json`})
		require.Error(t, err)
	})
}

// TestFindSymbolByName pins the recursive search through a document
// symbol tree: a top-level match is returned directly, a nested match
// is found by descending into children, and a name present nowhere in
// the tree reports nil rather than panicking.
func TestFindSymbolByName(t *testing.T) {
	t.Parallel()

	tree := []protocol.DocumentSymbolResult{
		&protocol.DocumentSymbol{
			Name: "Outer",
			Children: []protocol.DocumentSymbol{
				{Name: "Inner", Range: protocol.Range{Start: protocol.Position{Line: 3}}},
			},
		},
		&protocol.SymbolInformation{Name: "TopLevel"},
	}

	t.Run("finds a top-level match", func(t *testing.T) {
		t.Parallel()
		got := findSymbolByName(tree, "TopLevel")
		require.NotNil(t, got)
		require.Equal(t, "TopLevel", got.GetName())
	})

	t.Run("descends into children", func(t *testing.T) {
		t.Parallel()
		got := findSymbolByName(tree, "Inner")
		require.NotNil(t, got)
		require.Equal(t, "Inner", got.GetName())
		require.Equal(t, uint32(3), got.GetRange().Start.Line)
	})

	t.Run("missing name returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, findSymbolByName(tree, "NoSuchSymbol"))
	})
}
