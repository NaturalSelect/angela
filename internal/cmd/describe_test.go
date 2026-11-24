package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/branchdiff"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
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
	for i := range branchdiff.MaxCommits + 5 {
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

func TestShowDescription_PrintsWhenNotOpeningInVSCode(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	showDescription(t.Context(), &stdout, &stderr, dataDir, "## Summary\nDoes a thing.", nil)

	require.Equal(t, "## Summary\nDoes a thing.\n", stdout.String())
	require.Empty(t, stderr.String())
	require.NoDirExists(t, filepath.Join(dataDir, scratch.ViewerDir),
		"nothing is written when the description is only printed")
}

func TestShowDescription_OpensInVSCodeInsteadOfPrinting(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	want := filepath.Join(dataDir, scratch.ViewerDir, describeViewerFile)

	var opened vscodemcp.OpenRequest
	open := func(_ context.Context, req vscodemcp.OpenRequest) error {
		opened = req
		return nil
	}

	var stdout, stderr bytes.Buffer
	showDescription(t.Context(), &stdout, &stderr, dataDir, "## Summary\nDoes a thing.", open)

	require.Equal(t, want, opened.Path)
	got, err := os.ReadFile(want)
	require.NoError(t, err)
	require.Equal(t, "## Summary\nDoes a thing.\n", string(got))
	require.Contains(t, stdout.String(), want)
	require.NotContains(t, stdout.String(), "Does a thing.",
		"the text lives in the editor and is not repeated in the terminal")
	require.Empty(t, stderr.String())
}

func TestShowDescription_ReusesTheSameFile(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	open := func(context.Context, vscodemcp.OpenRequest) error { return nil }

	var out bytes.Buffer
	showDescription(t.Context(), &out, &out, dataDir, "first", open)
	showDescription(t.Context(), &out, &out, dataDir, "second", open)

	entries, err := os.ReadDir(filepath.Join(dataDir, scratch.ViewerDir))
	require.NoError(t, err)
	require.Len(t, entries, 1, "a later run overwrites the file so VS Code reuses its tab")
	got, err := os.ReadFile(filepath.Join(dataDir, scratch.ViewerDir, describeViewerFile))
	require.NoError(t, err)
	require.Equal(t, "second\n", string(got))
}

func TestShowDescription_FallsBackToPrintingWhenVSCodeFails(t *testing.T) {
	t.Parallel()
	open := func(context.Context, vscodemcp.OpenRequest) error { return errors.New("code: not found") }

	var stdout, stderr bytes.Buffer
	showDescription(t.Context(), &stdout, &stderr, t.TempDir(), "## Summary\nDoes a thing.", open)

	require.Equal(t, "## Summary\nDoes a thing.\n", stdout.String(), "the text must never be lost")
	require.Contains(t, stderr.String(), "Could not open the description in VS Code")
	require.Contains(t, stderr.String(), "code: not found")
}

func TestShowDescription_FallsBackToPrintingWithoutDataDirectory(t *testing.T) {
	t.Parallel()
	called := false
	open := func(context.Context, vscodemcp.OpenRequest) error {
		called = true
		return nil
	}

	var stdout, stderr bytes.Buffer
	showDescription(t.Context(), &stdout, &stderr, "", "## Summary\nDoes a thing.", open)

	require.False(t, called)
	require.Equal(t, "## Summary\nDoes a thing.\n", stdout.String())
	require.Contains(t, stderr.String(), "the data directory is not set")
}
