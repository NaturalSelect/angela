package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func loadRuntimeMCPStore(t *testing.T, fileConfig string) (*ConfigStore, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "angela.json")

	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	t.Setenv("ANGELA_GLOBAL_DATA", dir)
	resetProviderState()
	t.Cleanup(resetProviderState)

	require.NoError(t, os.WriteFile(configPath, []byte(fileConfig), 0o600))
	store, err := Load(dir, dir, false)
	require.NoError(t, err)
	store.globalDataPath = configPath
	store.CaptureStalenessSnapshot([]string{configPath})
	return store, configPath
}

func TestAddRuntimeMCP(t *testing.T) {
	t.Run("registers servers and reports their names sorted", func(t *testing.T) {
		store := NewTestStore(&Config{})
		added := store.AddRuntimeMCP(map[string]MCPConfig{
			"zed":   {Type: MCPHttp, URL: "https://example.com/mcp"},
			"alpha": {Type: MCPStdio, Command: "alpha-server"},
		})
		require.Equal(t, []string{"alpha", "zed"}, added)
		require.Equal(t, "alpha-server", store.Config().MCP["alpha"].Command)
		require.Equal(t, "https://example.com/mcp", store.Config().MCP["zed"].URL)
		require.True(t, store.IsRuntimeMCP("alpha"))
		require.False(t, store.IsRuntimeMCP("missing"))
	})

	t.Run("a server from a config file is not replaced", func(t *testing.T) {
		store := NewTestStore(&Config{MCP: MCPs{
			"fs": {Type: MCPStdio, Command: "from-file"},
		}})
		added := store.AddRuntimeMCP(map[string]MCPConfig{
			"fs": {Type: MCPStdio, Command: "from-editor"},
		})
		require.Empty(t, added)
		require.Equal(t, "from-file", store.Config().MCP["fs"].Command)
		require.False(t, store.IsRuntimeMCP("fs"))
	})

	t.Run("an earlier editor server is replaced by a later one", func(t *testing.T) {
		store := NewTestStore(&Config{})
		store.AddRuntimeMCP(map[string]MCPConfig{"ctx": {Type: MCPHttp, URL: "https://one.example"}})
		added := store.AddRuntimeMCP(map[string]MCPConfig{"ctx": {Type: MCPHttp, URL: "https://two.example"}})
		require.Equal(t, []string{"ctx"}, added)
		require.Equal(t, "https://two.example", store.Config().MCP["ctx"].URL)
	})

	t.Run("a config snapshot taken earlier is left untouched", func(t *testing.T) {
		store := NewTestStore(&Config{MCP: MCPs{"fs": {Type: MCPStdio, Command: "fs"}}})
		before := store.Config()
		store.AddRuntimeMCP(map[string]MCPConfig{"ctx": {Type: MCPHttp, URL: "https://example.com"}})
		require.NotContains(t, before.MCP, "ctx")
		require.Contains(t, store.Config().MCP, "ctx")
	})
}

func TestRuntimeMCPUsesIdentityResolver(t *testing.T) {
	store := NewTestStore(&Config{})
	store.AddRuntimeMCP(map[string]MCPConfig{"ctx": {Type: MCPHttp, URL: "https://example.com"}})

	const literal = "$(touch /tmp/pwned) ${HOME}"
	got, err := store.MCPFieldResolver("ctx", "url").ResolveValue(literal)
	require.NoError(t, err)
	require.Equal(t, literal, got, "editor values are literal, never shell-expanded")
}

func TestRuntimeMCPSurvivesReload(t *testing.T) {
	store, configPath := loadRuntimeMCPStore(t, twoProviderConfig("openai", "gpt-4"))

	store.AddRuntimeMCP(map[string]MCPConfig{"ctx": {Type: MCPHttp, URL: "https://example.com/mcp"}})
	require.NoError(t, store.ReloadFromDisk(context.Background()))

	require.Equal(t, "https://example.com/mcp", store.Config().MCP["ctx"].URL)
	require.True(t, store.IsRuntimeMCP("ctx"))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), "ctx", "a runtime server is never written to the config file")
}

func TestRuntimeMCPYieldsToConfigFileOnReload(t *testing.T) {
	store, configPath := loadRuntimeMCPStore(t, twoProviderConfig("openai", "gpt-4"))

	store.AddRuntimeMCP(map[string]MCPConfig{"ctx": {Type: MCPStdio, Command: "from-editor"}})

	fileWithServer := `{
		"slots": {"main": {"provider": "openai", "model": "gpt-4"}},
		"providers": {"openai": {"api_key": "test-key", "models": [{"id": "gpt-4", "name": "GPT-4"}]}},
		"mcp": {"ctx": {"type": "stdio", "command": "from-file"}}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(fileWithServer), 0o600))
	require.NoError(t, store.ReloadFromDisk(context.Background()))

	require.Equal(t, "from-file", store.Config().MCP["ctx"].Command)
	require.False(t, store.IsRuntimeMCP("ctx"), "the file entry takes over, so it is shell-expanded like any other")
}
