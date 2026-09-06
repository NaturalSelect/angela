package editorapproval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VSCode reviews diffs using the Visual Studio Code CLI's `--wait
// --diff` mode: it writes the before/after content to temporary files,
// opens them in VS Code's diff editor, and blocks until the user closes
// the tab. VS Code deletes an internal marker file on close, which is
// what makes `code --wait` block the calling process in the first
// place — the same mechanism git relies on for `core.editor`.
//
// That view has no dedicated accept/reject action, so the decision is
// inferred from the final content of the "after" file: content left
// different from Request.OldContent is treated as an approval (using
// whatever the reviewer left behind, which may differ from
// Request.NewContent if they edited it further); content reverted back
// to Request.OldContent is treated as a denial.
type VSCode struct {
	// Command is the CLI invocation used to launch the editor, e.g.
	// []string{"code"}. Defaults to []string{"code"} when empty. Tests
	// override it to point at a stand-in binary.
	Command []string
}

// Name implements Editor.
func (v VSCode) Name() string { return "vscode" }

func (v VSCode) command() []string {
	if len(v.Command) > 0 {
		return v.Command
	}
	return []string{"code"}
}

// Available implements Editor.
func (v VSCode) Available() bool {
	_, err := exec.LookPath(v.command()[0])
	return err == nil
}

// Review implements Editor.
func (v VSCode) Review(ctx context.Context, req Request) (Decision, error) {
	dir, err := os.MkdirTemp("", "angela-vscode-diff-*")
	if err != nil {
		return Decision{}, fmt.Errorf("create temp dir for diff review: %w", err)
	}
	defer os.RemoveAll(dir)

	base := filepath.Base(req.FilePath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "file"
	}
	oldPath := filepath.Join(dir, "before-"+base)
	newPath := filepath.Join(dir, "after-"+base)

	if err := os.WriteFile(oldPath, []byte(req.OldContent), 0o600); err != nil {
		return Decision{}, fmt.Errorf("write before file for diff review: %w", err)
	}
	if err := os.WriteFile(newPath, []byte(req.NewContent), 0o600); err != nil {
		return Decision{}, fmt.Errorf("write after file for diff review: %w", err)
	}

	cmd := v.command()
	args := make([]string, 0, len(cmd)-1+4)
	args = append(args, cmd[1:]...)
	args = append(args, "--wait", "--diff", oldPath, newPath)

	run := exec.CommandContext(ctx, cmd[0], args...)
	var stderr bytes.Buffer
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		return Decision{}, fmt.Errorf("run %s: %w (stderr: %s)", cmd[0], err, strings.TrimSpace(stderr.String()))
	}

	after, err := os.ReadFile(newPath)
	if err != nil {
		return Decision{}, fmt.Errorf("read after file for diff review: %w", err)
	}

	if string(after) == req.OldContent {
		return Decision{Outcome: OutcomeDeny, Reason: "reverted to the original content in the editor"}, nil
	}
	return Decision{Outcome: OutcomeApprove, Content: string(after)}, nil
}

var _ Editor = VSCode{}
