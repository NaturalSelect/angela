package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// initGitRepo creates a repository with two commits touching the same
// file, so log, blame, show, and diff all have something to report on.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	run("init", "-q")
	run("config", "user.name", "test")
	run("config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
	run("add", "a.txt")
	run("commit", "-q", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello again\n"), 0o644))
	run("commit", "-q", "-a", "-m", "second")

	return dir
}

func runGitTool(t *testing.T, dir string, args []string) fantasy.ToolResponse {
	t.Helper()
	tool := NewGitTool(dir)
	input, err := json.Marshal(GitParams{Args: args})
	require.NoError(t, err)
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Git, Input: string(input)})
	require.NoError(t, err)
	return resp
}

func TestGitTool_AllowsReadOnlyVerbs(t *testing.T) {
	t.Parallel()
	dir := initGitRepo(t)

	cases := []struct {
		name    string
		args    []string
		content string
	}{
		{"log", []string{"log", "--oneline", "-n", "5"}, "second"},
		{"blame", []string{"blame", "a.txt"}, "hello again"},
		{"show", []string{"show", "HEAD"}, "second"},
		{"diff between revisions", []string{"diff", "HEAD~1", "--", "a.txt"}, "hello again"},
		{"status", []string{"status"}, "branch"},
		{"branch listing", []string{"branch", "--list"}, ""},
		{"config read", []string{"config", "--get", "user.name"}, "test"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := runGitTool(t, dir, tc.args)
			require.False(t, resp.IsError, "content: %s", resp.Content)
			if tc.content != "" {
				require.Contains(t, resp.Content, tc.content)
			}
		})
	}
}

func TestGitTool_RejectsWritesAndEscapes(t *testing.T) {
	t.Parallel()
	dir := initGitRepo(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"commit", []string{"commit", "-m", "x"}, "is not a read-only command"},
		{"push", []string{"push"}, "is not a read-only command"},
		{"branch delete", []string{"branch", "-D", "x"}, "is not a read-only command"},
		{"checkout", []string{"checkout", "main"}, "is not a read-only command"},
		{"log with output redirect option", []string{"log", "--output=/tmp/x"}, "is not a read-only command"},
		{"diff no-index escapes the repo", []string{"diff", "--no-index", "a", "b"}, "is not a read-only command"},
		{"show with external diff driver", []string{"show", "--ext-diff"}, "is not a read-only command"},
		{"leading global option", []string{"-C", "/tmp", "log"}, "args[0] must be a git verb"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := runGitTool(t, dir, tc.args)
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, tc.want)
		})
	}
}

func TestGitTool_MissingArgs(t *testing.T) {
	t.Parallel()
	resp := runGitTool(t, t.TempDir(), nil)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "missing args")
}

func TestGitTool_NoOutput(t *testing.T) {
	t.Parallel()
	dir := initGitRepo(t)

	resp := runGitTool(t, dir, []string{"log", "-n", "0"})
	require.False(t, resp.IsError)
	require.Equal(t, BashNoOutput, resp.Content)
}

func TestGitTool_ExitCodeSurfacesAsFailure(t *testing.T) {
	t.Parallel()
	dir := initGitRepo(t)

	resp := runGitTool(t, dir, []string{"show", "does-not-exist"})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Exit code")
}
