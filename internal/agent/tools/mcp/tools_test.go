package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestEnsureRawBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []byte
		wantData []byte
	}{
		{
			name:     "already base64 encoded",
			input:    []byte("SGVsbG8gV29ybGQh"), // "Hello World!" in base64
			wantData: []byte("Hello World!"),
		},
		{
			name:     "raw binary data (PNG header)",
			input:    []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			wantData: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
		},
		{
			name:     "raw binary with high bytes",
			input:    []byte{0xFF, 0xD8, 0xFF, 0xE0}, // JPEG header
			wantData: []byte{0xFF, 0xD8, 0xFF, 0xE0},
		},
		{
			name:     "empty data",
			input:    []byte{},
			wantData: []byte{},
		},
		{
			name:     "base64 with padding",
			input:    []byte("YQ=="), // "a" in base64
			wantData: []byte("a"),
		},
		{
			name:     "base64 without padding",
			input:    []byte("YQ"),
			wantData: []byte("a"),
		},
		{
			name:     "base64 with whitespace",
			input:    []byte("U0dWc2JHOGdWMjl5YkdRaA==\n"),
			wantData: []byte("SGVsbG8gV29ybGQh"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ensureRawBytes(tt.input)
			require.Equal(t, tt.wantData, result)

			if len(result) > 0 && !bytes.Equal(result, tt.input) {
				reEncoded := base64.StdEncoding.EncodeToString(result)
				_, err := base64.StdEncoding.DecodeString(reEncoded)
				require.NoError(t, err, "re-encoded result should be valid base64")
			}
		})
	}
}

func TestFilterTools(t *testing.T) {
	t.Parallel()

	tools := []*Tool{
		{Name: "tool_a"},
		{Name: "tool_b"},
		{Name: "tool_c"},
	}

	t.Run("no filters returns all tools", func(t *testing.T) {
		t.Parallel()
		result := filterTools(config.MCPConfig{}, tools)
		require.Len(t, result, 3)
	})

	t.Run("disabled tools filters deny list", func(t *testing.T) {
		t.Parallel()
		result := filterTools(config.MCPConfig{DisabledTools: []string{"tool_a"}}, tools)
		require.Len(t, result, 2)
		require.Equal(t, "tool_b", result[0].Name)
		require.Equal(t, "tool_c", result[1].Name)
	})

	t.Run("enabled tools acts as allow list", func(t *testing.T) {
		t.Parallel()
		result := filterTools(config.MCPConfig{EnabledTools: []string{"tool_b"}}, tools)
		require.Len(t, result, 1)
		require.Equal(t, "tool_b", result[0].Name)
	})

	t.Run("enabled and disabled both apply", func(t *testing.T) {
		t.Parallel()
		result := filterTools(config.MCPConfig{
			EnabledTools:  []string{"tool_a", "tool_b"},
			DisabledTools: []string{"tool_b"},
		}, tools)
		require.Len(t, result, 1)
		require.Equal(t, "tool_a", result[0].Name)
	})

	t.Run("enabled with non-existent tool returns empty", func(t *testing.T) {
		t.Parallel()
		result := filterTools(config.MCPConfig{EnabledTools: []string{"non_existent"}}, tools)
		require.Len(t, result, 0)
	})
}

func TestTools(t *testing.T) {
	t.Parallel()

	const name = "test-tools-getter"
	t.Cleanup(func() { allTools.Del(name) })

	allTools.Set(name, []*Tool{{Name: "t1"}})

	found := false
	for k, v := range Tools() {
		if k == name {
			found = true
			require.Len(t, v, 1)
			require.Equal(t, "t1", v[0].Name)
		}
	}
	require.True(t, found, "Tools() must expose entries registered in allTools")
}

func TestRunTool_ParseError(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	_, err := RunTool(context.Background(), cfg, "does-not-matter", "does-not-matter", "{not-json")
	require.ErrorContains(t, err, "error parsing parameters")
}

func TestRunTool_NoSession(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	_, err := RunTool(context.Background(), cfg, "missing-server", "some_tool", "{}")
	require.Error(t, err)
}

