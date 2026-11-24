package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/branchdiff"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

const (
	// describeViewerFile is the fixed name the description is written
	// under in the viewer directory. A later run reuses the file and VS
	// Code reuses its tab.
	describeViewerFile = "describe.md"

	// describeOpenTimeout bounds the whole open so a stuck `code` CLI
	// cannot hang the command.
	describeOpenTimeout = 30 * time.Second
)

func init() {
	describeCmd.Flags().Int("max-diff-runes", 0, "Maximum characters (runes) of the commit list, diffstat and diff sent to the model; overrides options.commit_diff_runes (default 48000)")
}

var describeCmd = &cobra.Command{
	Use:   "describe <base> [head]",
	Short: "Describe the changes on a branch relative to another",
	Long: `Compare a branch with a base branch and print a generated description of
what it changed, ready to paste into a pull request. The description has no
title. This is the branch-to-branch counterpart of commit: nothing is
committed, staged or modified.

The comparison is "git diff base...head": the changes head made since it
forked from base, which is what a pull request shows. head defaults to the
currently checked out commit. Both can be any commit-ish, such as a branch,
a tag or a remote branch like origin/main.

In VS Code's integrated terminal the description is opened in the editor
instead of printed, like viewing a report in the interactive UI. It is
printed as usual when the output is redirected or VS Code cannot open it.`,
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

		// NOTE: Redirected output is for a pipe or a file, so it is never
		// handed to the editor.
		var open vscodeOpener
		if term.IsTerminal(os.Stdout.Fd()) && vscodemcp.InIntegratedTerminal(nil) {
			open = (&vscodemcp.Client{}).Open
		}
		showDescription(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), ws.Config().Options.DataDirectory, description, open)
		return nil
	},
}

// vscodeOpener shows a file in VS Code.
type vscodeOpener func(context.Context, vscodemcp.OpenRequest) error

// showDescription delivers description to the user. With an open function it
// goes to VS Code, and anything that stops that from working falls back to
// printing, so the generated text is never lost.
func showDescription(ctx context.Context, stdout, stderr io.Writer, dataDir, description string, open vscodeOpener) {
	if open != nil {
		path, err := openDescriptionInVSCode(ctx, dataDir, description, open)
		if err == nil {
			fmt.Fprintf(stdout, "Opened the description in VS Code: %s\n", path)
			return
		}
		fmt.Fprintf(stderr, "Could not open the description in VS Code, printing it instead: %v\n", err)
	}
	fmt.Fprintln(stdout, description)
}

// openDescriptionInVSCode writes description to the viewer directory under
// dataDir and opens it with open, returning the file's path.
//
// NOTE: `code` opens files, so the text goes to a file. The viewer directory
// sits in the data directory rather than the system temp dir, where on a
// shared host another user could create the name first and swap the file
// before `code` opens it. `angela vacuum` also cleans it.
func openDescriptionInVSCode(ctx context.Context, dataDir, description string, open vscodeOpener) (string, error) {
	if dataDir == "" {
		return "", errors.New("the data directory is not set")
	}
	dir := filepath.Join(dataDir, scratch.ViewerDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, describeViewerFile)
	if err := os.WriteFile(path, []byte(description+"\n"), 0o600); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, describeOpenTimeout)
	defer cancel()
	if err := open(ctx, vscodemcp.OpenRequest{Path: path}); err != nil {
		return "", err
	}
	return path, nil
}

// branchChanges gathers what a description of head against base in dir is
// written from.
func branchChanges(ctx context.Context, dir, base, head string) (string, error) {
	changes, err := branchdiff.Changes(ctx, func(ctx context.Context, args ...string) (string, error) {
		return runGit(ctx, dir, args...)
	}, base, head)
	if errors.Is(err, branchdiff.ErrNotRepository) {
		return "", fmt.Errorf("%s is not inside a git repository", dir)
	}
	return changes, err
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
