package server

import (
	"encoding/json"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/backend"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

// TestEnvelope_MarshalError verifies that a value json.Marshal cannot
// encode degrades to a nil payload instead of panicking or bubbling
// the error to the caller, which has no way to report it.
func TestEnvelope_MarshalError(t *testing.T) {
	t.Parallel()

	got := envelope(pubsub.PayloadTypeConfigChanged, make(chan int))

	require.Nil(t, got)
}

// TestAttachedClients verifies the nil-workspace guard and that a
// workspace with no attached clients reports zero.
func TestAttachedClients(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, attachedClients(nil, "s1"))

	ws := &backend.Workspace{ID: "w1", Path: t.TempDir()}
	require.Equal(t, 0, attachedClients(ws, "s1"))
}

func TestFileToProto(t *testing.T) {
	t.Parallel()

	src := history.File{
		ID:        "f1",
		SessionID: "s1",
		Path:      "/tmp/a.go",
		Content:   "package main",
		Version:   3,
		CreatedAt: 100,
		UpdatedAt: 200,
	}

	got := fileToProto(src)

	require.Equal(t, proto.File{
		ID:        "f1",
		SessionID: "s1",
		Path:      "/tmp/a.go",
		Content:   "package main",
		Version:   3,
		CreatedAt: 100,
		UpdatedAt: 200,
	}, got)
}

func TestMessagesToProto(t *testing.T) {
	t.Parallel()

	src := []message.Message{
		{ID: "m1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
		{ID: "m2", Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}},
	}

	got := messagesToProto(src)

	require.Len(t, got, 2)
	require.Equal(t, "m1", got[0].ID)
	require.Equal(t, proto.User, got[0].Role)
	require.Equal(t, "m2", got[1].ID)
	require.Equal(t, proto.Assistant, got[1].Role)
}

func TestMessagesToProto_Empty(t *testing.T) {
	t.Parallel()

	got := messagesToProto(nil)
	require.Empty(t, got)
}

func TestQuestionsToProto(t *testing.T) {
	t.Parallel()

	src := []question.Question{
		{
			ID:          "q1",
			Type:        question.TypeSingleChoice,
			Label:       "pick one",
			Text:        "Which?",
			Description: "desc",
			Choices: []question.Choice{
				{ID: "c1", Label: "Choice 1", Description: "first"},
				{ID: "c2", Label: "Choice 2"},
			},
		},
	}

	got := questionsToProto(src)

	require.Len(t, got, 1)
	require.Equal(t, "q1", got[0].ID)
	require.Equal(t, string(question.TypeSingleChoice), got[0].Type)
	require.Equal(t, "pick one", got[0].Label)
	require.Equal(t, "Which?", got[0].Question)
	require.Equal(t, "desc", got[0].Description)
	require.Len(t, got[0].Choices, 2)
	require.Equal(t, "c1", got[0].Choices[0].ID)
	require.Equal(t, "Choice 1", got[0].Choices[0].Label)
	require.Equal(t, "first", got[0].Choices[0].Description)
}

func TestQuestionsToProto_Empty(t *testing.T) {
	t.Parallel()

	require.Nil(t, questionsToProto(nil))
}

func TestTodosToProto_NonEmpty(t *testing.T) {
	t.Parallel()

	src := []session.Todo{
		{Content: "write tests", Status: session.TodoStatusInProgress, ActiveForm: "writing tests"},
		{Content: "ship it", Status: session.TodoStatusPending},
	}

	got := todosToProto(src)

	require.Len(t, got, 2)
	require.Equal(t, "write tests", got[0].Content)
	require.Equal(t, string(session.TodoStatusInProgress), got[0].Status)
	require.Equal(t, "writing tests", got[0].ActiveForm)
	require.Equal(t, "ship it", got[1].Content)
	require.Equal(t, string(session.TodoStatusPending), got[1].Status)
}

