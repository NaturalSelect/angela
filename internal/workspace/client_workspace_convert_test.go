package workspace

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/undo"
	"github.com/stretchr/testify/require"
)

func TestProtoToMCPEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   proto.MCPEventType
		want mcp.EventType
	}{
		{"state changed", proto.MCPEventStateChanged, mcp.EventStateChanged},
		{"tools list changed", proto.MCPEventToolsListChanged, mcp.EventToolsListChanged},
		{"prompts list changed", proto.MCPEventPromptsListChanged, mcp.EventPromptsListChanged},
		{"resources list changed", proto.MCPEventResourcesListChanged, mcp.EventResourcesListChanged},
		{"unknown falls back to state changed", proto.MCPEventType("bogus"), mcp.EventStateChanged},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, protoToMCPEventType(tc.in))
		})
	}
}

// TestProtoToMessage_AllPartTypes exercises every proto.ContentPart
// variant protoToMessage switches on, since the existing
// TestProtoToMessageToolResult only covers ToolResult.
func TestProtoToMessage_AllPartTypes(t *testing.T) {
	t.Parallel()

	src := proto.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      proto.Assistant,
		Model:     "gpt-5",
		Provider:  "openai",
		CreatedAt: 100,
		UpdatedAt: 200,
		Parts: []proto.ContentPart{
			proto.TextContent{Text: "hello"},
			proto.ReasoningContent{Thinking: "thinking...", Signature: "sig", StartedAt: 1, FinishedAt: 2},
			proto.ToolCall{ID: "call-1", Name: "bash", Input: `{"cmd":"ls"}`, Type: "function", Finished: true},
			proto.ToolResult{ToolCallID: "call-1", Name: "bash", Content: "out", Data: "d", MIMEType: "text/plain", Metadata: "{}", IsError: true},
			proto.Finish{Reason: proto.FinishReasonEndTurn, Time: 3, Message: "done", Details: "detail"},
			proto.ImageURLContent{URL: "http://x/img.png", Detail: "high"},
			proto.BinaryContent{Path: "/tmp/f", MIMEType: "image/png", Data: []byte{1, 2, 3}},
			proto.ShellCommand{Command: "ls", Output: "out", ExitCode: 1},
		},
		IsSummaryMessage: true,
	}

	got := protoToMessage(src)
	require.Equal(t, "m1", got.ID)
	require.Equal(t, "s1", got.SessionID)
	require.Equal(t, message.MessageRole(proto.Assistant), got.Role)
	require.Equal(t, "gpt-5", got.Model)
	require.Equal(t, "openai", got.Provider)
	require.Equal(t, int64(100), got.CreatedAt)
	require.Equal(t, int64(200), got.UpdatedAt)
	require.True(t, got.IsSummaryMessage)
	require.Len(t, got.Parts, 8)

	require.Equal(t, message.TextContent{Text: "hello"}, got.Parts[0])
	require.Equal(t, message.ReasoningContent{Thinking: "thinking...", Signature: "sig", StartedAt: 1, FinishedAt: 2}, got.Parts[1])
	require.Equal(t, message.ToolCall{ID: "call-1", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true}, got.Parts[2])
	require.Equal(t, message.ToolResult{ToolCallID: "call-1", Name: "bash", Content: "out", Data: "d", MIMEType: "text/plain", Metadata: "{}", IsError: true}, got.Parts[3])
	require.Equal(t, message.Finish{Reason: message.FinishReason(proto.FinishReasonEndTurn), Time: 3, Message: "done", Details: "detail"}, got.Parts[4])
	require.Equal(t, message.ImageURLContent{URL: "http://x/img.png", Detail: "high"}, got.Parts[5])
	require.Equal(t, message.BinaryContent{Path: "/tmp/f", MIMEType: "image/png", Data: []byte{1, 2, 3}}, got.Parts[6])
	require.Equal(t, message.ShellCommand{Command: "ls", Output: "out", ExitCode: 1}, got.Parts[7])
}

