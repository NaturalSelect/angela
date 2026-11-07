package editorapproval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/google/uuid"
)

// VSCodeMCP reviews diffs through the local MCP server that VS Code's
// GitHub Copilot Chat extension exposes for its own "Copilot CLI" chat
// sessions. Its `open_diff` tool gives a real Accept/Reject decision
// instead of one inferred from whatever content is left in a file when
// a tab closes, which is all a plain `code --wait --diff` could offer.
//
// This channel is undocumented and private to the Copilot Chat
// extension: there is no compatibility guarantee on the lock file
// format, the auth scheme, or the tool's existence and shape. Every
// failure here — no lock file, dial error, protocol error — is meant to
// be handled by the caller falling back to the terminal permission
// prompt, not surfaced to the user directly.
type VSCodeMCP struct {
	// WorkingDir is compared against each lock file's advertised
	// workspace folders to find the right VS Code window.
	WorkingDir string
	// Getenv defaults to os.Getenv; tests override it.
	Getenv func(string) string
}

// Name implements Editor.
func (v VSCodeMCP) Name() string { return "vscode-mcp" }

func (v VSCodeMCP) getenv() func(string) string {
	if v.Getenv != nil {
		return v.Getenv
	}
	return os.Getenv
}

// Available implements Editor. It requires running inside VS Code's
// integrated terminal — a cheap signal that a matching window is
// nearby — and a lock file matching WorkingDir. It deliberately doesn't
// dial the socket: that cost belongs in Review, and a lock file can go
// stale between this check and the call regardless.
func (v VSCodeMCP) Available() bool {
	getenv := v.getenv()
	if !vscodemcp.InIntegratedTerminal(getenv) {
		return false
	}
	_, err := vscodemcp.Discover(getenv, v.WorkingDir)
	return err == nil
}

// Review implements Editor.
func (v VSCodeMCP) Review(ctx context.Context, req Request) (Decision, error) {
	client, err := vscodemcp.Discover(v.getenv(), v.WorkingDir)
	if err != nil {
		return Decision{}, err
	}

	res, err := client.OpenDiff(ctx, vscodemcp.OpenDiffRequest{
		FilePath:    req.FilePath,
		NewContents: req.NewContent,
		TabName:     reviewTabName(req, v.WorkingDir),
	})
	if err != nil {
		return Decision{}, err
	}

	switch res.Status {
	case vscodemcp.DiffSaved:
		return Decision{Outcome: OutcomeApprove, Content: req.NewContent}, nil
	case vscodemcp.DiffRejected:
		// No reason: this is a bare rejection, the same as clicking "Deny"
		// with no explanation in the terminal, so it should end the turn
		// instead of carrying a reason back for the model to route around.
		return Decision{Outcome: OutcomeDeny}, nil
	default:
		return Decision{}, fmt.Errorf("unexpected open_diff status %q", res.Status)
	}
}

// reviewTabName names the VS Code diff tab. It's suffixed with a short
// unique id because the server tracks open diffs by tab name, and
// Angela can have more than one review in flight at once. The
// absolute path req.Description embeds is shortened to one relative
// to workingDir when it sits inside it, so a deeply nested project
// doesn't turn the tab title into a wall of directories.
func reviewTabName(req Request, workingDir string) string {
	name := req.Description
	if name == "" {
		name = filepath.Base(req.FilePath)
	} else if rel, ok := workspaceRelative(req.FilePath, workingDir); ok {
		name = strings.Replace(name, req.FilePath, rel, 1)
	}
	return fmt.Sprintf("angela: %s (%s)", name, uuid.NewString()[:8])
}

// workspaceRelative reports path relative to workingDir, for
// shortening an absolute path embedded in a UI label. It reports
// false when path isn't absolute or doesn't sit inside workingDir, so
// callers fall back to showing it unchanged.
func workspaceRelative(path, workingDir string) (string, bool) {
	if workingDir == "" || !filepathext.SmartIsAbs(path) {
		return "", false
	}
	rel, err := filepath.Rel(workingDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

var _ Editor = VSCodeMCP{}
