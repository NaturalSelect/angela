package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// TestGetMCPTools_NoServersConfigured pins the base case: with no MCP
// tools currently registered, GetMCPTools returns nothing rather than
// panicking or fabricating entries.
func TestGetMCPTools_NoServersConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	require.Empty(t, GetMCPTools(cfg, t.TempDir()))
}

func TestTool_ProviderOptions(t *testing.T) {
	t.Parallel()

	tool := &Tool{}
	require.Zero(t, tool.ProviderOptions())

	opts := fantasy.ProviderOptions{}
	tool.SetProviderOptions(opts)
	require.Equal(t, opts, tool.ProviderOptions())
}

func TestTool_NameMCPAndMCPToolName(t *testing.T) {
	t.Parallel()

	tool := &Tool{
		mcpName: "git",
		tool:    &mcp.Tool{Name: "commit"},
	}

	require.Equal(t, toolnames.MCPPrefix+"git_commit", tool.Name())
	require.Equal(t, "git", tool.MCP())
	require.Equal(t, "commit", tool.MCPToolName())
}

func TestTool_Info(t *testing.T) {
	t.Parallel()

	t.Run("extracts properties and required as []any", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{
			mcpName: "git",
			tool: &mcp.Tool{
				Name:        "commit",
				Description: "Commit staged changes",
				InputSchema: map[string]any{
					"properties": map[string]any{"message": map[string]any{"type": "string"}},
					"required":   []any{"message"},
				},
			},
		}

		info := tool.Info()
		require.Equal(t, toolnames.MCPPrefix+"git_commit", info.Name)
		require.Equal(t, "Commit staged changes", info.Description)
		require.Contains(t, info.Parameters, "message")
		require.Equal(t, []string{"message"}, info.Required)
	})

	t.Run("accepts required as already-typed []string", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{
			tool: &mcp.Tool{
				InputSchema: map[string]any{"required": []string{"a", "b"}},
			},
		}

		info := tool.Info()
		require.Equal(t, []string{"a", "b"}, info.Required)
	})

	t.Run("defaults to empty when schema is not a map", func(t *testing.T) {
		t.Parallel()
		tool := &Tool{tool: &mcp.Tool{InputSchema: "not-a-map"}}

		info := tool.Info()
		require.Empty(t, info.Parameters)
		require.Empty(t, info.Required)
	})
}

// TestTool_Run_RequiresSession pins that Run refuses to call into the
// MCP layer at all without a session ID on the context.
func TestTool_Run_RequiresSession(t *testing.T) {
	t.Parallel()

	tool := &Tool{mcpName: "git", tool: &mcp.Tool{Name: "commit"}}

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "session ID is required")
}

// TestTool_Run_InvalidJSONInput pins that malformed call input is
// reported as a tool error response rather than an internal error,
// without needing any live MCP connection.
func TestTool_Run_InvalidJSONInput(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := &Tool{mcpName: "git", tool: &mcp.Tool{Name: "commit"}, cfg: cfg}

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{not valid json`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "error parsing parameters")
}

// TestTool_Run_MCPNotConfigured pins that calling a tool whose MCP
// server was never initialized comes back as a tool error response
// instead of panicking or hanging, exercising Run without any live
// server connection.
func TestTool_Run_MCPNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	tool := &Tool{mcpName: "nonexistent-mcp-zzz-testonly", tool: &mcp.Tool{Name: "commit"}, cfg: cfg}

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "not available")
}

func TestSaveMCPMedia(t *testing.T) {
	t.Parallel()

	t.Run("writes the bytes under the media directory with a matching extension", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		payload := []byte{0x89, 0x50, 0x4E, 0x47}

		path, err := saveMCPMedia(dataDir, payload, "image/png")
		require.NoError(t, err)

		require.Equal(t, filepath.Join(dataDir, mcpMediaDirName), filepath.Dir(path))
		require.Equal(t, ".png", filepath.Ext(path))
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, payload, got)
	})

	t.Run("each call produces a distinct file", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()

		first, err := saveMCPMedia(dataDir, []byte("a"), "image/png")
		require.NoError(t, err)
		second, err := saveMCPMedia(dataDir, []byte("b"), "image/png")
		require.NoError(t, err)

		require.NotEqual(t, first, second)
	})

	t.Run("hostile media type cannot escape the media directory", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()

		path, err := saveMCPMedia(dataDir, []byte("x"), "image/../../../etc/passwd")
		require.NoError(t, err)

		require.Equal(t, filepath.Join(dataDir, mcpMediaDirName), filepath.Dir(path))
	})

	t.Run("fails when the data directory is not usable", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

		_, err := saveMCPMedia(blocker, []byte("x"), "image/png")
		require.Error(t, err)
	})
}

func TestPruneMCPMedia(t *testing.T) {
	t.Parallel()

	ageFile := func(t *testing.T, path string, age time.Duration) {
		t.Helper()
		when := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(path, when, when))
	}

	t.Run("removes only files older than the retention", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()

		stale, err := saveMCPMedia(dataDir, []byte("stale"), "image/png")
		require.NoError(t, err)
		fresh, err := saveMCPMedia(dataDir, []byte("fresh"), "image/png")
		require.NoError(t, err)
		ageFile(t, stale, 2*time.Hour)
		ageFile(t, fresh, 30*time.Minute)

		require.NoError(t, pruneMCPMedia(dataDir, time.Hour))

		require.NoFileExists(t, stale)
		require.FileExists(t, fresh)
	})

	t.Run("a missing media directory is not an error", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, pruneMCPMedia(t.TempDir(), time.Hour))
	})

	t.Run("leaves subdirectories alone", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		subdir := filepath.Join(dataDir, mcpMediaDirName, "nested")
		require.NoError(t, os.MkdirAll(subdir, 0o700))
		ageFile(t, subdir, 48*time.Hour)

		require.NoError(t, pruneMCPMedia(dataDir, time.Hour))

		require.DirExists(t, subdir)
	})

	t.Run("does not touch files outside the media directory", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		neighbour := filepath.Join(dataDir, "angela.db")
		require.NoError(t, os.WriteFile(neighbour, []byte("db"), 0o600))
		ageFile(t, neighbour, 48*time.Hour)
		_, err := saveMCPMedia(dataDir, []byte("x"), "image/png")
		require.NoError(t, err)

		require.NoError(t, pruneMCPMedia(dataDir, time.Hour))

		require.FileExists(t, neighbour)
	})
}

func TestMCPMediaExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mediaType string
		want      string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"image/png; charset=binary", ".png"},
		{"", ".bin"},
		{"not a media type", ".bin"},
		{"application/x-unknown-zzz", ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.mediaType, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, mcpMediaExtension(tt.mediaType))
		})
	}
}

func TestMediaSavedNote(t *testing.T) {
	t.Parallel()

	require.Equal(t, "The file is saved at /tmp/a.png.", mediaSavedNote("", "/tmp/a.png"))
	require.Equal(t, "scan me\n\nThe file is saved at /tmp/a.png.", mediaSavedNote("scan me", "/tmp/a.png"))
}
