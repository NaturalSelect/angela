package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeLayers writes the given JSON documents as angela.json files in
// separate directories and returns the paths in the order given, which
// loadFromConfigPaths treats as lowest to highest priority.
func writeLayers(t *testing.T, docs ...string) []string {
	t.Helper()

	paths := make([]string, 0, len(docs))
	for i, doc := range docs {
		dir := filepath.Join(t.TempDir(), "layer")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		path := filepath.Join(dir, "angela.json")
		require.NoError(t, os.WriteFile(path, []byte(doc), 0o644))
		paths = append(paths, path)
		_ = i
	}
	return paths
}

// TestAgentPermissionsReplaceAcrossLayers pins the contract for the
// agent permission fields: the highest-priority layer that names one
// replaces it outright. The generic config merge concatenates arrays,
// which would union a narrow high-priority allowlist with the broad
// low-priority one and silently grant back what was removed.
func TestAgentPermissionsReplaceAcrossLayers(t *testing.T) {
	t.Parallel()

	t.Run("a narrower allowed_tools replaces the broader one", func(t *testing.T) {
		t.Parallel()
		paths := writeLayers(t,
			`{"agents": {"coder": {"allowed_tools": ["bash", "view", "edit"]}}}`,
			`{"agents": {"coder": {"allowed_tools": ["view"]}}}`,
		)

		cfg, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		coder := cfg.AgentConfigs["coder"]
		require.NotNil(t, coder.AllowedTools)
		require.Equal(t, []string{"view"}, coder.AllowedTools.Tools,
			"the high-priority layer must replace, not union")
	})

	t.Run("an empty allowed_tools clears the lower layer", func(t *testing.T) {
		t.Parallel()
		paths := writeLayers(t,
			`{"agents": {"coder": {"allowed_tools": ["bash", "view"]}}}`,
			`{"agents": {"coder": {"allowed_tools": []}}}`,
		)

		cfg, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		coder := cfg.AgentConfigs["coder"]
		require.NotNil(t, coder.AllowedTools)
		require.Empty(t, coder.AllowedTools.Tools, "an explicit empty list must grant nothing")
	})

	t.Run("disabled_tools replaces rather than accumulates", func(t *testing.T) {
		t.Parallel()
		paths := writeLayers(t,
			`{"agents": {"coder": {"disabled_tools": ["bash", "write"]}}}`,
			`{"agents": {"coder": {"disabled_tools": ["write"]}}}`,
		)

		cfg, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		require.Equal(t, []string{"write"}, cfg.AgentConfigs["coder"].DisabledTools,
			"re-enabling bash in a higher layer must actually re-enable it")
	})

	t.Run("a layer switching to inherited does not break the load", func(t *testing.T) {
		t.Parallel()
		paths := writeLayers(t,
			`{"agents": {"coder": {"allowed_tools": ["bash", "view"]}}}`,
			`{"agents": {"coder": {"allowed_tools": "inherited"}}}`,
		)

		cfg, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		coder := cfg.AgentConfigs["coder"]
		require.NotNil(t, coder.AllowedTools)
		require.Equal(t, ToolSetInherited, coder.AllowedTools.Kind)
	})

	t.Run("a field only the low layer names survives", func(t *testing.T) {
		t.Parallel()
		paths := writeLayers(t,
			`{"agents": {"coder": {"allowed_tools": ["bash"], "disabled_tools": ["write"]}}}`,
			`{"agents": {"coder": {"allowed_tools": ["view"]}}}`,
		)

		cfg, _, err := loadFromConfigPaths(context.Background(), paths)
		require.NoError(t, err)

		coder := cfg.AgentConfigs["coder"]
		require.Equal(t, []string{"view"}, coder.AllowedTools.Tools)
		require.Equal(t, []string{"write"}, coder.DisabledTools,
			"a field the high layer never mentions must keep the low layer's value")
	})
}
