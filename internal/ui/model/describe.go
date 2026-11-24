package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"mvdan.cc/sh/v3/syntax"

	"github.com/NaturalSelect/angela/internal/branchdiff"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
)

// describeViewerFile is the fixed name a description is written under in the
// viewer directory. A later run reuses the file and VS Code reuses its tab.
const describeViewerFile = "describe.md"

// describeReadyMsg carries a generated description for Update to show in a
// Viewer dialog. openErr is set when the description was meant for VS Code
// and could not be opened there.
type describeReadyMsg struct {
	base, head  string
	description string
	openErr     error
}

// handleDescribe handles dialog.ActionDescribe.
func (m *UI) handleDescribe(msg dialog.ActionDescribe) tea.Cmd {
	if msg.Base == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		m.dialog.OpenDialog(dialog.NewArguments(
			m.com,
			"Describe Branch Changes",
			"Writes a pull request description of what the head branch changed since it forked from the base. Nothing is committed or modified.",
			[]commands.Argument{
				{ID: "BASE", Title: "Base Branch", Description: "Branch or commit to compare against, such as main.", Required: true},
				{ID: "HEAD", Title: "Head Branch", Description: "Defaults to the checked out commit."},
			},
			dialog.ActionDescribe{SessionID: msg.SessionID},
		))
		return nil
	}

	m.dialog.CloseDialog(dialog.ArgumentsID)
	head := msg.Head
	if head == "" {
		head = "HEAD"
	}
	return tea.Batch(m.showPending(describingInfo), m.describeBranchCmd(msg.SessionID, msg.Base, head))
}

// describeBranchCmd gathers the changes of head against base, has the model
// describe them, and delivers the text: to the editor when the TUI runs in
// VS Code's terminal, else, or when the editor fails, back to Update as a
// describeReadyMsg.
//
// NOTE: The git commands are fixed here and run with an empty session ID, so
// none of them is persisted into the chat history. Only the description text
// comes from the model.
func (m *UI) describeBranchCmd(sessionID, base, head string) tea.Cmd {
	inVSCode := m.inVSCode()
	open := m.openInVSCode
	if open == nil {
		open = (&vscodemcp.Client{}).Open
	}
	var dir string
	var dirErr error
	if inVSCode {
		dir, dirErr = m.viewerDir()
	}

	return func() tea.Msg {
		ctx := context.Background()

		changes, err := branchdiff.Changes(ctx, m.workspaceGit, base, head)
		switch {
		case errors.Is(err, branchdiff.ErrNotRepository):
			return util.ReportWarn("The workspace is not inside a git repository")()
		case errors.Is(err, branchdiff.ErrNoChanges):
			return util.ReportWarn(err.Error())()
		case err != nil:
			return util.ReportError(err)()
		}

		description, err := m.com.Workspace.AgentGenerateChangeDescription(ctx, sessionID, base, head, changes)
		if err != nil {
			return util.ReportError(fmt.Errorf("failed to generate change description: %w", err))()
		}

		ready := describeReadyMsg{base: base, head: head, description: description}
		if !inVSCode {
			return ready
		}
		if dirErr != nil {
			ready.openErr = dirErr
			return ready
		}
		if err := openDescriptionInVSCode(ctx, dir, description, open); err != nil {
			ready.openErr = err
			return ready
		}
		return util.ReportInfo("Opened the description in VS Code")()
	}
}

// openDescriptionInVSCode writes description to dir and opens it with open.
//
// NOTE: `code` opens files, so the text goes to a file in the viewer
// directory, which `angela vacuum` also cleans.
func openDescriptionInVSCode(ctx context.Context, dir, description string, open func(context.Context, vscodemcp.OpenRequest) error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, describeViewerFile)
	if err := os.WriteFile(path, []byte(description+"\n"), 0o600); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, openInVSCodeTimeout)
	defer cancel()
	return open(ctx, vscodemcp.OpenRequest{Path: path})
}

// openDescriptionViewer shows a generated description in a Viewer dialog.
func (m *UI) openDescriptionViewer(msg describeReadyMsg) {
	// NOTE: Closing first keeps a second run from stacking a second viewer.
	m.dialog.CloseDialog(dialog.ViewerID)
	m.dialog.OpenDialog(dialog.NewViewer(m.com, dialog.ViewerSpec{
		Title:     "Describe",
		TitleInfo: msg.base + "..." + msg.head,
		Content:   msg.description,
		Markdown:  true,
	}))
}

// workspaceGit runs git in the workspace's directory through its shell and
// returns the trimmed output. Every argument is quoted as one inert shell
// word, so a branch name typed by the user cannot add commands or options.
func (m *UI) workspaceGit(ctx context.Context, args ...string) (string, error) {
	words := make([]string, 0, len(args)+1)
	words = append(words, "git")
	for _, arg := range args {
		quoted, err := syntax.Quote(arg, syntax.LangBash)
		if err != nil {
			return "", fmt.Errorf("cannot quote %q for shell: %w", arg, err)
		}
		words = append(words, quoted)
	}

	resp, err := m.com.Workspace.AgentRunShellCommand(ctx, "", strings.Join(words, " "), 0, nil, false)
	if err != nil {
		return "", err
	}
	output := strings.TrimSpace(resp.Output)
	if resp.ExitCode != 0 {
		return "", fmt.Errorf("git %s failed: %s", args[0], output)
	}
	return output, nil
}
