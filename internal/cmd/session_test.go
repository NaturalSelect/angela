package cmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestResolveSessionID_DirectUUIDMatch(t *testing.T) {
	t.Parallel()

	m := NewMockSessionService(gomock.NewController(t))
	want := session.Session{ID: "session-uuid-1", Title: "Direct"}
	m.EXPECT().Get(gomock.Any(), "session-uuid-1").Return(want, nil)

	got, err := resolveSessionID(t.Context(), m, "session-uuid-1")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolveSessionID_HashExactMatch(t *testing.T) {
	t.Parallel()

	m := NewMockSessionService(gomock.NewController(t))
	target := session.Session{ID: "session-uuid-2", Title: "Hash match"}
	m.EXPECT().Get(gomock.Any(), gomock.Any()).Return(session.Session{}, sql.ErrNoRows)
	m.EXPECT().List(gomock.Any()).Return([]session.Session{target}, nil)

	got, err := resolveSessionID(t.Context(), m, session.HashID(target.ID))
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolveSessionID_HashPrefixMatch(t *testing.T) {
	t.Parallel()

	m := NewMockSessionService(gomock.NewController(t))
	target := session.Session{ID: "session-uuid-3", Title: "Prefix match"}
	m.EXPECT().Get(gomock.Any(), gomock.Any()).Return(session.Session{}, sql.ErrNoRows)
	m.EXPECT().List(gomock.Any()).Return([]session.Session{target}, nil)

	hash := session.HashID(target.ID)
	got, err := resolveSessionID(t.Context(), m, hash[:6])
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolveSessionID_AmbiguousPrefixMatch(t *testing.T) {
	t.Parallel()

	m := NewMockSessionService(gomock.NewController(t))
	sessions := []session.Session{
		{ID: "session-uuid-4", Title: "First", CreatedAt: 1000},
		{ID: "session-uuid-5", Title: "Second", CreatedAt: 2000},
	}
	m.EXPECT().Get(gomock.Any(), gomock.Any()).Return(session.Session{}, sql.ErrNoRows)
	m.EXPECT().List(gomock.Any()).Return(sessions, nil)

	// Every hash has "" as a prefix, so an empty query matches all sessions
	// and forces the ambiguous branch without needing a real hash collision.
	_, err := resolveSessionID(t.Context(), m, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is ambiguous")
	require.Contains(t, err.Error(), "First")
	require.Contains(t, err.Error(), "Second")
}

func TestResolveSessionID_NotFound(t *testing.T) {
	t.Parallel()

	m := NewMockSessionService(gomock.NewController(t))
	m.EXPECT().Get(gomock.Any(), gomock.Any()).Return(session.Session{}, sql.ErrNoRows)
	m.EXPECT().List(gomock.Any()).Return(nil, nil)

	_, err := resolveSessionID(t.Context(), m, "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found: missing")
}

func TestResolveSessionID_ListErrorPropagates(t *testing.T) {
	t.Parallel()

	m := NewMockSessionService(gomock.NewController(t))
	wantErr := errors.New("db exploded")
	m.EXPECT().Get(gomock.Any(), gomock.Any()).Return(session.Session{}, sql.ErrNoRows)
	m.EXPECT().List(gomock.Any()).Return(nil, wantErr)

	_, err := resolveSessionID(t.Context(), m, "missing")
	require.ErrorIs(t, err, wantErr)
}

func TestMessagePtrs(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{{ID: "a"}, {ID: "b"}}
	ptrs := messagePtrs(msgs)

	require.Len(t, ptrs, 2)
	require.Equal(t, "a", ptrs[0].ID)
	require.Equal(t, "b", ptrs[1].ID)
	require.Same(t, &msgs[0], ptrs[0])
	require.Same(t, &msgs[1], ptrs[1])
}

func TestIsBrokenPipe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"raw epipe", syscall.EPIPE, true},
		{"wrapped epipe", errors.New("write: " + syscall.EPIPE.Error()), true},
		{"broken pipe message", errors.New("write tcp: broken pipe"), true},
		{"unrelated error", errors.New("disk full"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, isBrokenPipe(tt.err))
		})
	}
}

func TestConvertParts_KnownTypes(t *testing.T) {
	t.Parallel()

	parts := []message.ContentPart{
		message.TextContent{Text: "hello"},
		message.ReasoningContent{Thinking: "thinking...", StartedAt: 10, FinishedAt: 20},
		message.ToolCall{ID: "tc1", Name: "bash", Input: `{"command":"ls"}`},
		message.ToolResult{ToolCallID: "tc1", Name: "bash", Content: "out", IsError: true, MIMEType: "text/plain"},
		message.BinaryContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		message.ImageURLContent{URL: "https://example.com/x.png", Detail: "high"},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: 42},
	}

	want := []sessionShowPart{
		{Type: "text", Text: "hello"},
		{Type: "reasoning", Thinking: "thinking...", StartedAt: 10, FinishedAt: 20},
		{Type: "tool_call", ToolCallID: "tc1", Name: "bash", Input: `{"command":"ls"}`},
		{Type: "tool_result", ToolCallID: "tc1", Name: "bash", Content: "out", IsError: true, MIMEType: "text/plain"},
		{Type: "binary", MIMEType: "image/png", Size: 3},
		{Type: "image_url", URL: "https://example.com/x.png", Detail: "high"},
		{Type: "finish", Reason: string(message.FinishReasonEndTurn), Time: 42},
	}

	require.Equal(t, want, convertParts(parts))
}

