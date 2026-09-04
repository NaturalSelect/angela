package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestRenameRecordsBothSides pins the fix for a rename that only ever
// stored the pre-edit content: with one side missing, restoring a file
// from history brought back the text the rename had replaced.
func TestRenameRecordsBothSides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "a.go")
	second := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(first, []byte("old one\n"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("old two\n"), 0o644))

	files, recorded := newRecordingHistoryService(t, "", true)
	tracker, reads := newRecordingFileTracker(t, time.Time{})
	tool := NewRenameTool(nil, files, tracker).(*renameTool)

	paths := []string{first, second}
	before := tool.readAll(paths)
	require.Equal(t, map[string]string{first: "old one\n", second: "old two\n"}, before)

	// The language server writes the files; the tool records what it
	// finds afterwards.
	require.NoError(t, os.WriteFile(first, []byte("new one\n"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("new two\n"), 0o644))

	tool.recordRename(t.Context(), "session", paths, before)

	require.Equal(t,
		[]string{"old one\n", "new one\n", "old two\n", "new two\n"},
		*recorded,
		"every renamed file leaves both of its sides in history")
	require.Equal(t, paths, *reads)
}

// TestRenameRecordsNothingWithoutSession pins that a rename outside a
// session touches neither history nor the read tracker, rather than
// filing versions under an empty session ID.
func TestRenameRecordsNothingWithoutSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))

	files, recorded := newRecordingHistoryService(t, "", true)
	tracker, reads := newRecordingFileTracker(t, time.Time{})
	tool := NewRenameTool(nil, files, tracker).(*renameTool)

	tool.recordRename(t.Context(), "", []string{path}, map[string]string{path: "old\n"})

	require.Empty(t, *recorded)
	require.Empty(t, *reads)
}

// TestRenameSkipsUnreadableFiles pins that a file the tool could not
// snapshot is left out of history rather than recorded with an empty
// previous version, which would look like the rename created it.
func TestRenameSkipsUnreadableFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	present := filepath.Join(dir, "a.go")
	absent := filepath.Join(dir, "gone.go")
	require.NoError(t, os.WriteFile(present, []byte("old\n"), 0o644))

	files, recorded := newRecordingHistoryService(t, "", true)
	tool := NewRenameTool(nil, files, newFileTracker(t, time.Time{})).(*renameTool)

	before := tool.readAll([]string{present, absent})
	require.NotContains(t, before, absent)

	require.NoError(t, os.WriteFile(present, []byte("new\n"), 0o644))
	tool.recordRename(t.Context(), "session", []string{present, absent}, before)

	require.Equal(t, []string{"old\n", "new\n"}, *recorded)
}

// TestRenamePlanRejectsBadInput pins that the arguments are checked
// before the language server is consulted, so a malformed call never
// reaches the gate.
func TestRenamePlanRejectsBadInput(t *testing.T) {
	t.Parallel()

	tool := NewRenameTool(nil, nil, nil).(*renameTool)

	for name, params := range map[string]RenameParams{
		"no symbol":   {NewName: "After"},
		"no new name": {Symbol: "Before"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, err := tool.plan(context.Background(), params)
			require.NoError(t, err)
			require.NotNil(t, plan.Response)
			require.True(t, plan.Response.IsError)
			require.Nil(t, plan.Apply)
		})
	}
}

// TestRenameToolPlans pins that rename reaches the gate as a planner.
func TestRenameToolPlans(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*Planner)(nil), NewRenameTool(nil, nil, nil))
}

// TestRenamePlanSymbolNotFound pins the error path reached when the
// symbol cannot be resolved to any LSP-backed position — no live
// language server is needed to reach it.
func TestRenamePlanSymbolNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := NewRenameTool(newLSPManagerWithNoClients(t), nil, nil).(*renameTool)

	plan, err := tool.plan(t.Context(), RenameParams{Symbol: "NoSuchSymbolXYZ", NewName: "After", Path: dir})
	require.NoError(t, err)
	require.NotNil(t, plan.Response)
	require.True(t, plan.Response.IsError)
	require.Contains(t, plan.Response.Content, "not found")
	require.Nil(t, plan.Apply)
}

// TestRenameToolRun_PropagatesPlanResponse pins that run() returns
// whatever plan() settled on directly, without invoking Apply.
func TestRenameToolRun_PropagatesPlanResponse(t *testing.T) {
	t.Parallel()

	tool := NewRenameTool(nil, nil, nil).(*renameTool)

	resp, err := tool.run(t.Context(), RenameParams{NewName: "After"}, fantasy.ToolCall{})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "symbol is required")
}

// TestRenameToolPlanCapitalized pins that the exported Plan method
// decodes the call's JSON input before delegating to plan(), and
// reports malformed input instead of panicking on it.
func TestRenameToolPlanCapitalized(t *testing.T) {
	t.Parallel()

	tool := NewRenameTool(nil, nil, nil).(*renameTool)

	t.Run("decodes valid input", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(RenameParams{Symbol: "Before"})
		require.NoError(t, err)

		plan, err := tool.Plan(t.Context(), fantasy.ToolCall{Input: string(input)})
		require.NoError(t, err)
		require.NotNil(t, plan.Response)
		require.True(t, plan.Response.IsError)
		require.Contains(t, plan.Response.Content, "new_name is required")
	})

	t.Run("rejects malformed input", func(t *testing.T) {
		t.Parallel()
		_, err := tool.Plan(t.Context(), fantasy.ToolCall{Input: `{not valid json`})
		require.Error(t, err)
	})
}
