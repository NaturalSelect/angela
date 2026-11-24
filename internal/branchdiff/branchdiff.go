// Package branchdiff gathers what a description of one branch against another
// is written from. It runs git through a caller-supplied function, so the CLI
// can use a plain subprocess and the TUI can use the workspace's shell.
package branchdiff

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// MaxCommits caps how many commit subjects Changes lists. The newest ones are
// kept when a branch has more.
const MaxCommits = 100

// ErrNotRepository is returned when git is not run inside a work tree.
var ErrNotRepository = errors.New("not inside a git repository")

// ErrNoChanges is returned, wrapped, when head adds nothing to base.
var ErrNoChanges = errors.New("no changes")

// Git runs git with args and returns its trimmed standard output. A failure
// must carry git's own message.
type Git func(ctx context.Context, args ...string) (string, error)

// Changes gathers what a description of head against base is written from:
// the commit subjects, the diffstat and the diff of base...head, in that
// order. The diff comes last because it is the part cut when the model's
// budget runs out, and the other two still cover the whole branch. It returns
// an error when the two sides do not differ.
func Changes(ctx context.Context, git Git, base, head string) (string, error) {
	// NOTE: Outside a repository git reinterprets some of these commands
	// as no-index diffs and prints its entire usage text, so check first.
	if _, err := git(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", ErrNotRepository
	}

	for _, ref := range []string{base, head} {
		if err := verifyRef(ctx, git, ref); err != nil {
			return "", err
		}
	}

	diff, err := git(ctx, "diff", "--no-color", base+"..."+head, "--")
	if err != nil {
		return "", err
	}
	if diff == "" {
		return "", fmt.Errorf("%w between %s and %s", ErrNoChanges, base, head)
	}

	stat, err := git(ctx, "diff", "--no-color", "--stat", base+"..."+head, "--")
	if err != nil {
		return "", err
	}

	commits, err := git(ctx, "log", "--no-color", "--no-merges", "--reverse",
		fmt.Sprintf("--max-count=%d", MaxCommits), "--format=- %s", base+".."+head, "--")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if commits != "" {
		fmt.Fprintf(&b, "Commits (oldest first, at most %d):\n%s\n\n", MaxCommits, commits)
	}
	fmt.Fprintf(&b, "Diffstat:\n%s\n\nDiff:\n%s", stat, diff)
	return b.String(), nil
}

// verifyRef checks that ref names a commit.
func verifyRef(ctx context.Context, git Git, ref string) error {
	// NOTE: A leading dash would make git read the ref as an option when it
	// is joined into a range such as base...head.
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid revision %q", ref)
	}
	if _, err := git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return fmt.Errorf("unknown branch or commit %q", ref)
	}
	return nil
}
