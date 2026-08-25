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

type mockEditFileTracker struct {
	lastRead time.Time
	reads    []string
}

func (m *mockEditFileTracker) RecordRead(ctx context.Context, sessionID, path string) {
	m.reads = append(m.reads, path)
}

func (m *mockEditFileTracker) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return m.lastRead
}

func (m *mockEditFileTracker) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return m.reads, nil
}

// editPlan builds a plan for one edit call, the way the permission
// decorator does, and returns it with the context to apply it in.
func editPlan(t *testing.T, dir string, tracker filetracker.Service, files history.Service, params EditParams) (Plan, context.Context) {
	t.Helper()

	tool := NewEditTool(nil, files, tracker, dir).(*editTool)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session")

	plan, err := tool.plan(ctx, params)
	require.NoError(t, err)
	return plan, ctx
}

func TestReplaceContentPreservesCRLFAndMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\r\nbeta\r\n"), 0o644))

	tracker := &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}
	plan, ctx := editPlan(t, dir, tracker, &mockHistoryService{}, EditParams{
		FilePath: filePath, OldString: "beta", NewString: "BETA",
	})

	resp, err := plan.Apply(ctx)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Content replaced in file: "+filePath)

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "alpha\r\nBETA\r\n", string(content))
	require.Equal(t, []string{filePath}, tracker.reads)

	var meta EditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "alpha\nbeta\n", meta.OldContent)
	require.Equal(t, "alpha\r\nBETA\r\n", meta.NewContent)
}

func TestDeleteContentRejectsMultipleMatchesWithoutReplaceAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\nalpha\n"), 0o644))

	tracker := &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}
	plan, _ := editPlan(t, dir, tracker, &mockHistoryService{}, EditParams{
		FilePath: filePath, OldString: "alpha\n",
	})

	require.NotNil(t, plan.Response, "an ambiguous edit is settled, not prompted")
	require.True(t, plan.Response.IsError)
	require.Contains(t, plan.Response.Content, "old_string appears multiple times")
	require.Nil(t, plan.Apply)

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "alpha\nbeta\nalpha\n", string(content))
}

// TestEditCreatePlanCreatesNothing pins that planning a new file leaves
// the disk alone. Planning runs before the user is asked, so directories
// made here would outlive a refusal.
func TestEditCreatePlanCreatesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "new.txt")

	tracker := &mockEditFileTracker{lastRead: time.Now()}
	plan, ctx := editPlan(t, dir, tracker, &mockHistoryService{}, EditParams{
		FilePath: nested, NewString: "hello\n",
	})

	require.NoDirExists(t, filepath.Dir(nested), "planning must not create directories")
	require.NotNil(t, plan.Apply)

	_, err := plan.Apply(ctx)
	require.NoError(t, err)

	content, err := os.ReadFile(nested)
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(content))
}

// TestEditPlanKeepsMismatchDiagnostics pins that the whitespace
// diagnosis survives the move into planning. It is what lets the model
// fix its own edit instead of guessing, and it is easy to lose because
// nothing else depends on it.
func TestEditPlanKeepsMismatchDiagnostics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(filePath, []byte("func main() {\n\tprintln(1)\n}\n"), 0o644))

	tracker := &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}
	plan, _ := editPlan(t, dir, tracker, &mockHistoryService{}, EditParams{
		FilePath: filePath, OldString: "println(2)", NewString: "println(3)",
	})

	require.NotNil(t, plan.Response)
	require.True(t, plan.Response.IsError)
	require.Contains(t, plan.Response.Content, "old_string not found")
	require.Nil(t, plan.Apply)
}

// TestEditPlanPreviewsTheDiff pins that the plan carries what the
// permission dialog draws, and what a refusal shows in the chat.
func TestEditPlanPreviewsTheDiff(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))

	tracker := &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}
	plan, _ := editPlan(t, dir, tracker, &mockHistoryService{}, EditParams{
		FilePath: filePath, OldString: "alpha", NewString: "omega",
	})

	params, ok := plan.Preview.Params.(EditPermissionsParams)
	require.True(t, ok, "the dialog asserts on EditPermissionsParams")
	require.Equal(t, "alpha\n", params.OldContent)
	require.Equal(t, "omega\n", params.NewContent)

	meta, ok := plan.Refusal.(EditResponseMetadata)
	require.True(t, ok, "a refused edit still shows its diff")
	require.Equal(t, "alpha\n", meta.OldContent)
	require.Equal(t, "omega\n", meta.NewContent)
}

// TestEditPlanRefusesUnreadFile pins that the read-before-edit rule is
// enforced during planning, so the user is never asked about an edit
// that would be rejected anyway.
func TestEditPlanRefusesUnreadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))

	plan, _ := editPlan(t, dir, &mockEditFileTracker{}, &mockHistoryService{}, EditParams{
		FilePath: filePath, OldString: "alpha", NewString: "omega",
	})

	require.NotNil(t, plan.Response)
	require.True(t, plan.Response.IsError)
	require.Contains(t, plan.Response.Content, "must read the file")
	require.Nil(t, plan.Apply)
}

// TestEditToolPlans pins that edit reaches the gate as a planner.
func TestEditToolPlans(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*Planner)(nil), NewEditTool(nil, nil, nil, t.TempDir()))
}
