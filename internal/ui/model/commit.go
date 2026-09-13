package model

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"mvdan.cc/sh/v3/syntax"

	"github.com/NaturalSelect/angela/internal/ui/util"
)

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

			command, err := signedCommitCommand(message)
			if err != nil {
				return util.ReportError(fmt.Errorf("failed to build commit command: %w", err))()
			}

			commitResp, err := m.com.Workspace.AgentRunShellCommand(ctx, "", command, 0, nil, false)
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

// signedCommitCommand builds a `git commit -s` invocation with message
// quoted as a single shell word via syntax.Quote, so it reaches git
// exactly as generated no matter what it contains: quotes, backticks,
// "$", newlines, and any other shell metacharacter are inert inside
// the quoting Quote picks. LangBash matches the variant mvdan's
// interpreter defaults to when it later parses this command (see
// internal/shell), so the quoted form round-trips exactly. Quoting
// the whole message as one word, rather than delimiting it between
// two copies of a fixed token, leaves no token for the message to
// collide with.
func signedCommitCommand(message string) (string, error) {
	quoted, err := syntax.Quote(message, syntax.LangBash)
	if err != nil {
		return "", fmt.Errorf("cannot quote commit message for shell: %w", err)
	}
	return "git commit -s -m " + quoted, nil
}
