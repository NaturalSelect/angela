package cmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/spf13/cobra"
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

func newSessionRunCmd(t *testing.T, dataDir string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("data-dir", "", "")
	require.NoError(t, cmd.Flags().Set("data-dir", dataDir))
	return cmd
}

// isolateSessionEnv keeps sessionSetup's config.Init call from touching the
// developer's real config or the network: it points config discovery at an
// empty directory instead of this repo's own angela.json, and disables
// telemetry and the Catwalk provider fetch (both on by default).
func isolateSessionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ANGELA_DISABLE_METRICS", "true")
	t.Setenv("ANGELA_DISABLE_PROVIDER_AUTO_UPDATE", "true")
	t.Setenv("ANGELA_GLOBAL_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
}

// seedSession creates a session directly against dataDir's database,
// bypassing the CLI, and releases its connection before returning so the
// RunE call under test opens its own instead of sharing (and later
// force-closing) this one.
func seedSession(t *testing.T, dataDir, title string) session.Session {
	t.Helper()
	ctx := t.Context()
	db.ResetPool()
	t.Cleanup(db.ResetPool)
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	sess, err := session.NewService(db.New(conn), conn).Create(ctx, title)
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	return sess
}

// listSessionsDirect reopens dataDir's database to verify state left behind
// by a session command. It resets angela's connection pool first because
// sessionSetup's cleanup closes its pooled *sql.DB directly rather than
// releasing it, which would otherwise hand back an already-closed
// connection for the same data directory.
func listSessionsDirect(t *testing.T, dataDir string) []session.Session {
	t.Helper()
	ctx := t.Context()
	db.ResetPool()
	t.Cleanup(db.ResetPool)
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	list, err := session.NewService(db.New(conn), conn).List(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	return list
}

func TestSessionSetup_ConnectsAndCleansUp(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	cmd := newSessionRunCmd(t, dataDir)

	ctx, svc, cleanup, err := sessionSetup(cmd)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NotNil(t, svc.sessions)
	require.NotNil(t, svc.messages)

	list, err := svc.sessions.List(ctx)
	require.NoError(t, err)
	require.Empty(t, list)

	cleanup()
}

// TestSessionSetup_PropagatesDBConnectError puts a regular file where the
// data directory should be, so db.Connect's os.MkdirAll fails and the
// error-wrapping branch runs.
func TestSessionSetup_PropagatesDBConnectError(t *testing.T) {
	isolateSessionEnv(t)
	parent := t.TempDir()
	blocked := filepath.Join(parent, "blocked")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	cmd := newSessionRunCmd(t, filepath.Join(blocked, "data"))

	_, _, _, err := sessionSetup(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to connect to database")
}

// TestSessionSetup_PropagatesConfigInitError covers config.Init's own
// error branch: a malformed global angela.json must fail sessionSetup
// before it ever tries to connect to a database. sessionSetup calls
// config.Init with an empty working directory, so only the global
// config path (not a project-local angela.json) is ever consulted.
func TestSessionSetup_PropagatesConfigInitError(t *testing.T) {
	isolateSessionEnv(t)
	globalPath := config.GlobalConfig()
	require.NoError(t, os.MkdirAll(filepath.Dir(globalPath), 0o755))
	require.NoError(t, os.WriteFile(globalPath, []byte("{not valid json"), 0o644))

	cmd := newSessionRunCmd(t, t.TempDir())

	_, _, _, err := sessionSetup(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to initialize config")
}

func TestRunSessionList_JSONEmpty(t *testing.T) {
	isolateSessionEnv(t)
	cmd := newSessionRunCmd(t, t.TempDir())
	sessionListJSON = true
	defer func() { sessionListJSON = false }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionList(cmd, nil))

	var out []sessionJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	require.Empty(t, out)
}

func TestRunSessionList_JSONWithSessions(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedSession(t, dataDir, "First session")
	seedSession(t, dataDir, "Second session")

	cmd := newSessionRunCmd(t, dataDir)
	sessionListJSON = true
	defer func() { sessionListJSON = false }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionList(cmd, nil))

	var out []sessionJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	require.Len(t, out, 2)
}

func TestRunSessionList_HumanOutput(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedSession(t, dataDir, "Human list session")
	cmd := newSessionRunCmd(t, dataDir)
	getOutput := swapStdoutPipe(t)

	require.NoError(t, runSessionList(cmd, nil))

	require.Contains(t, getOutput(), "Human list session")
}

func TestRunSessionShow_JSON(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Show me")

	cmd := newSessionRunCmd(t, dataDir)
	sessionShowJSON = true
	defer func() { sessionShowJSON = false }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionShow(cmd, []string{sess.ID}))

	var out sessionShowOutput
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	require.Equal(t, sess.ID, out.Meta.UUID)
	require.Equal(t, "Show me", out.Meta.Title)
}

func TestRunSessionShow_Human(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Show me human")

	cmd := newSessionRunCmd(t, dataDir)
	getOutput := swapStdoutPipe(t)

	require.NoError(t, runSessionShow(cmd, []string{sess.ID}))

	require.Contains(t, getOutput(), "Show me human")
}

