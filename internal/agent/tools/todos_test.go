package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestTodosTool_RequiresSession pins that a call outside a session
// fails fast with a plain error rather than reaching the session
// store at all.
func TestTodosTool_RequiresSession(t *testing.T) {
	t.Parallel()

	tool := NewTodosTool(NewMockSessionService(gomock.NewController(t)))

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"todos":[]}`})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
	require.Zero(t, resp)
}

// TestTodosTool_GetSessionError pins that a lookup failure is wrapped
// with enough context to say what failed, rather than surfaced bare.
func TestTodosTool_GetSessionError(t *testing.T) {
	t.Parallel()

	sessions := NewMockSessionService(gomock.NewController(t))
	sessions.EXPECT().Get(gomock.Any(), "session-1").Return(session.Session{}, errors.New("no such session"))

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"todos":[]}`})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get session")
	require.Zero(t, resp)
}

// TestTodosTool_InvalidStatus pins that an unrecognized status is
// rejected before the session is ever saved, so a malformed call
// cannot overwrite a valid todo list with a partially-built one.
func TestTodosTool_InvalidStatus(t *testing.T) {
	t.Parallel()

	sessions := NewMockSessionService(gomock.NewController(t))
	sessions.EXPECT().Get(gomock.Any(), "session-1").Return(session.Session{ID: "session-1"}, nil)
	// No Save expectation: it must not be called.

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	input, err := json.Marshal(TodosParams{Todos: []TodoItem{{Content: "task", Status: "bogus"}}})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid status "bogus"`)
	require.Zero(t, resp)
}

// TestTodosTool_SaveError pins that a save failure is wrapped with
// enough context to say what failed.
func TestTodosTool_SaveError(t *testing.T) {
	t.Parallel()

	sessions := NewMockSessionService(gomock.NewController(t))
	sessions.EXPECT().Get(gomock.Any(), "session-1").Return(session.Session{ID: "session-1"}, nil)
	sessions.EXPECT().Save(gomock.Any(), gomock.Any()).Return(session.Session{}, errors.New("disk full"))

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	input, err := json.Marshal(TodosParams{Todos: []TodoItem{{Content: "task", Status: "pending"}}})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to save todos")
	require.Zero(t, resp)
}

// TestTodosTool_CreateNewList pins the first-ever write: IsNew is
// set, and starting an in_progress todo with no prior state marks it
// JustStarted.
func TestTodosTool_CreateNewList(t *testing.T) {
	t.Parallel()

	sessions := NewMockSessionService(gomock.NewController(t))
	sessions.EXPECT().Get(gomock.Any(), "session-1").Return(session.Session{ID: "session-1"}, nil)
	sessions.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, s session.Session) (session.Session, error) { return s, nil },
	)

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	input, err := json.Marshal(TodosParams{Todos: []TodoItem{
		{Content: "write tests", Status: "in_progress", ActiveForm: "Writing tests"},
		{Content: "ship it", Status: "pending"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Status: 1 pending, 1 in progress, 0 completed")

	var meta TodosResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.IsNew)
	require.Equal(t, "Writing tests", meta.JustStarted)
	require.Empty(t, meta.JustCompleted)
	require.Equal(t, 0, meta.Completed)
	require.Equal(t, 2, meta.Total)
}

// TestTodosTool_JustCompletedTransition pins that a todo moving from
// in_progress to completed is reported in JustCompleted, and counted.
func TestTodosTool_JustCompletedTransition(t *testing.T) {
	t.Parallel()

	sessions := NewMockSessionService(gomock.NewController(t))
	sessions.EXPECT().Get(gomock.Any(), "session-1").Return(session.Session{
		ID:    "session-1",
		Todos: []session.Todo{{Content: "write tests", Status: session.TodoStatusInProgress}},
	}, nil)
	sessions.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, s session.Session) (session.Session, error) { return s, nil },
	)

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	input, err := json.Marshal(TodosParams{Todos: []TodoItem{
		{Content: "write tests", Status: "completed"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)

	var meta TodosResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.IsNew)
	require.Equal(t, []string{"write tests"}, meta.JustCompleted)
	require.Equal(t, 1, meta.Completed)
	require.Equal(t, 1, meta.Total)
}

// TestTodosTool_JustStartedOnlyOnTransition pins that a todo already
// in_progress before the call is not reported as JustStarted again on
// a call that merely restates it, so the UI does not replay the
// "started" notification on every unrelated update.
func TestTodosTool_JustStartedOnlyOnTransition(t *testing.T) {
	t.Parallel()

	sessions := NewMockSessionService(gomock.NewController(t))
	sessions.EXPECT().Get(gomock.Any(), "session-1").Return(session.Session{
		ID:    "session-1",
		Todos: []session.Todo{{Content: "write tests", Status: session.TodoStatusInProgress}},
	}, nil)
	sessions.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, s session.Session) (session.Session, error) { return s, nil },
	)

	tool := NewTodosTool(sessions)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")

	input, err := json.Marshal(TodosParams{Todos: []TodoItem{
		{Content: "write tests", Status: "in_progress"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)

	var meta TodosResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Empty(t, meta.JustStarted)
}