// TestMessageToProto_AllContentPartTypes exercises every message.ContentPart
// case in messageToProto beyond TextContent/ToolResult (already covered
// elsewhere), so a future part-type regression that drops fields during
// conversion is caught.
func TestMessageToProto_AllContentPartTypes(t *testing.T) {
	t.Parallel()

	src := message.Message{
		ID:   "m1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "hmm", Signature: "sig", StartedAt: 1, FinishedAt: 2},
			message.ToolCall{ID: "call-1", Name: "view", Input: `{"path":"a"}`, Finished: true},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 42, Message: "done", Details: "ok"},
			message.ImageURLContent{URL: "https://example.com/x.png", Detail: "high"},
			message.BinaryContent{Path: "/tmp/x.bin", MIMEType: "application/octet-stream", Data: []byte("data")},
			message.ShellCommand{Command: "ls -la", Output: "total 0", ExitCode: 0},
		},
	}

	got := messageToProto(src)
	require.Len(t, got.Parts, 6)

	reasoning, ok := got.Parts[0].(proto.ReasoningContent)
	require.True(t, ok, "expected proto.ReasoningContent, got %T", got.Parts[0])
	require.Equal(t, "hmm", reasoning.Thinking)
	require.Equal(t, "sig", reasoning.Signature)
	require.Equal(t, int64(1), reasoning.StartedAt)
	require.Equal(t, int64(2), reasoning.FinishedAt)

	toolCall, ok := got.Parts[1].(proto.ToolCall)
	require.True(t, ok, "expected proto.ToolCall, got %T", got.Parts[1])
	require.Equal(t, "call-1", toolCall.ID)
	require.Equal(t, "view", toolCall.Name)
	require.Equal(t, `{"path":"a"}`, toolCall.Input)
	require.True(t, toolCall.Finished)

	finish, ok := got.Parts[2].(proto.Finish)
	require.True(t, ok, "expected proto.Finish, got %T", got.Parts[2])
	require.Equal(t, proto.FinishReasonEndTurn, finish.Reason)
	require.Equal(t, int64(42), finish.Time)
	require.Equal(t, "done", finish.Message)
	require.Equal(t, "ok", finish.Details)

	imgURL, ok := got.Parts[3].(proto.ImageURLContent)
	require.True(t, ok, "expected proto.ImageURLContent, got %T", got.Parts[3])
	require.Equal(t, "https://example.com/x.png", imgURL.URL)
	require.Equal(t, "high", imgURL.Detail)

	binary, ok := got.Parts[4].(proto.BinaryContent)
	require.True(t, ok, "expected proto.BinaryContent, got %T", got.Parts[4])
	require.Equal(t, "/tmp/x.bin", binary.Path)
	require.Equal(t, "application/octet-stream", binary.MIMEType)
	require.Equal(t, []byte("data"), binary.Data)

	shell, ok := got.Parts[5].(proto.ShellCommand)
	require.True(t, ok, "expected proto.ShellCommand, got %T", got.Parts[5])
	require.Equal(t, "ls -la", shell.Command)
	require.Equal(t, "total 0", shell.Output)
	require.Equal(t, 0, shell.ExitCode)
}

func TestMcpEventTypeToProto_KnownTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   mcp.EventType
		want proto.MCPEventType
	}{
		{name: "state changed", in: mcp.EventStateChanged, want: proto.MCPEventStateChanged},
		{name: "tools list changed", in: mcp.EventToolsListChanged, want: proto.MCPEventToolsListChanged},
		{name: "prompts list changed", in: mcp.EventPromptsListChanged, want: proto.MCPEventPromptsListChanged},
		{name: "resources list changed", in: mcp.EventResourcesListChanged, want: proto.MCPEventResourcesListChanged},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, mcpEventTypeToProto(tt.in))
		})
	}
}

