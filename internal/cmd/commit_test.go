package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitCmd := exec.Command("git", args...)
	gitCmd.Dir = dir
	out, err := gitCmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	runGitForTest(t, dir, "init", "-q")
	runGitForTest(t, dir, "config", "user.name", "Test User")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func stageFileForTest(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	runGitForTest(t, dir, "add", name)
}

func TestStagedDiff_NothingStaged(t *testing.T) {
	t.Parallel()
	dir := newTestRepo(t)

	_, err := stagedDiff(t.Context(), dir)
	require.ErrorIs(t, err, errNothingStaged)
}

func TestStagedDiff_ReturnsStagedChangesOnly(t *testing.T) {
	t.Parallel()
	dir := newTestRepo(t)
	stageFileForTest(t, dir, "staged.txt", "staged\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unstaged.txt"), []byte("unstaged\n"), 0o644))

	diff, err := stagedDiff(t.Context(), dir)
	require.NoError(t, err)
	require.Contains(t, diff, "staged.txt")
	require.NotContains(t, diff, "unstaged.txt")
}

func TestStagedDiff_OutsideRepository(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	_, err := stagedDiff(t.Context(), t.TempDir())
	require.Error(t, err)
	require.NotErrorIs(t, err, errNothingStaged)
	require.Contains(t, err.Error(), "not inside a git repository")
}

// TestCommitStaged_RecordsMessageLiterally pins that the generated message
// reaches git untouched: shell metacharacters stay inert and the sign-off
// trailer is added.
func TestCommitStaged_RecordsMessageLiterally(t *testing.T) {
	t.Parallel()
	dir := newTestRepo(t)
	stageFileForTest(t, dir, "a.txt", "a\n")

	message := "fix: handle `$HOME` and \"quotes\"\n\nBody line with 'single' quotes."
	var stdout, stderr bytes.Buffer
	require.NoError(t, commitStaged(t.Context(), dir, message, &stdout, &stderr))

	got := runGitForTest(t, dir, "log", "-1", "--format=%B")
	require.Contains(t, got, message)
	require.Contains(t, got, "Signed-off-by: Test User <test@example.com>")
}

// TestCommitStaged_SkipsHooks pins --no-verify: a failing pre-commit hook
// must not block recording the staged diff.
func TestCommitStaged_SkipsHooks(t *testing.T) {
	t.Parallel()
	dir := newTestRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	stageFileForTest(t, dir, "a.txt", "a\n")

	var stdout, stderr bytes.Buffer
	require.NoError(t, commitStaged(t.Context(), dir, "chore: add a", &stdout, &stderr))
}

func TestCommitStaged_NothingToCommit(t *testing.T) {
	t.Parallel()
	dir := newTestRepo(t)

	var stdout, stderr bytes.Buffer
	err := commitStaged(t.Context(), dir, "chore: nothing", &stdout, &stderr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "git commit failed")
}
