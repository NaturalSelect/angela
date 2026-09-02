package undo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fixture wires a real, temporary-database-backed undo.Service the
// same way app.New does, so tests exercise the same SQL and the same
// disk operations undo runs in production.
type fixture struct {
	svc        Service
	messages   message.Service
	history    history.Service
	sessions   session.Service
	busy       *fakeBusyChecker
	workingDir string
}

// fakeBusyChecker lets tests mark specific sessions as having live
// agent activity, the same thing agent.Coordinator reports in
// production.
type fakeBusyChecker struct {
	busy   map[string]bool
	branch map[string]bool
}

func (f *fakeBusyChecker) IsSessionBusy(sessionID string) bool   { return f.busy[sessionID] }
func (f *fakeBusyChecker) IsSessionBranch(sessionID string) bool { return f.branch[sessionID] }

func newFixture(t *testing.T) fixture {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	q := db.New(conn)
	sessions := session.NewService(q, conn)
	messages := message.NewService(q)
	historySvc := history.NewService(q, conn)
	filetrackerSvc := filetracker.NewService(q)
	workingDir := t.TempDir()
	busy := &fakeBusyChecker{busy: map[string]bool{}, branch: map[string]bool{}}

	return fixture{
		svc:        NewService(messages, historySvc, sessions, filetrackerSvc, busy, workingDir),
		messages:   messages,
		history:    historySvc,
		sessions:   sessions,
		busy:       busy,
		workingDir: workingDir,
	}
}

func (f fixture) newSession(t *testing.T) string {
	t.Helper()
	sess, err := f.sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	return sess.ID
}

func (f fixture) userMessage(t *testing.T, sessionID, text string) {
	t.Helper()
	_, err := f.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	})
	require.NoError(t, err)
}

// editCall simulates one Edit tool call the same way the real tool
// leaves it behind: the file written to disk, its version chain in
// file history, and the assistant/tool message pair recording the
// call, so undo can be tested against the same data shapes the real
// tool produces without going through tool execution itself.
func (f fixture) editCall(t *testing.T, sessionID, path, oldContent, newContent string, created bool) {
	t.Helper()
	ctx := t.Context()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(newContent), 0o644))

	if _, err := f.history.GetByPathAndSession(ctx, path, sessionID); err != nil {
		seed := oldContent
		if created {
			seed = ""
		}
		_, err := f.history.Create(ctx, sessionID, path, seed)
		require.NoError(t, err)
	}
	_, err := f.history.CreateVersion(ctx, sessionID, path, newContent)
	require.NoError(t, err)

	callID := uuid.New().String()
	input, err := json.Marshal(map[string]string{"file_path": path})
	require.NoError(t, err)
	_, err = f.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: callID, Name: toolnames.Edit, Input: string(input)}},
	})
	require.NoError(t, err)

	meta, err := json.Marshal(tools.EditResponseMetadata{OldContent: oldContent, NewContent: newContent, Created: created})
	require.NoError(t, err)
	_, err = f.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: callID, Name: toolnames.Edit, Metadata: string(meta)}},
	})
	require.NoError(t, err)
}

// agentCall simulates one Agent (subagent) tool call: it creates the
// child session under the same derived ID the coordinator uses,
// invokes build to populate the child's own messages, then records
// the call and its result on the parent session.
func (f fixture) agentCall(t *testing.T, parentSessionID string, build func(childSessionID string)) string {
	t.Helper()
	ctx := t.Context()

	callID := uuid.New().String()
	msg, err := f.messages.Create(ctx, parentSessionID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: callID, Name: toolnames.Agent, Input: "{}"}},
	})
	require.NoError(t, err)

	childID := f.sessions.CreateAgentToolSessionID(msg.ID, callID)
	_, err = f.sessions.CreateTaskSession(ctx, childID, parentSessionID, "sub")
	require.NoError(t, err)

	build(childID)

	_, err = f.messages.Create(ctx, parentSessionID, message.CreateMessageParams{
		Role:  message.Tool,
		Parts: []message.ContentPart{message.ToolResult{ToolCallID: callID, Name: toolnames.Agent, Content: "done"}},
	})
	require.NoError(t, err)
	return childID
}

func TestFindCut(t *testing.T) {
	t.Parallel()

	msg := func(id string, role message.MessageRole) message.Message {
		return message.Message{ID: id, Role: role}
	}

	tests := []struct {
		name string
		msgs []message.Message
		want int
	}{
		{
			name: "empty session",
			msgs: nil,
			want: -1,
		},
		{
			name: "no user message at all",
			msgs: []message.Message{msg("a1", message.Assistant), msg("t1", message.Tool)},
			want: -1,
		},
		{
			name: "single pending user message with no reply yet",
			msgs: []message.Message{msg("u1", message.User)},
			want: 0,
		},
		{
			name: "one completed turn following another",
			msgs: []message.Message{
				msg("u1", message.User), msg("a1", message.Assistant), msg("t1", message.Tool),
				msg("u2", message.User), msg("a2", message.Assistant), msg("t2", message.Tool),
			},
			want: 3,
		},
		{
			name: "consecutive queued user messages with no reply yet",
			msgs: []message.Message{
				msg("u1", message.User), msg("a1", message.Assistant),
				msg("u2", message.User), msg("u3", message.User),
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, findCut(tt.msgs))
		})
	}
}

