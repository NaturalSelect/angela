package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// isolateReloadEnv points HOME/XDG/ANGELA_* at a throwaway directory so a
// load only sees the config files the test writes. No t.Parallel(): these
// tests Setenv.
func isolateReloadEnv(t *testing.T) (workDir, dataDir string) {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(isolated, ".local", "share"))
	t.Setenv("ANGELA_GLOBAL_CONFIG", filepath.Join(isolated, ".config", "angela"))
	return t.TempDir(), t.TempDir()
}

// writeJSONConfig writes an angela.json into dir.
func writeJSONConfig(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(body), 0o644))
}

// TestLoad_MCPFromJSON covers the whole file-to-struct path for MCP servers:
// discovery, merge, and deserialization into Config.MCP. Both transports are
// exercised because they populate disjoint field sets — a stdio server is
// defined by command/args/env, an http one by url/headers — so a single
// transport would leave half the struct untested.
func TestLoad_MCPFromJSON(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	writeJSONConfig(t, workDir, `{
	  "mcp": {
	    "github": {
	      "type": "http",
	      "url": "https://api.githubcopilot.com/mcp/",
	      "headers": {"Authorization": "Bearer xyz"},
	      "timeout": 30
	    },
	    "fs": {
	      "type": "stdio",
	      "command": "node",
	      "args": ["server.js", "--stdio"],
	      "env": {"NODE_ENV": "production"}
	    }
	  }
	}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	mcps := store.Config().MCP

	gh, ok := mcps["github"]
	require.True(t, ok, "github MCP should be configured")
	require.Equal(t, config.MCPHttp, gh.Type)
	require.Equal(t, "https://api.githubcopilot.com/mcp/", gh.URL)
	require.Equal(t, "Bearer xyz", gh.Headers["Authorization"])
	require.Equal(t, 30, gh.Timeout)

	fs, ok := mcps["fs"]
	require.True(t, ok, "fs MCP should be configured")
	require.Equal(t, config.MCPStdio, fs.Type)
	require.Equal(t, "node", fs.Command)
	require.Equal(t, []string{"server.js", "--stdio"}, fs.Args)
	require.Equal(t, "production", fs.Env["NODE_ENV"])
}

// TestLoad_LSPFromJSON is the LSP counterpart. The server name is deliberately
// one applyLSPDefaults knows nothing about, so every asserted value can only
// have come from the file — a defaults table entry would otherwise mask a
// deserialization bug.
func TestLoad_LSPFromJSON(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	writeJSONConfig(t, workDir, `{
	  "lsp": {
	    "examplels": {
	      "command": "example-language-server",
	      "args": ["--stdio"],
	      "filetypes": ["example", "ex"],
	      "root_markers": ["example.toml"],
	      "env": {"EXAMPLE_LOG": "debug"},
	      "timeout": 60
	    }
	  }
	}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)

	l, ok := store.Config().LSP["examplels"]
	require.True(t, ok, "examplels LSP should be configured")
	require.Equal(t, "example-language-server", l.Command)
	require.Equal(t, []string{"--stdio"}, l.Args)
	require.Equal(t, []string{"example", "ex"}, l.FileTypes)
	require.Equal(t, []string{"example.toml"}, l.RootMarkers)
	require.Equal(t, "debug", l.Env["EXAMPLE_LOG"])
	require.Equal(t, 60, l.Timeout)
}

// TestLoad_ProjectLayerDisablesInheritedServers pins the layering rule that
// makes a global config usable at all: a project must be able to switch off a
// server it inherits without restating it. The entry survives the merge —
// disabling marks it, it does not delete it — which is what lets a deeper
// layer turn it back on.
func TestLoad_ProjectLayerDisablesInheritedServers(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)

	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	writeJSONConfig(t, globalDir, `{
	  "mcp": {
	    "shared": {"type": "stdio", "command": "shared-server"},
	    "kept":   {"type": "stdio", "command": "kept-server"}
	  },
	  "lsp": {
	    "sharedls": {"command": "shared-ls"},
	    "keptls":   {"command": "kept-ls"}
	  }
	}`)
	writeJSONConfig(t, workDir, `{
	  "mcp": {"shared": {"disabled": true}},
	  "lsp": {"sharedls": {"disabled": true}}
	}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	cfg := store.Config()

	shared, ok := cfg.MCP["shared"]
	require.True(t, ok, "a disabled MCP stays in the map so a deeper layer can re-enable it")
	require.True(t, shared.Disabled)
	require.Equal(t, "shared-server", shared.Command,
		"disabling must not drop the inherited command")

	require.False(t, cfg.MCP["kept"].Disabled, "the sibling MCP is untouched")

	sharedLS, ok := cfg.LSP["sharedls"]
	require.True(t, ok)
	require.True(t, sharedLS.Disabled)
	require.Equal(t, "shared-ls", sharedLS.Command)
	require.False(t, cfg.LSP["keptls"].Disabled, "the sibling LSP is untouched")
}

// TestLoad_TracksNotYetCreatedGlobalConfig verifies that config.Load tracks
// the global config path even when the file does not exist yet, so a config
// created after startup is detected as a staleness change. Tracking only
// successfully-loaded paths would let a mid-session global config go
// unnoticed until something else triggered a reload.
//
// Project-level configs are discovered by walking the tree for existing
// files, so a not-yet-created project config is still only picked up on the
// next reload; the global path is the common case this covers.
func TestLoad_TracksNotYetCreatedGlobalConfig(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	globalDir := t.TempDir()
	globalConfig := filepath.Join(globalDir, "angela.json")
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)

	// A provider must be configured so Load runs past its early
	// "not configured" return and reaches the staleness snapshot capture.
	writeJSONConfig(t, workDir, `{"providers": {"openai": {"api_key": "k"}}}`)

	// Load with no global config present.
	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	require.False(t, store.ConfigStaleness().Dirty, "fresh load should be clean")

	// Create the global config after startup.
	require.NoError(t, os.WriteFile(globalConfig, []byte(`{"options": {"debug": true}}`), 0o644))

	staleness := store.ConfigStaleness()
	require.True(t, staleness.Dirty, "creating a global config must be detected")
	require.Contains(t, staleness.Changed, globalConfig)
}
