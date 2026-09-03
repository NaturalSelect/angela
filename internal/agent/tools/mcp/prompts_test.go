package mcp

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// liveSessionWithPromptHandler is like liveSessionWithCapabilities but lets
// the caller supply the prompt's handler directly, so tests can exercise
// GetPromptMessages' role/content filtering (only "user" messages with text
// content are surfaced) against a real session.
func liveSessionWithPromptHandler(t *testing.T, promptName string, handler mcp.PromptHandler) *ClientSession {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "srv"}, nil)
	server.AddPrompt(&mcp.Prompt{Name: promptName}, handler)
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	client := mcp.NewClient(&mcp.Implementation{Name: "angela-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	return &ClientSession{ClientSession: clientSession, cancel: cancel}
}

func TestPrompts(t *testing.T) {
	t.Parallel()

	const name = "test-prompts-getter"
	t.Cleanup(func() { allPrompts.Del(name) })

	allPrompts.Set(name, []*Prompt{{Name: "p1"}})

	found := false
	for k, v := range Prompts() {
		if k == name {
			found = true
			require.Len(t, v, 1)
			require.Equal(t, "p1", v[0].Name)
		}
	}
	require.True(t, found, "Prompts() must expose entries registered in allPrompts")
}

// TestGetPromptMessages pins the message filtering GetPromptMessages
// performs: only messages with Role "user" and *mcp.TextContent content are
// returned, in order, and everything else (other roles, non-text content) is
// dropped.
func TestGetPromptMessages(t *testing.T) {
	const name = "test-get-prompt-messages"
	t.Cleanup(func() {
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess := liveSessionWithPromptHandler(t, "greeting", func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: "hello"}},
				{Role: "assistant", Content: &mcp.TextContent{Text: "ignored, not user"}},
				{Role: "user", Content: &mcp.ImageContent{Data: []byte{1, 2, 3}, MIMEType: "image/png"}},
				{Role: "user", Content: &mcp.TextContent{Text: "world"}},
			},
		}, nil
	})
	sessions.Set(name, sess)
	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

	messages, err := GetPromptMessages(context.Background(), cfg, name, "greeting", map[string]string{"k": "v"})
	require.NoError(t, err)
	require.Equal(t, []string{"hello", "world"}, messages)
}

func TestGetPromptMessages_NoSession(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	_, err := GetPromptMessages(context.Background(), cfg, "missing", "greeting", nil)
	require.Error(t, err)
}

func TestRefreshPrompts(t *testing.T) {
	t.Run("no session logs and returns without touching state", func(t *testing.T) {
		t.Parallel()

		const name = "test-refresh-prompts-no-session"
		RefreshPrompts(context.Background(), name)
		_, ok := allPrompts.Get(name)
		require.False(t, ok)
	})

	t.Run("populates prompts and marks connected", func(t *testing.T) {
		const name = "test-refresh-prompts-connected"
		t.Cleanup(func() {
			if s, ok := sessions.Take(name); ok {
				_ = s.Close()
			}
			allPrompts.Del(name)
			states.Del(name)
		})

		sess := liveSessionWithCapabilities(t, "a_tool", "a_prompt", "res://thing")
		sessions.Set(name, sess)

		RefreshPrompts(context.Background(), name)

		got, ok := allPrompts.Get(name)
		require.True(t, ok)
		require.Len(t, got, 1)

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateConnected, info.State)
		require.Equal(t, 1, info.Counts.Prompts)
	})

	t.Run("list error marks the server errored", func(t *testing.T) {
		const name = "test-refresh-prompts-error"
		t.Cleanup(func() {
			if s, ok := sessions.Take(name); ok {
				_ = s.Close()
			}
			states.Del(name)
		})

		sess := liveSessionWithCapabilities(t, "a_tool", "a_prompt", "res://thing")
		require.NoError(t, sess.Close())
		sessions.Set(name, sess)

		RefreshPrompts(context.Background(), name)

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateError, info.State)
	})
}
