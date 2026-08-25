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

type mockFileTrackerService struct{}

func (m mockFileTrackerService) RecordRead(ctx context.Context, sessionID, path string) {}

func (m mockFileTrackerService) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return time.Now()
}

func (m mockFileTrackerService) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

func TestWriteToolWritesEmptyNewFile(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewWriteTool(nil, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(WriteParams{FilePath: "empty.txt", Content: ""})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  WriteToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	b, err := os.ReadFile(filepath.Join(workingDir, "empty.txt"))
	require.NoError(t, err)
	require.Equal(t, "", string(b))
}

// writePlan builds a plan for one write call, the way the permission
// decorator does.
func writePlan(t *testing.T, workingDir string, params WriteParams) Plan {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	tool := NewWriteTool(nil, &mockHistoryService{}, mockFileTrackerService{}, workingDir).(*writeTool)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	plan, err := tool.Plan(ctx, fantasy.ToolCall{
		ID: "call-1", Name: WriteToolName, Input: string(input),
	})
	require.NoError(t, err)
	return plan
}

// TestWritePlanCreatesNothingUntilApply pins that planning leaves the
// disk alone. Planning happens before the user is asked, so a directory
// created here would survive a refusal: the user says no and finds an
// empty tree they never agreed to.
func TestWritePlanCreatesNothingUntilApply(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	nested := filepath.Join("a", "b", "c.txt")
	fullPath := filepath.Join(workingDir, nested)

	plan := writePlan(t, workingDir, WriteParams{FilePath: nested, Content: "hi"})
	require.NoDirExists(t, filepath.Dir(fullPath), "planning must not create directories")
	require.NoFileExists(t, fullPath)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")
	resp, err := plan.Apply(ctx)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	content, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	require.Equal(t, "hi", string(content))
}

// TestWritePlanPreviewsTheDiff pins that the plan carries what the
// permission dialog renders. The dialog type asserts on this struct and
// draws nothing when the assertion fails.
func TestWritePlanPreviewsTheDiff(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	existing := filepath.Join(workingDir, "a.txt")
	require.NoError(t, os.WriteFile(existing, []byte("old\n"), 0o644))

	plan := writePlan(t, workingDir, WriteParams{FilePath: "a.txt", Content: "new\n"})

	params, ok := plan.Preview.Params.(WritePermissionsParams)
	require.True(t, ok, "the dialog asserts on WritePermissionsParams")
	require.Equal(t, "old\n", params.OldContent)
	require.Equal(t, "new\n", params.NewContent)

	meta, ok := plan.Refusal.(WriteResponseMetadata)
	require.True(t, ok, "a refused write still shows its diff")
	require.NotEmpty(t, meta.Diff)
}

// TestWritePlanSettlesWithoutPrompting pins that a write with nothing to
// do never reaches the user. Asking someone to approve a no-op is noise,
// and the model needs to read why instead.
func TestWritePlanSettlesWithoutPrompting(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "same.txt"), []byte("x"), 0o644))

	plan := writePlan(t, workingDir, WriteParams{FilePath: "same.txt", Content: "x"})
	require.NotNil(t, plan.Response, "an unchanged write is settled by planning")
	require.True(t, plan.Response.IsError)
	require.Nil(t, plan.Apply)
}
