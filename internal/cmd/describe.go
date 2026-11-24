package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// maxDescribeCommits caps how many commit subjects are sent to the model,
// so a long-lived branch cannot crowd the diff out of the diff budget.
const maxDescribeCommits = 100

func init() {
	describeCmd.Flags().Int("max-diff-runes", 0, "Maximum characters (runes) of the commit list, diffstat and diff sent to the model; overrides options.commit_diff_runes (default 48000)")
}

var describeCmd = &cobra.Command{
	Use:   "describe <base> [head]",
	Short: "Describe the changes on a branch relative to another",
	Long: `Compare a branch with a base branch and print a generated description of
what it changed, ready to paste into a pull request. This is the
branch-to-branch counterpart of commit: nothing is committed, staged or
modified.

The comparison is "git diff base...head": the changes head made since it
forked from base, which is what a pull request shows. head defaults to the
currently checked out commit. Both can be any commit-ish, such as a branch,
a tag or a remote branch like origin/main.`,
	Example: `# Describe the current branch against main
angela describe main

# Describe a named branch against another, without checking it out
angela describe origin/main feature/login

# Send more of a large diff to the model
angela describe main --max-diff-runes 100000`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		if n := commitDiffRunesFlag(cmd); n != nil && *n < 1 {
			return fmt.Errorf("--max-diff-runes must be positive, got %d", *n)
		}

		base, head := args[0], "HEAD"
		if len(args) == 2 {
			head = args[1]
		}

		// NOTE: Resolved to an absolute path before setup because
		// ResolveCwd changes the process directory, which would
		// invalidate a relative --cwd for every later use.
		cwdFlag, _ := cmd.Flags().GetString("cwd")
		repoDir, err := filepath.Abs(cwdFlag)
		if err != nil {
			return fmt.Errorf("failed to resolve working directory: %w", err)
		}

		changes, err := branchChanges(ctx, repoDir, base, head)
		if err != nil {
			return err
		}

		ws, cleanup, err := setupLocalWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		if !ws.Config().IsConfigured() {
			return fmt.Errorf("no providers configured - please run 'angela' to set up a provider interactively")
		}

		description, err := ws.AgentGenerateChangeDescription(ctx, "", base, head, changes)
		if err != nil {
			return fmt.Errorf("failed to generate change description: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), description)
		return nil
	},
}

// branchChanges gathers what a description of head against base is written
// from: the commit subjects, the diffstat and the diff of base...head, in
// that order. The diff comes last because it is the part cut when the
// model's budget runs out, and the other two still cover the whole branch.
// It returns an error when the two sides do not differ.
func branchChanges(ctx context.Context, dir, base, head string) (string, error) {
	// NOTE: Outside a repository git reinterprets some of these commands
	// as no-index diffs and prints its entire usage text, so check first.
	if _, err := runGit(ctx, dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", fmt.Errorf("%s is not inside a git repository", dir)
	}

	for _, ref := range []string{base, head} {
		if err := verifyRef(ctx, dir, ref); err != nil {
			return "", err
		}
	}

	diff, err := runGit(ctx, dir, "diff", "--no-color", base+"..."+head, "--")
	if err != nil {
		return "", err
	}
	if diff == "" {
		return "", fmt.Errorf("no changes between %s and %s", base, head)
	}

	stat, err := runGit(ctx, dir, "diff", "--no-color", "--stat", base+"..."+head, "--")
	if err != nil {
		return "", err
	}

	commits, err := runGit(ctx, dir, "log", "--no-color", "--no-merges", "--reverse",
		fmt.Sprintf("--max-count=%d", maxDescribeCommits), "--format=- %s", base+".."+head, "--")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if commits != "" {
		fmt.Fprintf(&b, "Commits (oldest first, at most %d):\n%s\n\n", maxDescribeCommits, commits)
	}
	fmt.Fprintf(&b, "Diffstat:\n%s\n\nDiff:\n%s", stat, diff)
	return b.String(), nil
}

// verifyRef checks that ref names a commit in dir.
func verifyRef(ctx context.Context, dir, ref string) error {
	// NOTE: A leading dash would make git read the ref as an option when it
	// is joined into a range such as base...head below.
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid revision %q", ref)
	}
	if _, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return fmt.Errorf("unknown branch or commit %q", ref)
	}
	return nil
}

// runGit runs git in dir and returns its trimmed standard output. A failure
// carries git's own message from standard error.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	var stderr bytes.Buffer
	c.Stderr = &stderr

	out, err := c.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New("git " + args[0] + " failed: " + detail)
	}
	return strings.TrimSpace(string(out)), nil
}
