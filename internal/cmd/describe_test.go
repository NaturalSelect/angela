package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newBranchRepo returns a repository where "base" and "feature" fork from one
// commit. feature adds feature.txt in a commit titled "feat: add feature";
// base then moves on with base-only.txt, so a two-dot comparison would
// wrongly report that file as removed. The repository is left on feature.
func newBranchRepo(t *testing.T) string {
	t.Helper()
	dir := newTestRepo(t)

	commitFileForTest(t, dir, "shared.txt", "shared\n", "chore: initial commit")
	runGitForTest(t, dir, "branch", "base")
	runGitForTest(t, dir, "checkout", "-q", "-b", "feature")
	commitFileForTest(t, dir, "feature.txt", "feature\n", "feat: add feature")

	runGitForTest(t, dir, "checkout", "-q", "base")
	commitFileForTest(t, dir, "base-only.txt", "base only\n", "chore: base moves on")
	runGitForTest(t, dir, "checkout", "-q", "feature")
	return dir
}

func commitFileForTest(t *testing.T, dir, name, content, subject string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	runGitForTest(t, dir, "add", name)
	runGitForTest(t, dir, "commit", "-q", "-m", subject)
}

func TestBranchChanges_DescribesOnlyWhatTheBranchChanged(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)

	changes, err := branchChanges(t.Context(), dir, "base", "feature")
	require.NoError(t, err)
	require.Contains(t, changes, "- feat: add feature")
	require.Contains(t, changes, "Diffstat:")
	require.Contains(t, changes, "+feature")
	require.NotContains(t, changes, "base-only.txt",
		"changes made on the base after the fork are not the branch's own")
	require.NotContains(t, changes, "base moves on")
}

func TestBranchChanges_HeadDefaultsToCheckedOutCommitByName(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)

	fromHead, err := branchChanges(t.Context(), dir, "base", "HEAD")
	require.NoError(t, err)
	named, err := branchChanges(t.Context(), dir, "base", "feature")
	require.NoError(t, err)
	require.Equal(t, named, fromHead)
}

func TestBranchChanges_ComparesBranchesWithoutCheckingThemOut(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)
	runGitForTest(t, dir, "checkout", "-q", "base")

	changes, err := branchChanges(t.Context(), dir, "base", "feature")
	require.NoError(t, err)
	require.Contains(t, changes, "feature.txt")
}

func TestBranchChanges_NoDifference(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)

	_, err := branchChanges(t.Context(), dir, "feature", "feature")
	require.ErrorContains(t, err, "no changes between feature and feature")
}

func TestBranchChanges_OnlyCommitsBeyondTheLimitAreDropped(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)
	for i := range maxDescribeCommits + 5 {
		runGitForTest(t, dir, "commit", "-q", "--allow-empty", "-m", "chore: filler "+string(rune('a'+i%26)))
	}
	commitFileForTest(t, dir, "last.txt", "last\n", "feat: the newest commit")

	changes, err := branchChanges(t.Context(), dir, "base", "feature")
	require.NoError(t, err)
	require.Contains(t, changes, "- feat: the newest commit")
	require.NotContains(t, changes, "- feat: add feature",
		"the oldest commit is the one dropped when the branch exceeds the cap")
	require.Contains(t, changes, "+feature", "the diff still covers the dropped commit's changes")
}

func TestBranchChanges_UnknownRevision(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)

	_, err := branchChanges(t.Context(), dir, "no-such-branch", "feature")
	require.ErrorContains(t, err, `unknown branch or commit "no-such-branch"`)

	_, err = branchChanges(t.Context(), dir, "base", "no-such-branch")
	require.ErrorContains(t, err, `unknown branch or commit "no-such-branch"`)
}

func TestBranchChanges_RejectsOptionLookingRevision(t *testing.T) {
	t.Parallel()
	dir := newBranchRepo(t)

	_, err := branchChanges(t.Context(), dir, "--output=/tmp/pwned", "feature")
	require.ErrorContains(t, err, "invalid revision")
}

func TestBranchChanges_OutsideRepository(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	_, err := branchChanges(t.Context(), t.TempDir(), "main", "HEAD")
	require.ErrorContains(t, err, "not inside a git repository")
}

func TestDescribeCmd_Arguments(t *testing.T) {
	t.Parallel()

	require.Error(t, describeCmd.Args(describeCmd, nil), "a base is required")
	require.NoError(t, describeCmd.Args(describeCmd, []string{"main"}))
	require.NoError(t, describeCmd.Args(describeCmd, []string{"main", "feature"}))
	require.Error(t, describeCmd.Args(describeCmd, []string{"a", "b", "c"}))
}

func TestDescribeCmd_DiffBudgetFlag(t *testing.T) {
	t.Parallel()

	flag := describeCmd.Flags().Lookup("max-diff-runes")
	require.NotNil(t, flag, "the budget flag must exist so commitDiffRunesFlag finds it")
	require.Equal(t, "0", flag.DefValue)
}