func TestRunSessionShow_NotFound(t *testing.T) {
	isolateSessionEnv(t)
	cmd := newSessionRunCmd(t, t.TempDir())

	err := runSessionShow(cmd, []string{"does-not-exist"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found")
}

func TestRunSessionDelete_JSON(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Delete me")

	cmd := newSessionRunCmd(t, dataDir)
	sessionDeleteJSON = true
	defer func() { sessionDeleteJSON = false }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionDelete(cmd, []string{sess.ID}))

	var out sessionMutationResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	require.True(t, out.Deleted)
	require.Equal(t, sess.ID, out.UUID)

	require.Empty(t, listSessionsDirect(t, dataDir))
}

func TestRunSessionDelete_Human(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Delete me human")

	cmd := newSessionRunCmd(t, dataDir)
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionDelete(cmd, []string{sess.ID}))
	require.Contains(t, buf.String(), "Deleted session")

	require.Empty(t, listSessionsDirect(t, dataDir))
}

func TestRunSessionRename_JSON(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Old title")

	cmd := newSessionRunCmd(t, dataDir)
	sessionRenameJSON = true
	defer func() { sessionRenameJSON = false }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionRename(cmd, []string{sess.ID, "New", "title"}))

	var out sessionMutationResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	require.True(t, out.Renamed)
	require.Equal(t, "New title", out.Title)

	list := listSessionsDirect(t, dataDir)
	require.Len(t, list, 1)
	require.Equal(t, "New title", list[0].Title)
}

func TestRunSessionRename_Human(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Old title human")

	cmd := newSessionRunCmd(t, dataDir)
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionRename(cmd, []string{sess.ID, "Renamed"}))
	require.Contains(t, buf.String(), "Renamed session")

	list := listSessionsDirect(t, dataDir)
	require.Len(t, list, 1)
	require.Equal(t, "Renamed", list[0].Title)
}

func TestRunSessionLast_JSON(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	sess := seedSession(t, dataDir, "Only session")

	cmd := newSessionRunCmd(t, dataDir)
	sessionLastJSON = true
	defer func() { sessionLastJSON = false }()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, runSessionLast(cmd, nil))

	var out sessionShowOutput
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	require.Equal(t, sess.ID, out.Meta.UUID)
}

func TestRunSessionLast_NoSessions(t *testing.T) {
	isolateSessionEnv(t)
	cmd := newSessionRunCmd(t, t.TempDir())

	err := runSessionLast(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no sessions found")
}

// TestOutputSessionHuman_WritesExpectedFields exercises outputSessionHuman
// directly (not through a RunE), since it writes straight to os.Stdout via
// sessionWriter rather than through cmd.OutOrStdout().
func TestOutputSessionHuman_WritesExpectedFields(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	sess := session.Session{ID: "session-uuid-human", Title: "Direct human output", CreatedAt: 1700000000}
	msgs := []*message.Message{
		{
			ID: "m1", Role: message.User, CreatedAt: 1700000001,
			Parts: []message.ContentPart{message.TextContent{Text: "hello there"}},
		},
	}

	require.NoError(t, outputSessionHuman(t.Context(), sess, msgs))

	out := getOutput()
	require.Contains(t, out, "Direct human output")
	require.Contains(t, out, session.HashID(sess.ID)[:12])
	require.Contains(t, out, "hello there")
}

// TestOutputSessionHuman_RendersSkillsAndMultipleItems covers the
// skills-summary line (only rendered when the transcript loaded at least
// one skill) and the blank-line separator emitted between the second and
// later rendered items.
func TestOutputSessionHuman_RendersSkillsAndMultipleItems(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	skillMeta, err := json.Marshal(tools.ViewResponseMetadata{
		ResourceType:        tools.ViewResourceSkill,
		ResourceName:        "alpha",
		ResourceDescription: "Alpha skill",
	})
	require.NoError(t, err)

	sess := session.Session{ID: "session-uuid-skills", Title: "With skills", CreatedAt: 1700000000}
	msgs := []*message.Message{
		{
			ID: "m1", Role: message.User, CreatedAt: 1700000001,
			Parts: []message.ContentPart{message.TextContent{Text: "first message"}},
		},
		{
			ID: "m2", Role: message.Tool, CreatedAt: 1700000002,
			Parts: []message.ContentPart{message.ToolResult{Metadata: string(skillMeta)}},
		},
		{
			ID: "m3", Role: message.User, CreatedAt: 1700000003,
			Parts: []message.ContentPart{message.TextContent{Text: "second message"}},
		},
	}

	require.NoError(t, outputSessionHuman(t.Context(), sess, msgs))

	out := getOutput()
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "first message")
	require.Contains(t, out, "second message")
}

// TestSessionWriter_NonTerminalReturnsPlainWriter covers the common test
// and CI path: stdout is not a terminal, so sessionWriter must hand back a
// plain writer over the current os.Stdout rather than spawning a pager.
func TestSessionWriter_NonTerminalReturnsPlainWriter(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	w, cleanup, usingPager := sessionWriter(t.Context(), 5)
	defer cleanup()

	require.False(t, usingPager)
	require.NotNil(t, w)

	_, err := io.WriteString(w, "sessionWriter direct test")
	require.NoError(t, err)
	require.Contains(t, getOutput(), "sessionWriter direct test")
}