func TestConvertParts_UnknownTypeFallsBackToUnknown(t *testing.T) {
	t.Parallel()

	got := convertParts([]message.ContentPart{message.ShellCommand{Command: "ls"}})
	require.Equal(t, []sessionShowPart{{Type: "unknown"}}, got)
}

func TestConvertParts_Empty(t *testing.T) {
	t.Parallel()

	got := convertParts(nil)
	require.Empty(t, got)
}

func TestExtractSkillsFromMessages(t *testing.T) {
	t.Parallel()

	alphaMeta, err := json.Marshal(tools.ViewResponseMetadata{
		ResourceType:        tools.ViewResourceSkill,
		ResourceName:        "alpha",
		ResourceDescription: "Alpha skill",
	})
	require.NoError(t, err)
	betaMeta, err := json.Marshal(tools.ViewResponseMetadata{
		ResourceType:        tools.ViewResourceSkill,
		ResourceName:        "beta",
		ResourceDescription: "Beta skill",
	})
	require.NoError(t, err)

	msgs := []*message.Message{
		{CreatedAt: 200, Parts: []message.ContentPart{message.ToolResult{Metadata: string(alphaMeta)}}},
		{CreatedAt: 100, Parts: []message.ContentPart{message.ToolResult{Metadata: string(betaMeta)}}},
		// Duplicate of alpha: should be deduped and keep the first LoadedAt.
		{CreatedAt: 300, Parts: []message.ContentPart{message.ToolResult{Metadata: string(alphaMeta)}}},
		// No metadata: ignored.
		{CreatedAt: 50, Parts: []message.ContentPart{message.ToolResult{Metadata: ""}}},
		// Metadata present but not a skill resource: ignored.
		{CreatedAt: 60, Parts: []message.ContentPart{message.ToolResult{Metadata: `{"resource_type":"other"}`}}},
		// Not a tool result at all: ignored.
		{CreatedAt: 70, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
	}

	skills := extractSkillsFromMessages(msgs)
	require.Len(t, skills, 2)

	require.Equal(t, "beta", skills[0].Name)
	require.Equal(t, "Beta skill", skills[0].Description)
	require.Equal(t, time.Unix(100, 0).Format(time.RFC3339), skills[0].LoadedAt)

	require.Equal(t, "alpha", skills[1].Name)
	require.Equal(t, "Alpha skill", skills[1].Description)
	require.Equal(t, time.Unix(200, 0).Format(time.RFC3339), skills[1].LoadedAt)
}

func TestExtractSkillsFromMessages_NoSkills(t *testing.T) {
	t.Parallel()

	msgs := []*message.Message{
		{CreatedAt: 1, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
	}
	require.Empty(t, extractSkillsFromMessages(msgs))
}

func TestOutputSessionJSON(t *testing.T) {
	t.Parallel()

	sess := session.Session{
		ID:               "session-uuid-9",
		Title:            "My session",
		CreatedAt:        1000,
		UpdatedAt:        2000,
		Cost:             1.25,
		PromptTokens:     10,
		CompletionTokens: 20,
	}
	msgs := []*message.Message{
		{
			ID:        "msg-1",
			Role:      message.User,
			CreatedAt: 1500,
			Model:     "gpt-5",
			Provider:  "openai",
			Parts:     []message.ContentPart{message.TextContent{Text: "hi"}},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, outputSessionJSON(&buf, sess, msgs))

	var out sessionShowOutput
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))

	require.Equal(t, session.HashID(sess.ID), out.Meta.ID)
	require.Equal(t, sess.ID, out.Meta.UUID)
	require.Equal(t, "My session", out.Meta.Title)
	require.Equal(t, int64(30), out.Meta.TotalTokens)
	require.Empty(t, out.Meta.Skills)

	require.Len(t, out.Messages, 1)
	require.Equal(t, "msg-1", out.Messages[0].ID)
	require.Equal(t, "user", out.Messages[0].Role)
	require.Equal(t, "gpt-5", out.Messages[0].Model)
	require.Len(t, out.Messages[0].Parts, 1)
	require.Equal(t, "text", out.Messages[0].Parts[0].Type)
	require.Equal(t, "hi", out.Messages[0].Parts[0].Text)
}