func TestRunTool_TextContent(t *testing.T) {
	const name = "test-run-tool-text"
	t.Cleanup(func() {
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess, _ := liveSession(t, "send_message")
	sessions.Set(name, sess)
	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

	result, err := RunTool(context.Background(), cfg, name, "send_message", "{}")
	require.NoError(t, err)
	require.Equal(t, ToolResult{Type: "text", Content: "ok"}, result)
}

func TestRunTool_UnknownToolError(t *testing.T) {
	const name = "test-run-tool-unknown"
	t.Cleanup(func() {
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess, _ := liveSession(t, "send_message")
	sessions.Set(name, sess)
	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

	_, err := RunTool(context.Background(), cfg, name, "does_not_exist", "{}")
	require.Error(t, err)
}

// TestRunTool_ContentVariants pins RunTool's content-shaping logic: text
// parts are newline-joined, and the first image or audio part (if any) wins
// and carries its raw bytes and media type through ensureRawBytes.
func TestRunTool_ContentVariants(t *testing.T) {
	tests := []struct {
		name    string
		content []mcp.Content
		want    ToolResult
	}{
		{
			name:    "single text content",
			content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
			want:    ToolResult{Type: "text", Content: "hello"},
		},
		{
			name: "multiple text parts are newline joined",
			content: []mcp.Content{
				&mcp.TextContent{Text: "line one"},
				&mcp.TextContent{Text: "line two"},
			},
			want: ToolResult{Type: "text", Content: "line one\nline two"},
		},
		{
			name:    "empty content returns empty text result",
			content: nil,
			want:    ToolResult{Type: "text", Content: ""},
		},
		{
			name:    "image content returns raw bytes and media type",
			content: []mcp.Content{&mcp.ImageContent{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIMEType: "image/png"}},
			want:    ToolResult{Type: "image", Content: "", Data: []byte{0x89, 0x50, 0x4E, 0x47}, MediaType: "image/png"},
		},
		{
			name:    "audio content returns raw bytes and media type",
			content: []mcp.Content{&mcp.AudioContent{Data: []byte{1, 2, 3, 4}, MIMEType: "audio/wav"}},
			want:    ToolResult{Type: "media", Content: "", Data: []byte{1, 2, 3, 4}, MediaType: "audio/wav"},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("test-run-tool-variant-%d", i)
			t.Cleanup(func() {
				if s, ok := sessions.Take(name); ok {
					_ = s.Close()
				}
			})

			content := tt.content
			sess, _ := liveSessionWithHandler(t, "variant_tool", func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: content}, nil, nil
			})
			sessions.Set(name, sess)
			cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

			got, err := RunTool(context.Background(), cfg, name, "variant_tool", "{}")
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRefreshTools(t *testing.T) {
	t.Run("no session logs and returns without touching state", func(t *testing.T) {
		t.Parallel()

		const name = "test-refresh-tools-no-session"
		RefreshTools(context.Background(), config.NewTestStore(&config.Config{}), name)
		_, ok := allTools.Get(name)
		require.False(t, ok)
	})

	t.Run("populates tools and marks connected", func(t *testing.T) {
		const name = "test-refresh-tools-connected"
		t.Cleanup(func() {
			if s, ok := sessions.Take(name); ok {
				_ = s.Close()
			}
			allTools.Del(name)
			states.Del(name)
		})

		sess, _ := liveSession(t, "send_message")
		sessions.Set(name, sess)
		cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

		RefreshTools(context.Background(), cfg, name)

		tools, ok := allTools.Get(name)
		require.True(t, ok)
		require.Len(t, tools, 1)
		require.Equal(t, "send_message", tools[0].Name)

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateConnected, info.State)
		require.Equal(t, 1, info.Counts.Tools)
	})

	t.Run("list error marks the server errored", func(t *testing.T) {
		const name = "test-refresh-tools-error"
		t.Cleanup(func() {
			if s, ok := sessions.Take(name); ok {
				_ = s.Close()
			}
			states.Del(name)
		})

		sess, _ := liveSession(t, "send_message")
		require.NoError(t, sess.Close())
		sessions.Set(name, sess)
		cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

		RefreshTools(context.Background(), cfg, name)

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateError, info.State)
	})
}