// TestProtoToMessages_EmptyIsNeverNil pins that, unlike protoToTodos and
// friends, protoToMessages always returns a non-nil slice (make with
// len, no nil guard), since callers may range over it unconditionally.
func TestProtoToMessages_EmptyIsNeverNil(t *testing.T) {
	t.Parallel()

	got := protoToMessages(nil)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestProtoToMessages(t *testing.T) {
	t.Parallel()

	in := []proto.Message{
		{ID: "m1", Role: proto.User},
		{ID: "m2", Role: proto.Assistant},
	}
	got := protoToMessages(in)
	require.Len(t, got, 2)
	require.Equal(t, "m1", got[0].ID)
	require.Equal(t, "m2", got[1].ID)
}

func TestProtoToSession(t *testing.T) {
	t.Parallel()

	src := proto.Session{
		ID:               "s1",
		ParentSessionID:  "p1",
		Title:            "title",
		Agent:            "coder",
		ActiveAgent:      config.ActiveAgentState{Agent: "coder"},
		MessageCount:     5,
		PromptTokens:     10,
		CompletionTokens: 20,
		SummaryMessageID: "sm1",
		Cost:             1.5,
		Todos: []proto.Todo{
			{Content: "do it", Status: "pending", ActiveForm: "doing it"},
		},
		CreatedAt: 100,
		UpdatedAt: 200,
		// IsBusy and AttachedClients are wire-only and must not survive.
		IsBusy:          true,
		AttachedClients: 3,
	}

	want := session.Session{
		ID:               "s1",
		ParentSessionID:  "p1",
		Title:            "title",
		Agent:            "coder",
		ActiveAgent:      config.ActiveAgentState{Agent: "coder"},
		SummaryMessageID: "sm1",
		MessageCount:     5,
		PromptTokens:     10,
		CompletionTokens: 20,
		Cost:             1.5,
		Todos: []session.Todo{
			{Content: "do it", Status: session.TodoStatusPending, ActiveForm: "doing it"},
		},
		CreatedAt: 100,
		UpdatedAt: 200,
	}

	require.Equal(t, want, protoToSession(src))
}

func TestProtoToTodos(t *testing.T) {
	t.Parallel()

	t.Run("nil for empty input", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, protoToTodos(nil))
		require.Nil(t, protoToTodos([]proto.Todo{}))
	})

	t.Run("maps every field", func(t *testing.T) {
		t.Parallel()
		in := []proto.Todo{
			{Content: "a", Status: "pending", ActiveForm: "doing a"},
			{Content: "b", Status: "completed", ActiveForm: "doing b"},
		}
		got := protoToTodos(in)
		require.Equal(t, []session.Todo{
			{Content: "a", Status: session.TodoStatusPending, ActiveForm: "doing a"},
			{Content: "b", Status: session.TodoStatusCompleted, ActiveForm: "doing b"},
		}, got)
	})
}

func TestProtoToFile(t *testing.T) {
	t.Parallel()

	src := proto.File{
		ID:        "f1",
		SessionID: "s1",
		Path:      "/tmp/x",
		Content:   "hi",
		Version:   3,
		CreatedAt: 100,
		UpdatedAt: 200,
	}
	want := history.File{
		ID:        "f1",
		SessionID: "s1",
		Path:      "/tmp/x",
		Content:   "hi",
		Version:   3,
		CreatedAt: 100,
		UpdatedAt: 200,
	}
	require.Equal(t, want, protoToFile(src))
}

func TestProtoToFiles(t *testing.T) {
	t.Parallel()

	t.Run("empty is never nil", func(t *testing.T) {
		t.Parallel()
		got := protoToFiles(nil)
		require.NotNil(t, got)
		require.Empty(t, got)
	})

	t.Run("maps every entry", func(t *testing.T) {
		t.Parallel()
		in := []proto.File{{ID: "f1"}, {ID: "f2"}}
		got := protoToFiles(in)
		require.Len(t, got, 2)
		require.Equal(t, "f1", got[0].ID)
		require.Equal(t, "f2", got[1].ID)
	})
}

func TestProtoToUndoSkippedFiles(t *testing.T) {
	t.Parallel()

	t.Run("nil for empty input", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, protoToUndoSkippedFiles(nil))
	})

	t.Run("maps every field", func(t *testing.T) {
		t.Parallel()
		in := []proto.UndoSkippedFile{{Path: "/a", Reason: "busy"}}
		want := []undo.SkippedFile{{Path: "/a", Reason: "busy"}}
		require.Equal(t, want, protoToUndoSkippedFiles(in))
	})
}

