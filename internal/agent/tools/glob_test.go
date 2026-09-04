package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestGlobFilesScopedPrefixMatchesUnscoped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mkfile := func(rel string) {
		full := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
	}
	mkfile("a/b/one.go")
	mkfile("a/b/c/two.go")
	mkfile("a/other.txt")
	mkfile("z/three.go")

	got, _, err := globFiles(context.Background(), "a/**/*.go", root, 100)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, p := range got {
		require.Contains(t, p, filepath.Join("a", "b"))
	}
}

func TestGlobFilesDoesNotFollowSymlinkEscape(t *testing.T) {
	t.Parallel()

	// Build a project dir with a symlink pointing outside it. With symlink
	// following disabled, the glob must not pick up files behind the link.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.go"), []byte("x"), 0o644))

	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "in.go"), []byte("x"), 0o644))
	link := filepath.Join(project, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	got, _, err := globFiles(context.Background(), "**/*.go", project, 100)
	require.NoError(t, err)
	for _, p := range got {
		require.NotContains(t, p, "secret.go", "glob followed a symlink out of the search root")
	}
}

func TestGlobFilesCapsResultsOnLargeTree(t *testing.T) {
	t.Parallel()

	// A tree with far more matches than the limit must still return at
	// most `limit` results and report truncation, regardless of which
	// backend (ripgrep or the doublestar fallback) runs.
	root := t.TempDir()
	for i := range 500 {
		dir := filepath.Join(root, "pkg")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		name := filepath.Join(dir, "file"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+"_"+string(rune('0'+i%10))+".go")
		require.NoError(t, os.WriteFile(name, []byte("x"), 0o644))
	}

	got, truncated, err := globFiles(context.Background(), "**/*.go", root, 10)
	require.NoError(t, err)
	require.LessOrEqual(t, len(got), 10, "must not exceed limit")
	require.True(t, truncated, "should report truncation on an over-limit tree")
}

func TestNewGlobToolRequiresPattern(t *testing.T) {
	t.Parallel()

	tool := NewGlobTool(t.TempDir(), config.ToolGlob{})
	input, err := json.Marshal(GlobParams{})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Glob, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "pattern is required")
}

func TestNewGlobToolReturnsMatchesWithMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x"), 0o644))

	tool := NewGlobTool(dir, config.ToolGlob{})
	input, err := json.Marshal(GlobParams{Pattern: "*.go"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Glob, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "a.go")
	require.Contains(t, resp.Content, "b.go")
	require.NotContains(t, resp.Content, "c.txt")

	var meta GlobResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 2, meta.NumberOfFiles)
	require.False(t, meta.Truncated)
}

func TestNewGlobToolReportsNoFilesFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := NewGlobTool(dir, config.ToolGlob{})
	input, err := json.Marshal(GlobParams{Pattern: "*.nonexistent"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Glob, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "No files found")
}

// TestNewGlobToolUsesParamsPathOverWorkingDir pins that an explicit
// params.Path search root wins over the tool's configured workingDir,
// letting a caller search a different tree without a new tool instance.
func TestNewGlobToolUsesParamsPathOverWorkingDir(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "only.go"), []byte("x"), 0o644))

	tool := NewGlobTool(workingDir, config.ToolGlob{})
	input, err := json.Marshal(GlobParams{Pattern: "*.go", Path: other})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Glob, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "only.go")
}

func TestNormalizeFilePaths(t *testing.T) {
	t.Parallel()

	paths := []string{filepath.Join("a", "b", "c.go")}
	normalizeFilePaths(paths)
	require.Equal(t, "a/b/c.go", paths[0])
}
