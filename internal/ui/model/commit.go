package model

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/ui/util"
)

// commitMessageDelimiter is the heredoc terminator used to pass a
// generated commit message to git without any shell interpretation
// of its content: a single-quoted heredoc disables variable,
// command-substitution, and glob expansion inside it, so quotes,
// backticks, and "$" in the message are all passed through literally.
const commitMessageDelimiter = "ANGELA_COMMIT_EOF"

// commitStagedChanges generates a commit message from the workspace's
// currently staged changes and commits them with a sign-off, with no
// confirmation step in between: the git commands themselves are
// fixed by this function, not chosen by the model, so they carry the
// same trust level as bang-mode's user-typed shell commands — only
// the message text comes from the model. Both git commands run with
// an empty session ID so neither is persisted into the chat history,
// matching how AgentRunShellCommand is used outside of bang mode.
func (m *UI) commitStagedChanges(sessionID string) tea.Cmd {
	return tea.Batch(
		util.ReportInfo("Generating commit message from staged changes…"),
		func() tea.Msg {
			ctx := context.Background()

			diffResp, err := m.com.Workspace.AgentRunShellCommand(ctx, "", "git diff --cached", 0, nil, false)
			if err != nil {
				return util.ReportError(fmt.Errorf("failed to read staged changes: %w", err))()
			}
			if diffResp.ExitCode != 0 {
				return util.ReportError(fmt.Errorf("git diff --cached failed: %s", strings.TrimSpace(diffResp.Output)))()
			}
			diff := strings.TrimSpace(diffResp.Output)
			if diff == "" {
				return util.ReportWarn("Nothing staged to commit")()
			}

			message, err := m.com.Workspace.AgentGenerateCommitMessage(ctx, sessionID, diff)
			if err != nil {
				return util.ReportError(fmt.Errorf("failed to generate commit message: %w", err))()
			}

			commitResp, err := m.com.Workspace.AgentRunShellCommand(ctx, "", signedCommitCommand(message), 0, nil, false)
			if err != nil {
				return util.ReportError(fmt.Errorf("failed to run git commit: %w", err))()
			}
			if commitResp.ExitCode != 0 {
				return util.ReportError(fmt.Errorf("git commit failed: %s", strings.TrimSpace(commitResp.Output)))()
			}

			return util.ReportInfo("Committed: " + message)()
		},
	)
}

// signedCommitCommand builds a `git commit -s` invocation that passes
// message through a quoted heredoc so it reaches git exactly as
// generated, regardless of quotes, backticks, or "$" it contains —
// the same heredoc pattern the coder agent itself is instructed to
// use for a commit message with a body (see bash.md.tpl).
func signedCommitCommand(message string) string {
	return fmt.Sprintf("git commit -s -m \"$(cat <<'%s'\n%s\n%s\n)\"", commitMessageDelimiter, message, commitMessageDelimiter)
}
