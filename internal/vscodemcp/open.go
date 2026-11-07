package vscodemcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// OpenRequest names a single file to show in VS Code.
type OpenRequest struct {
	Path string
	// Line and Column are 1-based. Zero means unspecified, and Column
	// is ignored without a Line.
	Line   int
	Column int
}

// Open shows a single file in VS Code, optionally at a position, and
// returns once the CLI has handed the request to the editor.
//
// NOTE: The MCP server has no tool that opens a plain file (see the
// tools registered upstream in copilotcli/vscode-node/tools), so this
// goes through the `code` CLI instead. That makes it independent of the
// lock file: a zero Client can call it.
func (c *Client) Open(ctx context.Context, req OpenRequest) error {
	if req.Path == "" {
		return errors.New("open: empty path")
	}

	// NOTE: Always absolute, which also keeps a path that starts with a
	// dash from being parsed as a CLI flag.
	path, err := filepath.Abs(req.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", req.Path, err)
	}
	target := path
	if req.Line > 0 {
		target += ":" + strconv.Itoa(req.Line)
		if req.Column > 0 {
			target += ":" + strconv.Itoa(req.Column)
		}
	}

	cmd := c.command()
	args := append(slices.Clone(cmd[1:]), "--reuse-window", "--goto", target)

	run := exec.CommandContext(ctx, cmd[0], args...)
	var stderr bytes.Buffer
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		return fmt.Errorf("run %s: %w (stderr: %s)", cmd[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (c *Client) command() []string {
	if len(c.Command) > 0 {
		return c.Command
	}
	return []string{"code"}
}
