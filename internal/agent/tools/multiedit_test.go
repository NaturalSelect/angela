package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/stretchr/testify/require"
)

func TestApplyEditToContentPartialSuccess(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\nline 3\n"

	// Test successful edit.
	newContent, _, err := applyEditToContent(content, MultiEditOperation{
		OldString: "line 1",
		NewString: "LINE 1",
	})
	require.NoError(t, err)
	require.Contains(t, newContent, "LINE 1")
	require.Contains(t, newContent, "line 2")

	// Test failed edit (string not found).
	_, _, err = applyEditToContent(content, MultiEditOperation{
		OldString: "line 99",
		NewString: "LINE 99",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestApplyEditToContentReplacementModes(t *testing.T) {
	t.Parallel()

	content := "alpha\nbeta\nalpha\n"

	newContent, _, err := applyEditToContent(content, MultiEditOperation{
		OldString:  "alpha",
		NewString:  "ALPHA",
		ReplaceAll: true,
	})
	require.NoError(t, err)
	require.Equal(t, "ALPHA\nbeta\nALPHA\n", newContent)

	_, _, err = applyEditToContent(content, MultiEditOperation{
		OldString: "alpha",
		NewString: "ALPHA",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple times")

	newContent, _, err = applyEditToContent(content, MultiEditOperation{})
	require.NoError(t, err)
	require.Equal(t, content, newContent)
}

func TestMultiEditSequentialApplication(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	// Create test file.
	content := "line 1\nline 2\nline 3\nline 4\n"
	err := os.WriteFile(testFile, []byte(content), 0o644)
	require.NoError(t, err)

	// Manually test the sequential application logic.
	currentContent := content

	// Apply edits sequentially, tracking failures.
	edits := []MultiEditOperation{
		{OldString: "line 1", NewString: "LINE 1"},   // Should succeed
		{OldString: "line 99", NewString: "LINE 99"}, // Should fail - doesn't exist
		{OldString: "line 3", NewString: "LINE 3"},   // Should succeed
		{OldString: "line 2", NewString: "LINE 2"},   // Should succeed - still exists
	}

	var failedEdits []FailedEdit
	successCount := 0

	for i, edit := range edits {
		newContent, _, err := applyEditToContent(currentContent, edit)
		if err != nil {
			failedEdits = append(failedEdits, FailedEdit{
				Index: i + 1,
				Error: err.Error(),
				Edit:  edit,
			})
			continue
		}
		currentContent = newContent
		successCount++
	}

	// Verify results.
	require.Equal(t, 3, successCount, "Expected 3 successful edits")
	require.Len(t, failedEdits, 1, "Expected 1 failed edit")

	// Check failed edit details.
	require.Equal(t, 2, failedEdits[0].Index)
	require.Contains(t, failedEdits[0].Error, "not found")

	// Verify content changes.
	require.Contains(t, currentContent, "LINE 1")
	require.Contains(t, currentContent, "LINE 2")
	require.Contains(t, currentContent, "LINE 3")
	require.Contains(t, currentContent, "line 4") // Original unchanged
	require.NotContains(t, currentContent, "LINE 99")
}

func TestMultiEditAllEditsSucceed(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\nline 3\n"

	edits := []MultiEditOperation{
		{OldString: "line 1", NewString: "LINE 1"},
		{OldString: "line 2", NewString: "LINE 2"},
		{OldString: "line 3", NewString: "LINE 3"},
	}

	currentContent := content
	successCount := 0

	for _, edit := range edits {
		newContent, _, err := applyEditToContent(currentContent, edit)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		currentContent = newContent
		successCount++
	}

	require.Equal(t, 3, successCount)
	require.Contains(t, currentContent, "LINE 1")
	require.Contains(t, currentContent, "LINE 2")
	require.Contains(t, currentContent, "LINE 3")
}

func TestMultiEditAllEditsFail(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\n"

	edits := []MultiEditOperation{
		{OldString: "line 99", NewString: "LINE 99"},
		{OldString: "line 100", NewString: "LINE 100"},
	}

	currentContent := content
	var failedEdits []FailedEdit

	for i, edit := range edits {
		newContent, _, err := applyEditToContent(currentContent, edit)
		if err != nil {
			failedEdits = append(failedEdits, FailedEdit{
				Index: i + 1,
				Error: err.Error(),
				Edit:  edit,
			})
			continue
		}
		currentContent = newContent
	}

	require.Len(t, failedEdits, 2)
	require.Equal(t, content, currentContent, "Content should be unchanged")
}

// multiEditPlan builds a plan for one multiedit call, the way the
// permission decorator does, and returns it with the context to apply
// it in.
func multiEditPlan(t *testing.T, dir string, tracker filetracker.Service, files history.Service, params MultiEditParams) (Plan, context.Context) {
	t.Helper()

	tool := NewMultiEditTool(nil, files, tracker, dir).(*multiEditTool)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session")

	plan, err := tool.plan(ctx, params)
	require.NoError(t, err)
	return plan, ctx
}

func TestProcessMultiEditExistingFilePartialFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0o644))

	plan, ctx := multiEditPlan(t, dir, newFileTracker(t, time.Now().Add(time.Second)), newHistoryService(t, "", false), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "two", NewString: "TWO"},
			{OldString: "missing", NewString: "MISSING"},
		},
	})

	resp, err := plan.Apply(ctx)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Applied 1 of 2 edits")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "one\nTWO\nthree\n", string(content))

	var meta MultiEditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 1, meta.EditsApplied)
	require.Len(t, meta.EditsFailed, 1)
	require.Equal(t, 2, meta.EditsFailed[0].Index)
	require.Equal(t, "one\ntwo\nthree\n", meta.OldContent)
	require.Equal(t, "one\nTWO\nthree\n", meta.NewContent)
}

func TestProcessMultiEditWithCreationPartialFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "nested", "test.txt")

	plan, ctx := multiEditPlan(t, dir, newFileTracker(t, time.Time{}), newHistoryService(t, "", false), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "", NewString: "one\ntwo\nthree\n"},
			{OldString: "two", NewString: "TWO"},
			{OldString: "missing", NewString: "MISSING"},
		},
	})

	resp, err := plan.Apply(ctx)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "File created with 2 of 3 edits")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "one\nTWO\nthree\n", string(content))

	var meta MultiEditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 2, meta.EditsApplied)
	require.Len(t, meta.EditsFailed, 1)
	require.Equal(t, 3, meta.EditsFailed[0].Index)
	require.Equal(t, "", meta.OldContent)
	require.Equal(t, "one\nTWO\nthree\n", meta.NewContent)
}

// TestMultiEditPlanCreatesNothingUntilApply pins that planning a new
// file leaves the disk alone. Planning runs before the user is asked,
// so directories made here would outlive a refusal.
func TestMultiEditPlanCreatesNothingUntilApply(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "nested", "test.txt")

	plan, ctx := multiEditPlan(t, dir, newFileTracker(t, time.Time{}), newHistoryService(t, "", false), MultiEditParams{
		FilePath: filePath,
		Edits:    []MultiEditOperation{{OldString: "", NewString: "hello\n"}},
	})

	require.NoDirExists(t, filepath.Dir(filePath), "planning must not create directories")
	require.NotNil(t, plan.Apply)

	_, err := plan.Apply(ctx)
	require.NoError(t, err)
	require.FileExists(t, filePath)
}

// TestMultiEditPlanPreviewsTheWholeBatch pins that the user is asked
// once, about the combined result of every edit, rather than once per
// edit.
func TestMultiEditPlanPreviewsTheWholeBatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\n"), 0o644))

	plan, _ := multiEditPlan(t, dir, newFileTracker(t, time.Now().Add(time.Second)), newHistoryService(t, "", false), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "one", NewString: "ONE"},
			{OldString: "two", NewString: "TWO"},
		},
	})

	params, ok := plan.Preview.Params.(MultiEditPermissionsParams)
	require.True(t, ok, "the dialog asserts on MultiEditPermissionsParams")
	require.Equal(t, "one\ntwo\n", params.OldContent)
	require.Equal(t, "ONE\nTWO\n", params.NewContent, "both edits show in one prompt")

	meta, ok := plan.Refusal.(MultiEditResponseMetadata)
	require.True(t, ok, "a refused batch still shows its diff")
	require.Equal(t, "ONE\nTWO\n", meta.NewContent)
	require.Equal(t, 2, meta.EditsApplied)
}

// TestMultiEditPlanSettlesWhenEveryEditFails pins that a batch with
// nothing to write never reaches the gate.
func TestMultiEditPlanSettlesWhenEveryEditFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\n"), 0o644))

	plan, _ := multiEditPlan(t, dir, newFileTracker(t, time.Now().Add(time.Second)), newHistoryService(t, "", false), MultiEditParams{
		FilePath: filePath,
		Edits:    []MultiEditOperation{{OldString: "missing", NewString: "MISSING"}},
	})

	require.NotNil(t, plan.Response)
	require.True(t, plan.Response.IsError)
	require.Contains(t, plan.Response.Content, "all 1 edit(s) failed")
	require.Nil(t, plan.Apply)
}

// TestMultiEditToolPlans pins that multiedit reaches the gate as a
// planner.
func TestMultiEditToolPlans(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*Planner)(nil), NewMultiEditTool(nil, nil, nil, t.TempDir()))
}
