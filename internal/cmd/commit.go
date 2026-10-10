package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var errNothingStaged = errors.New("nothing staged to commit")

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Commit staged changes with a generated message",
	Long: `Generate a commit message for the staged changes and commit them, without
opening the interactive UI. This is the command-line counterpart of /commit:
the commit is signed off, repository git hooks are skipped, and nothing
happens unless changes are staged.`,
	Example: `# Stage some changes, then commit them with a generated message
git add internal/cmd
angela commit`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		// NOTE: Resolved to an absolute path before setup because
		// ResolveCwd changes the process directory, which would
		// invalidate a relative --cwd for every later use.
		cwdFlag, _ := cmd.Flags().GetString("cwd")
		repoDir, err := filepath.Abs(cwdFlag)
		if err != nil {
			return fmt.Errorf("failed to resolve working directory: %w", err)
		}

		diff, err := stagedDiff(ctx, repoDir)
		if err != nil {
			return err
		}

		ws, cleanup, err := setupWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		if !ws.Config().IsConfigured() {
			return fmt.Errorf("no providers configured - please run 'angela' to set up a provider interactively")
		}

		message, err := ws.AgentGenerateCommitMessage(ctx, "", diff)
		if err != nil {
			return fmt.Errorf("failed to generate commit message: %w", err)
		}

		return commitStaged(ctx, repoDir, message, cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

// stagedDiff returns the output of `git diff --cached` run in dir, or
// errNothingStaged when it is blank.
func stagedDiff(ctx context.Context, dir string) (string, error) {
	// NOTE: Outside a repository git reinterprets `diff --cached` as a
	// no-index diff and prints its entire usage text, so check first.
	probe := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	probe.Dir = dir
	if err := probe.Run(); err != nil {
		return "", fmt.Errorf("%s is not inside a git repository", dir)
	}

	diffCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--no-color")
	diffCmd.Dir = dir
	var stderr bytes.Buffer
	diffCmd.Stderr = &stderr

	out, err := diffCmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git diff --cached failed: %s", detail)
	}

	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return "", errNothingStaged
	}
	return diff, nil
}

// commitStaged commits what is staged in dir with message, streaming git's
// own output to stdout and stderr.
//
// NOTE: The message is passed as a single argv entry, so no shell ever sees
// it. --no-verify matches /commit: record the staged diff as it stands
// instead of running whatever hooks the repository wired into git.
func commitStaged(ctx context.Context, dir, message string, stdout, stderr io.Writer) error {
	commit := exec.CommandContext(ctx, "git", "commit", "-s", "--no-verify", "-m", message)
	commit.Dir = dir
	commit.Stdout = stdout
	commit.Stderr = stderr
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}
	return nil
}