// TestWrapEvent_RemainingBranches exercises every wrapEvent case not
// already covered by events_test.go / sessions_isbusy_test.go, plus the
// default (unrecognized type) branch.
func TestWrapEvent_RemainingBranches(t *testing.T) {
	t.Parallel()

	t.Run("LSPEvent", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[app.LSPEvent]{
			Type: pubsub.UpdatedEvent,
			Payload: app.LSPEvent{
				Type:            app.LSPEventDiagnosticsChanged,
				Name:            "gopls",
				DiagnosticCount: 3,
			},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeLSPEvent, env.Type)

		var decoded pubsub.Event[proto.LSPEvent]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, proto.LSPEventDiagnosticsChanged, decoded.Payload.Type)
		require.Equal(t, "gopls", decoded.Payload.Name)
		require.Equal(t, 3, decoded.Payload.DiagnosticCount)
	})

	t.Run("MCPEvent known type", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[mcp.Event]{
			Type: pubsub.UpdatedEvent,
			Payload: mcp.Event{
				Type:   mcp.EventToolsListChanged,
				Name:   "docker",
				State:  mcp.StateConnected,
				Counts: mcp.Counts{Tools: 5},
			},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeMCPEvent, env.Type)

		var decoded pubsub.Event[proto.MCPEvent]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, proto.MCPEventToolsListChanged, decoded.Payload.Type)
		require.Equal(t, "docker", decoded.Payload.Name)
		require.Equal(t, proto.MCPStateConnected, decoded.Payload.State)
		require.Equal(t, 5, decoded.Payload.ToolCount)
	})

	t.Run("PermissionRequest", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[permission.PermissionRequest]{
			Type: pubsub.CreatedEvent,
			Payload: permission.PermissionRequest{
				ID:          "p1",
				SessionID:   "s1",
				ToolCallID:  "tc1",
				ToolName:    "edit",
				Description: "edit a file",
				Action:      "write",
				Path:        "/tmp/x",
			},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypePermissionRequest, env.Type)

		var decoded pubsub.Event[proto.PermissionRequest]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "p1", decoded.Payload.ID)
		require.Equal(t, "s1", decoded.Payload.SessionID)
		require.Equal(t, "tc1", decoded.Payload.ToolCallID)
		require.Equal(t, "edit", decoded.Payload.ToolName)
		require.Equal(t, "/tmp/x", decoded.Payload.Path)
	})

	t.Run("PermissionNotification", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[permission.PermissionNotification]{
			Type: pubsub.UpdatedEvent,
			Payload: permission.PermissionNotification{
				ToolCallID: "tc1",
				Granted:    true,
			},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypePermissionNotification, env.Type)

		var decoded pubsub.Event[proto.PermissionNotification]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "tc1", decoded.Payload.ToolCallID)
		require.True(t, decoded.Payload.Granted)
		require.False(t, decoded.Payload.Denied)
	})

	t.Run("QuestionRequest", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[question.Request]{
			Type: pubsub.CreatedEvent,
			Payload: question.Request{
				ID:                 "batch-1",
				SessionID:          "s1",
				ToolCallID:         "tc1",
				Questions:          []question.Question{{ID: "q1", Type: question.TypeYesNo, Text: "Continue?"}},
				ConfirmTitle:       "Confirm",
				ConfirmDescription: "Please confirm",
			},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeQuestionRequest, env.Type)

		var decoded pubsub.Event[proto.QuestionRequest]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "batch-1", decoded.Payload.ID)
		require.Equal(t, "s1", decoded.Payload.SessionID)
		require.Len(t, decoded.Payload.Questions, 1)
		require.Equal(t, "q1", decoded.Payload.Questions[0].ID)
		require.Equal(t, "Confirm", decoded.Payload.ConfirmTitle)
	})

	t.Run("QuestionNotification", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[question.Notification]{
			Type:    pubsub.UpdatedEvent,
			Payload: question.Notification{BatchID: "batch-1"},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeQuestionNotification, env.Type)

		var decoded pubsub.Event[proto.QuestionNotification]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "batch-1", decoded.Payload.BatchID)
	})

	t.Run("Message", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[message.Message]{
			Type: pubsub.CreatedEvent,
			Payload: message.Message{
				ID:   "m1",
				Role: message.User,
				Parts: []message.ContentPart{
					message.TextContent{Text: "hi"},
				},
			},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeMessage, env.Type)

		var decoded pubsub.Event[proto.Message]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "m1", decoded.Payload.ID)
		require.Equal(t, proto.User, decoded.Payload.Role)
	})

	t.Run("Session", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[session.Session]{
			Type:    pubsub.UpdatedEvent,
			Payload: session.Session{ID: "s1", Title: "my session"},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeSession, env.Type)

		var decoded pubsub.Event[proto.Session]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "s1", decoded.Payload.ID)
		require.Equal(t, "my session", decoded.Payload.Title)
	})

	t.Run("File", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[history.File]{
			Type:    pubsub.CreatedEvent,
			Payload: history.File{ID: "f1", SessionID: "s1", Path: "/tmp/x"},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeFile, env.Type)

		var decoded pubsub.Event[proto.File]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "f1", decoded.Payload.ID)
		require.Equal(t, "/tmp/x", decoded.Payload.Path)
	})

	t.Run("ConfigChanged", func(t *testing.T) {
		t.Parallel()
		src := pubsub.Event[proto.ConfigChanged]{
			Type:    pubsub.UpdatedEvent,
			Payload: proto.ConfigChanged{WorkspaceID: "ws1"},
		}
		env := wrapEvent(src)
		require.NotNil(t, env)
		require.Equal(t, pubsub.PayloadTypeConfigChanged, env.Type)

		var decoded pubsub.Event[proto.ConfigChanged]
		require.NoError(t, json.Unmarshal(env.Payload, &decoded))
		require.Equal(t, "ws1", decoded.Payload.WorkspaceID)
	})

	t.Run("unrecognized type", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, wrapEvent("not a known event type"))
		require.Nil(t, wrapEvent(42))
	})
}