func TestProtoToUndoPreview(t *testing.T) {
	t.Parallel()

	src := proto.UndoPreview{
		CutMessageID: "m1",
		PoppedText:   "popped",
		MessageCount: 2,
		Revert:       []string{"/a"},
		Delete:       []string{"/b"},
		Skipped:      []proto.UndoSkippedFile{{Path: "/c", Reason: "locked"}},
	}
	want := undo.Preview{
		CutMessageID: "m1",
		PoppedText:   "popped",
		MessageCount: 2,
		Revert:       []string{"/a"},
		Delete:       []string{"/b"},
		Skipped:      []undo.SkippedFile{{Path: "/c", Reason: "locked"}},
	}
	require.Equal(t, want, protoToUndoPreview(src))
}

func TestProtoToUndoResult(t *testing.T) {
	t.Parallel()

	src := proto.UndoResult{
		PoppedText:   "popped",
		Reverted:     []string{"/a"},
		Deleted:      []string{"/b"},
		Skipped:      []proto.UndoSkippedFile{{Path: "/c", Reason: "locked"}},
		MessageCount: 2,
	}
	want := undo.Result{
		PoppedText:   "popped",
		Reverted:     []string{"/a"},
		Deleted:      []string{"/b"},
		Skipped:      []undo.SkippedFile{{Path: "/c", Reason: "locked"}},
		MessageCount: 2,
	}
	require.Equal(t, want, protoToUndoResult(src))
}

func TestSessionToProto(t *testing.T) {
	t.Parallel()

	src := session.Session{
		ID:               "s1",
		ParentSessionID:  "p1",
		Title:            "title",
		Agent:            "coder",
		ActiveAgent:      config.ActiveAgentState{Agent: "coder"},
		SummaryMessageID: "sm1",
		MessageCount:     5,
		PromptTokens:     10,
		CompletionTokens: 20,
		Cost:             1.5,
		Todos: []session.Todo{
			{Content: "do it", Status: session.TodoStatusInProgress, ActiveForm: "doing it"},
		},
		CreatedAt: 100,
		UpdatedAt: 200,
	}

	want := proto.Session{
		ID:               "s1",
		ParentSessionID:  "p1",
		Title:            "title",
		Agent:            "coder",
		ActiveAgent:      config.ActiveAgentState{Agent: "coder"},
		SummaryMessageID: "sm1",
		MessageCount:     5,
		PromptTokens:     10,
		CompletionTokens: 20,
		Cost:             1.5,
		Todos: []proto.Todo{
			{Content: "do it", Status: "in_progress", ActiveForm: "doing it"},
		},
		CreatedAt: 100,
		UpdatedAt: 200,
	}

	require.Equal(t, want, sessionToProto(src))
}

func TestTodosToProto(t *testing.T) {
	t.Parallel()

	t.Run("nil for empty input", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, todosToProto(nil))
	})

	t.Run("maps every field", func(t *testing.T) {
		t.Parallel()
		in := []session.Todo{{Content: "a", Status: session.TodoStatusCompleted, ActiveForm: "doing a"}}
		want := []proto.Todo{{Content: "a", Status: "completed", ActiveForm: "doing a"}}
		require.Equal(t, want, todosToProto(in))
	})
}

func TestProtoQuestionsToDomain(t *testing.T) {
	t.Parallel()

	t.Run("nil for empty input", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, protoQuestionsToDomain(nil))
	})

	t.Run("maps every field including nested choices", func(t *testing.T) {
		t.Parallel()
		in := []proto.QuestionItem{
			{
				ID:          "q1",
				Type:        "single_select",
				Label:       "label",
				Question:    "pick one",
				Description: "desc",
				Choices: []proto.QuestionChoice{
					{ID: "c1", Label: "Choice 1", Description: "first"},
					{ID: "c2", Label: "Choice 2", Description: "second"},
				},
			},
		}
		want := []question.Question{
			{
				ID:          "q1",
				Type:        question.Type("single_select"),
				Label:       "label",
				Text:        "pick one",
				Description: "desc",
				Choices: []question.Choice{
					{ID: "c1", Label: "Choice 1", Description: "first"},
					{ID: "c2", Label: "Choice 2", Description: "second"},
				},
			},
		}
		require.Equal(t, want, protoQuestionsToDomain(in))
	})
}

