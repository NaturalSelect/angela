package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// collectInfoMsgs runs cmd and everything it batches, returning every
// util.InfoMsg produced, in order. commitStagedChanges always starts
// with the "Generating…" info message before whatever the async
// fetch produces, so callers typically care about the last entry.
func collectInfoMsgs(cmd tea.Cmd) []util.InfoMsg {
	var msgs []util.InfoMsg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case util.InfoMsg:
			msgs = append(msgs, m)
		case tea.BatchMsg:
			for _, sub := range m {
				walk(sub)
			}
		}
	}
	walk(cmd)
	return msgs
}

// TestCommitStagedChanges_Success drives the whole happy path: reading
// the staged diff, generating a message from it, and running the
// signed commit, ending in a single confirmation toast that includes
// the generated message.
func TestCommitStagedChanges_Success(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "diff --git a/x b/x\n+y", ExitCode: 0}, nil)
	ws.EXPECT().AgentGenerateCommitMessage(gomock.Any(), "s1", "diff --git a/x b/x\n+y").
		Return("fix: add y", nil)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", signedCommitCommand("fix: add y"), 0, nil, false).
		Return(proto.ShellCommandResponse{ExitCode: 0}, nil)

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2, "expected the initial \"Generating…\" toast plus the final outcome")
	require.Equal(t, util.InfoTypeInfo, msgs[1].Type)
	require.Equal(t, "Committed: fix: add y", msgs[1].Msg)
}

// TestCommitStagedChanges_DiffReadError pins that a transport failure
// reading the staged diff is reported and never reaches generation or
// commit — the mock has no expectations for either, so an unwanted
// call would fail the test on its own.
func TestCommitStagedChanges_DiffReadError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{}, errors.New("boom"))

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2)
	require.Equal(t, util.InfoTypeError, msgs[1].Type)
	require.Contains(t, msgs[1].Msg, "failed to read staged changes")
	require.Contains(t, msgs[1].Msg, "boom")
}

// TestCommitStagedChanges_DiffCommandNonZeroExit pins that a non-zero
// exit from `git diff --cached` (e.g. outside a repo) is reported
// with its output, distinctly from a transport error.
func TestCommitStagedChanges_DiffCommandNonZeroExit(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "fatal: not a git repository", ExitCode: 128}, nil)

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2)
	require.Equal(t, util.InfoTypeError, msgs[1].Type)
	require.Contains(t, msgs[1].Msg, "git diff --cached failed")
	require.Contains(t, msgs[1].Msg, "fatal: not a git repository")
}

// TestCommitStagedChanges_NothingStaged pins that a diff which is
// blank once trimmed (e.g. only whitespace) is treated as "nothing to
// commit" rather than being handed to the commit-message model.
func TestCommitStagedChanges_NothingStaged(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "   \n\t", ExitCode: 0}, nil)

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2)
	require.Equal(t, util.InfoTypeWarn, msgs[1].Type)
	require.Equal(t, "Nothing staged to commit", msgs[1].Msg)
}

// TestCommitStagedChanges_GenerateError pins that a failure from the
// commit-message model is reported and never reaches the commit
// step.
func TestCommitStagedChanges_GenerateError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "diff --git a/x b/x\n+y", ExitCode: 0}, nil)
	ws.EXPECT().AgentGenerateCommitMessage(gomock.Any(), "s1", "diff --git a/x b/x\n+y").
		Return("", errors.New("model unavailable"))

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2)
	require.Equal(t, util.InfoTypeError, msgs[1].Type)
	require.Contains(t, msgs[1].Msg, "failed to generate commit message")
	require.Contains(t, msgs[1].Msg, "model unavailable")
}

// TestCommitStagedChanges_CommitCommandError pins that a transport
// failure running the final `git commit` is reported.
func TestCommitStagedChanges_CommitCommandError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "diff --git a/x b/x\n+y", ExitCode: 0}, nil)
	ws.EXPECT().AgentGenerateCommitMessage(gomock.Any(), "s1", "diff --git a/x b/x\n+y").
		Return("fix: add y", nil)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", signedCommitCommand("fix: add y"), 0, nil, false).
		Return(proto.ShellCommandResponse{}, errors.New("exec failed"))

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2)
	require.Equal(t, util.InfoTypeError, msgs[1].Type)
	require.Contains(t, msgs[1].Msg, "failed to run git commit")
	require.Contains(t, msgs[1].Msg, "exec failed")
}

// TestCommitStagedChanges_CommitCommandNonZeroExit pins that a
// non-zero exit from `git commit` itself (e.g. a rejected hook) is
// reported with its output.
func TestCommitStagedChanges_CommitCommandNonZeroExit(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "diff --git a/x b/x\n+y", ExitCode: 0}, nil)
	ws.EXPECT().AgentGenerateCommitMessage(gomock.Any(), "s1", "diff --git a/x b/x\n+y").
		Return("fix: add y", nil)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", signedCommitCommand("fix: add y"), 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "pre-commit hook rejected", ExitCode: 1}, nil)

	m := newBusyUIWithWorkspace(ws)
	msgs := collectInfoMsgs(m.commitStagedChanges("s1"))

	require.Len(t, msgs, 2)
	require.Equal(t, util.InfoTypeError, msgs[1].Type)
	require.Contains(t, msgs[1].Msg, "git commit failed")
	require.Contains(t, msgs[1].Msg, "pre-commit hook rejected")
}

// TestSignedCommitCommand pins the exact heredoc shape the commit
// message is wrapped in: a single-quoted heredoc so quotes,
// backticks, and "$" in a generated message reach git literally
// instead of being interpreted by the shell.
func TestSignedCommitCommand(t *testing.T) {
	t.Parallel()

	got := signedCommitCommand("fix: handle `$HOME` and \"quotes\"")
	want := "git commit -s -m \"$(cat <<'ANGELA_COMMIT_EOF'\n" +
		"fix: handle `$HOME` and \"quotes\"\n" +
		"ANGELA_COMMIT_EOF\n)\""
	require.Equal(t, want, got)
}

// TestCommitRefusesBusySession mirrors TestSummarizeRefusesItsOwnSessionBusy
// for the /commit dispatch gate: committing while a turn is running
// risks capturing a half-finished edit, so a busy session must be
// refused before touching the workspace at all.
func TestCommitRefusesBusySession(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(true)

	m := newSummarizeGateUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionCommit{SessionID: "current"})
	require.NotNil(t, cmd)
	msg := cmd()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "a busy session must report the warning message, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestCommitDispatchesWhenIdle mirrors
// TestSummarizeIgnoresUnrelatedSessionBusy: an idle session's commit
// must actually run end to end through the dialog dispatch path, not
// just through commitStagedChanges called directly.
func TestCommitDispatchesWhenIdle(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(false)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", "git diff --cached", 0, nil, false).
		Return(proto.ShellCommandResponse{Output: "diff --git a/x b/x\n+y", ExitCode: 0}, nil)
	ws.EXPECT().AgentGenerateCommitMessage(gomock.Any(), "current", "diff --git a/x b/x\n+y").
		Return("fix: add y", nil)
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", signedCommitCommand("fix: add y"), 0, nil, false).
		Return(proto.ShellCommandResponse{ExitCode: 0}, nil)

	m := newSummarizeGateUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionCommit{SessionID: "current"})
	require.NotNil(t, cmd, "an idle session's commit must be dispatched, not refused")

	msgs := collectInfoMsgs(cmd)
	require.NotEmpty(t, msgs)
	require.Equal(t, "Committed: fix: add y", msgs[len(msgs)-1].Msg)
}