func TestUndoRevertsTheLastTurnAndPopsItsText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	f.userMessage(t, sessionID, "first turn")
	path := filepath.Join(f.workingDir, "a.txt")
	f.editCall(t, sessionID, path, "", "first\n", true)

	f.userMessage(t, sessionID, "second turn")
	f.editCall(t, sessionID, path, "first\n", "second\n", false)

	preview, err := f.svc.Preview(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "second turn", preview.PoppedText)
	require.Equal(t, []string{path}, preview.Revert)
	require.Empty(t, preview.Delete)
	require.Empty(t, preview.Skipped)

	result, err := f.svc.Undo(t.Context(), sessionID, preview.CutMessageID)
	require.NoError(t, err)
	require.Equal(t, "second turn", result.PoppedText)
	require.Equal(t, []string{path}, result.Reverted)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "first\n", string(content))

	remaining, err := f.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, remaining, 3, "only the first turn's user/assistant/tool-result messages should survive")
}

func TestUndoSkipsFilesModifiedOutsideTheSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	f.userMessage(t, sessionID, "edit two files")
	pathA := filepath.Join(f.workingDir, "a.txt")
	pathB := filepath.Join(f.workingDir, "b.txt")
	f.editCall(t, sessionID, pathA, "old a\n", "new a\n", false)
	f.editCall(t, sessionID, pathB, "old b\n", "new b\n", false)

	// Something outside Angela touches a.txt after the agent's edit.
	require.NoError(t, os.WriteFile(pathA, []byte("drifted\n"), 0o644))

	preview, err := f.svc.Preview(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{pathB}, preview.Revert)
	require.Len(t, preview.Skipped, 1)
	require.Equal(t, pathA, preview.Skipped[0].Path)
	require.Equal(t, "modified outside the session", preview.Skipped[0].Reason)

	result, err := f.svc.Undo(t.Context(), sessionID, preview.CutMessageID)
	require.NoError(t, err)
	require.Equal(t, []string{pathB}, result.Reverted)

	driftedContent, err := os.ReadFile(pathA)
	require.NoError(t, err)
	require.Equal(t, "drifted\n", string(driftedContent), "a drifted file must be left exactly as found")

	revertedContent, err := os.ReadFile(pathB)
	require.NoError(t, err)
	require.Equal(t, "old b\n", string(revertedContent))
}

func TestUndoDeletesFilesTheTurnCreated(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	f.userMessage(t, sessionID, "create a file")
	path := filepath.Join(f.workingDir, "new.txt")
	f.editCall(t, sessionID, path, "", "created content\n", true)

	preview, err := f.svc.Preview(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{path}, preview.Delete)
	require.Empty(t, preview.Revert)

	result, err := f.svc.Undo(t.Context(), sessionID, preview.CutMessageID)
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Deleted)
	require.NoFileExists(t, path)
}

func TestUndoRevertsSubagentFilesAndDeletesTheChildSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	f.userMessage(t, sessionID, "delegate to a subagent")
	path := filepath.Join(f.workingDir, "sub.txt")
	childID := f.agentCall(t, sessionID, func(child string) {
		f.editCall(t, child, path, "before\n", "after\n", false)
	})

	preview, err := f.svc.Preview(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{path}, preview.Revert)

	result, err := f.svc.Undo(t.Context(), sessionID, preview.CutMessageID)
	require.NoError(t, err)
	require.Equal(t, []string{path}, result.Reverted)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "before\n", string(content))

	_, err = f.sessions.Get(t.Context(), childID)
	require.Error(t, err, "the subagent session must not outlive the turn that created it")
}

func TestUndoRejectsAStaleCutMessageID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	f.userMessage(t, sessionID, "hello")

	_, err := f.svc.Undo(t.Context(), sessionID, "not-the-real-cut-point")
	require.ErrorIs(t, err, ErrStale)
}

func TestPreviewReportsNothingToUndoOnAnEmptySession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	_, err := f.svc.Preview(t.Context(), sessionID)
	require.ErrorIs(t, err, ErrNothingToUndo)
}

func TestUndoRefusesWhenTheSessionItselfIsBusy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)
	f.userMessage(t, sessionID, "hello")

	f.busy.busy[sessionID] = true

	_, err := f.svc.Preview(t.Context(), sessionID)
	require.ErrorIs(t, err, ErrSessionBusy)

	_, err = f.svc.Undo(t.Context(), sessionID, "whatever")
	require.ErrorIs(t, err, ErrSessionBusy)
}

func TestUndoRefusesWhenASubagentSessionIsAnActiveBranch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sessionID := f.newSession(t)

	f.userMessage(t, sessionID, "delegate to a branch")
	path := filepath.Join(f.workingDir, "sub.txt")
	childID := f.agentCall(t, sessionID, func(child string) {
		f.editCall(t, child, path, "before\n", "after\n", false)
	})

	// The branch's own ToolResult is already recorded (agentCall always
	// finishes it), but the coordinator still reports it as a live
	// branch: e.g. this process still has the parent tool call
	// suspended pending a merge decision.
	f.busy.branch[childID] = true

	_, err := f.svc.Preview(t.Context(), sessionID)
	require.ErrorIs(t, err, ErrSessionBusy)

	_, err = f.svc.Undo(t.Context(), sessionID, "whatever")
	require.ErrorIs(t, err, ErrSessionBusy)

	// A refused undo must not touch anything: the file stays as the
	// branch left it, and its session survives.
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "after\n", string(content))
	_, err = f.sessions.Get(t.Context(), childID)
	require.NoError(t, err, "a busy child session must survive a refused undo")
}