// TestTranslateEvent table-tests every proto event type
// (*ClientWorkspace).translateEvent switches on. Skills and
// UpdateAvailable already have dedicated tests covering their side
// effects, so they are omitted here to avoid duplicate coverage.
func TestTranslateEvent(t *testing.T) {
	t.Parallel()

	w := NewClientWorkspace(nil, proto.Workspace{})
	boom := errors.New("boom")

	tests := []struct {
		name  string
		event any
		check func(t *testing.T, out tea.Msg)
	}{
		{
			name: "LSPEvent",
			event: pubsub.Event[proto.LSPEvent]{
				Type: pubsub.UpdatedEvent,
				Payload: proto.LSPEvent{
					Type:            proto.LSPEventDiagnosticsChanged,
					Name:            "gopls",
					State:           lsp.StateReady,
					Error:           boom,
					DiagnosticCount: 4,
				},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[LSPEvent])
				require.True(t, ok, "expected pubsub.Event[LSPEvent], got %T", out)
				require.Equal(t, pubsub.UpdatedEvent, got.Type)
				require.Equal(t, LSPEventDiagnosticsChanged, got.Payload.Type)
				require.Equal(t, "gopls", got.Payload.Name)
				require.Equal(t, lsp.StateReady, got.Payload.State)
				require.Equal(t, boom, got.Payload.Error)
				require.Equal(t, 4, got.Payload.DiagnosticCount)
			},
		},
		{
			name: "MCPEvent",
			event: pubsub.Event[proto.MCPEvent]{
				Type: pubsub.UpdatedEvent,
				Payload: proto.MCPEvent{
					Type:          proto.MCPEventToolsListChanged,
					Name:          "server",
					State:         proto.MCPStateConnected,
					Error:         boom,
					ToolCount:     1,
					PromptCount:   2,
					ResourceCount: 3,
				},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[mcp.Event])
				require.True(t, ok, "expected pubsub.Event[mcp.Event], got %T", out)
				require.Equal(t, mcp.EventToolsListChanged, got.Payload.Type)
				require.Equal(t, "server", got.Payload.Name)
				require.Equal(t, mcp.State(proto.MCPStateConnected), got.Payload.State)
				require.Equal(t, boom, got.Payload.Error)
				require.Equal(t, mcp.Counts{Tools: 1, Prompts: 2, Resources: 3}, got.Payload.Counts)
			},
		},
		{
			name: "PermissionRequest",
			event: pubsub.Event[proto.PermissionRequest]{
				Type: pubsub.CreatedEvent,
				Payload: proto.PermissionRequest{
					ID: "req-1", SessionID: "s1", ToolCallID: "tc1",
					ToolName: "bash", Description: "run", Action: "run", Path: "/tmp",
				},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[permission.PermissionRequest])
				require.True(t, ok, "expected pubsub.Event[permission.PermissionRequest], got %T", out)
				require.Equal(t, "req-1", got.Payload.ID)
				require.Equal(t, "s1", got.Payload.SessionID)
				require.Equal(t, "tc1", got.Payload.ToolCallID)
				require.Equal(t, "bash", got.Payload.ToolName)
				require.Equal(t, "run", got.Payload.Description)
				require.Equal(t, "run", got.Payload.Action)
				require.Equal(t, "/tmp", got.Payload.Path)
			},
		},
		{
			name: "PermissionNotification",
			event: pubsub.Event[proto.PermissionNotification]{
				Type:    pubsub.UpdatedEvent,
				Payload: proto.PermissionNotification{ToolCallID: "tc1", Granted: true, Denied: false},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[permission.PermissionNotification])
				require.True(t, ok, "expected pubsub.Event[permission.PermissionNotification], got %T", out)
				require.Equal(t, "tc1", got.Payload.ToolCallID)
				require.True(t, got.Payload.Granted)
				require.False(t, got.Payload.Denied)
			},
		},
		{
			name: "QuestionRequest",
			event: pubsub.Event[proto.QuestionRequest]{
				Type: pubsub.CreatedEvent,
				Payload: proto.QuestionRequest{
					ID: "q1", SessionID: "s1", ToolCallID: "tc1",
					Questions:          []proto.QuestionItem{{ID: "i1", Type: "text", Question: "why?"}},
					ConfirmTitle:       "confirm",
					ConfirmDescription: "desc",
				},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[question.Request])
				require.True(t, ok, "expected pubsub.Event[question.Request], got %T", out)
				require.Equal(t, "q1", got.Payload.ID)
				require.Equal(t, "s1", got.Payload.SessionID)
				require.Equal(t, "tc1", got.Payload.ToolCallID)
				require.Len(t, got.Payload.Questions, 1)
				require.Equal(t, "i1", got.Payload.Questions[0].ID)
				require.Equal(t, "confirm", got.Payload.ConfirmTitle)
				require.Equal(t, "desc", got.Payload.ConfirmDescription)
			},
		},
		{
			name: "QuestionNotification",
			event: pubsub.Event[proto.QuestionNotification]{
				Type:    pubsub.UpdatedEvent,
				Payload: proto.QuestionNotification{BatchID: "b1"},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[question.Notification])
				require.True(t, ok, "expected pubsub.Event[question.Notification], got %T", out)
				require.Equal(t, "b1", got.Payload.BatchID)
			},
		},
		{
			name: "Message",
			event: pubsub.Event[proto.Message]{
				Type:    pubsub.UpdatedEvent,
				Payload: proto.Message{ID: "m1", Role: proto.User},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[message.Message])
				require.True(t, ok, "expected pubsub.Event[message.Message], got %T", out)
				require.Equal(t, "m1", got.Payload.ID)
			},
		},
		{
			name: "Session",
			event: pubsub.Event[proto.Session]{
				Type:    pubsub.UpdatedEvent,
				Payload: proto.Session{ID: "s1", Title: "t"},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[session.Session])
				require.True(t, ok, "expected pubsub.Event[session.Session], got %T", out)
				require.Equal(t, "s1", got.Payload.ID)
				require.Equal(t, "t", got.Payload.Title)
			},
		},
		{
			name: "File",
			event: pubsub.Event[proto.File]{
				Type:    pubsub.CreatedEvent,
				Payload: proto.File{ID: "f1", Path: "/tmp/x"},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[history.File])
				require.True(t, ok, "expected pubsub.Event[history.File], got %T", out)
				require.Equal(t, "f1", got.Payload.ID)
				require.Equal(t, "/tmp/x", got.Payload.Path)
			},
		},
		{
			name: "AgentEvent with error",
			event: pubsub.Event[proto.AgentEvent]{
				Type: pubsub.UpdatedEvent,
				Payload: proto.AgentEvent{
					Type: proto.AgentEventTypeError, SessionID: "s1", SessionTitle: "title",
					RunID: "r1", Error: boom, AWSSOCommand: "cmd", AWSSOURL: "url",
				},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[notify.Notification])
				require.True(t, ok, "expected pubsub.Event[notify.Notification], got %T", out)
				require.Equal(t, "s1", got.Payload.SessionID)
				require.Equal(t, "title", got.Payload.SessionTitle)
				require.Equal(t, "r1", got.Payload.RunID)
				require.Equal(t, notify.Type(proto.AgentEventTypeError), got.Payload.Type)
				require.Equal(t, "boom", got.Payload.Message)
				require.Equal(t, "cmd", got.Payload.AWSSOCommand)
				require.Equal(t, "url", got.Payload.AWSSOURL)
			},
		},
		{
			name: "AgentEvent without error leaves Message empty",
			event: pubsub.Event[proto.AgentEvent]{
				Type:    pubsub.UpdatedEvent,
				Payload: proto.AgentEvent{Type: proto.AgentEventTypeResponse, SessionID: "s1"},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[notify.Notification])
				require.True(t, ok, "expected pubsub.Event[notify.Notification], got %T", out)
				require.Empty(t, got.Payload.Message)
			},
		},
		{
			name: "RunComplete",
			event: pubsub.Event[proto.RunComplete]{
				Type: pubsub.UpdatedEvent,
				Payload: proto.RunComplete{
					SessionID: "s1", RunID: "r1", MessageID: "m1",
					Text: "done", Error: "oops", Cancelled: true,
				},
			},
			check: func(t *testing.T, out tea.Msg) {
				got, ok := out.(pubsub.Event[notify.RunComplete])
				require.True(t, ok, "expected pubsub.Event[notify.RunComplete], got %T", out)
				require.Equal(t, "s1", got.Payload.SessionID)
				require.Equal(t, "r1", got.Payload.RunID)
				require.Equal(t, "m1", got.Payload.MessageID)
				require.Equal(t, "done", got.Payload.Text)
				require.Equal(t, "oops", got.Payload.Error)
				require.True(t, got.Payload.Cancelled)
			},
		},
		{
			name:  "unknown event type falls back to nil",
			event: "not a pubsub event",
			check: func(t *testing.T, out tea.Msg) {
				require.Nil(t, out)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, w.translateEvent(tc.event))
		})
	}
}
