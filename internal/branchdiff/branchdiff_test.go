package branchdiff

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeGit answers the commands Changes runs from canned output and records
// every call, so tests can see what was and was not asked of git.
type fakeGit struct {
	calls   []string
	outputs map[string]string
	fail    map[string]error
}

func (f *fakeGit) run(_ context.Context, args ...string) (string, error) {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if err, ok := longestPrefixMatch(f.fail, call); ok {
		return "", err
	}
	out, _ := longestPrefixMatch(f.outputs, call)
	return out, nil
}

// NOTE: The longest prefix wins because map order is random.
func longestPrefixMatch[V any](m map[string]V, call string) (V, bool) {
	var (
		best  V
		found bool
		bestN = -1
	)
	for prefix, v := range m {
		if strings.HasPrefix(call, prefix) && len(prefix) > bestN {
			best, found, bestN = v, true, len(prefix)
		}
	}
	return best, found
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		outputs: map[string]string{
			"diff --no-color --stat": "x.txt | 1 +",
			"diff --no-color":        "diff --git a/x.txt b/x.txt\n+x",
			"log":                    "- feat: add x",
		},
		fail: map[string]error{},
	}
}

func TestChanges_OrdersCommitsThenDiffstatThenDiff(t *testing.T) {
	t.Parallel()

	got, err := Changes(t.Context(), newFakeGit().run, "main", "feature")
	require.NoError(t, err)
	require.Equal(t, "Commits (oldest first, at most 100):\n- feat: add x\n\n"+
		"Diffstat:\nx.txt | 1 +\n\n"+
		"Diff:\ndiff --git a/x.txt b/x.txt\n+x", got)
}

func TestChanges_ComparesAtTheMergeBase(t *testing.T) {
	t.Parallel()

	git := newFakeGit()
	_, err := Changes(t.Context(), git.run, "main", "feature")
	require.NoError(t, err)

	require.Contains(t, git.calls, "diff --no-color main...feature --", "the diff is what head added since the fork")
	require.Contains(t, git.calls, "diff --no-color --stat main...feature --")
	require.Contains(t, git.calls, "log --no-color --no-merges --reverse --max-count=100 --format=- %s main..feature --")
}

func TestChanges_OmitsCommitsSectionWhenThereAreNone(t *testing.T) {
	t.Parallel()

	git := newFakeGit()
	git.outputs["log"] = ""

	got, err := Changes(t.Context(), git.run, "main", "feature")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "Diffstat:\n"), "got %q", got)
}

func TestChanges_NoDifferenceWrapsErrNoChanges(t *testing.T) {
	t.Parallel()

	git := newFakeGit()
	git.outputs["diff --no-color"] = ""

	_, err := Changes(t.Context(), git.run, "main", "feature")
	require.ErrorIs(t, err, ErrNoChanges)
	require.ErrorContains(t, err, "no changes between main and feature")
}

func TestChanges_OutsideRepository(t *testing.T) {
	t.Parallel()

	git := newFakeGit()
	git.fail["rev-parse --is-inside-work-tree"] = errors.New("git rev-parse failed: fatal")

	_, err := Changes(t.Context(), git.run, "main", "feature")
	require.ErrorIs(t, err, ErrNotRepository)
	require.Equal(t, []string{"rev-parse --is-inside-work-tree"}, git.calls, "nothing else may run outside a repository")
}

func TestChanges_UnknownRevisionStopsBeforeDiffing(t *testing.T) {
	t.Parallel()

	git := newFakeGit()
	git.fail["rev-parse --verify --quiet nope^{commit}"] = errors.New("git rev-parse failed: ")

	_, err := Changes(t.Context(), git.run, "nope", "feature")
	require.ErrorContains(t, err, `unknown branch or commit "nope"`)
	for _, call := range git.calls {
		require.False(t, strings.HasPrefix(call, "diff"), "no diff may run for an unknown revision, ran %q", call)
	}
}

func TestChanges_RejectsOptionLookingRevisionWithoutAskingGit(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{"--output=/tmp/pwned", "-pwned"} {
		git := newFakeGit()

		_, err := Changes(t.Context(), git.run, ref, "feature")
		require.ErrorContains(t, err, "invalid revision", ref)

		_, err = Changes(t.Context(), git.run, "main", ref)
		require.ErrorContains(t, err, "invalid revision", ref)

		for _, call := range git.calls {
			require.NotContains(t, call, ref, "the ref must never reach git")
		}
	}
}

func TestChanges_PropagatesGitFailure(t *testing.T) {
	t.Parallel()

	git := newFakeGit()
	git.fail["diff --no-color main...feature"] = errors.New("git diff failed: boom")

	_, err := Changes(t.Context(), git.run, "main", "feature")
	require.ErrorContains(t, err, "git diff failed: boom")
}
