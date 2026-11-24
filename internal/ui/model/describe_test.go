package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// describeTestChanges is what the git outputs stubbed by expectBranchChanges
// add up to, which is what the agent must be handed.
const describeTestChanges = "Commits (oldest first, at most 100):\n- feat: add x\n\n" +
	"Diffstat:\nx.txt | 1 +\n\n" +
	"Diff:\ndiff --git a/x.txt b/x.txt\n+x"

// expectGit stubs one workspace shell command to answer with output.
func expectGit(ws *MockWorkspace, command, output string, exitCode int) {
	ws.EXPECT().AgentRunShellCommand(gomock.Any(), "", command, 0, nil, false).
		Return(workspace.ShellCommandResponse{Output: output, ExitCode: exitCode}, nil)
}

// expectBranchChanges stubs every git command the comparison of head against
// base runs, for a repository where they differ by one commit.
func expectBranchChanges(ws *MockWorkspace, base, head string) {
	expectGit(ws, "git rev-parse --is-inside-work-tree", "true\n", 0)
	expectGit(ws, "git rev-parse --verify --quiet '"+base+"^{commit}'", "1111111\n", 0)
	expectGit(ws, "git rev-parse --verify --quiet '"+head+"^{commit}'", "2222222\n", 0)
	expectGit(ws, "git diff --no-color "+base+"..."+head+" --", "diff --git a/x.txt b/x.txt\n+x\n", 0)
	expectGit(ws, "git diff --no-color --stat "+base+"..."+head+" --", " x.txt | 1 +\n", 0)
	expectGit(ws, "git log --no-color --no-merges --reverse '--max-count=100' '--format=- %s' "+base+".."+head+" --", "- feat: add x\n", 0)
}

// TestHandleDialogMsg_ActionDescribe_EmptyBaseOpensArguments verifies that
// picking "Describe Branch Changes" from the palette asks for the branches
// first and touches neither git nor the model.
func TestHandleDialogMsg_ActionDescribe_EmptyBaseOpensArguments(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionDescribe{SessionID: "s1"})
	require.Nil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the form opens")
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
}

// TestArgumentsDialog_DescribeFillsBaseAndHead verifies the form hands the
// typed branches to the action, trimmed, and keeps the session it was
// opened for.
func TestArgumentsDialog_DescribeFillsBaseAndHead(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{
		{ID: "BASE", Title: "Base Branch", Required: true},
		{ID: "HEAD", Title: "Head Branch"},
	}, dialog.ActionDescribe{SessionID: "s1"})

	a.HandleMsg(keyMsg(" main "))
	a.HandleMsg(keyMsg("down"))
	a.HandleMsg(keyMsg("feature"))

	require.Equal(t, dialog.ActionDescribe{SessionID: "s1", Base: "main", Head: "feature"}, a.HandleMsg(keyMsg("enter")))
}

// TestArgumentsDialog_DescribeLeavesHeadEmpty verifies an untouched head
// reaches the action empty, which means the checked out commit.
func TestArgumentsDialog_DescribeLeavesHeadEmpty(t *testing.T) {
	t.Parallel()

	a := argumentsDlg(t, []commands.Argument{
		{ID: "BASE", Title: "Base Branch", Required: true},
		{ID: "HEAD", Title: "Head Branch"},
	}, dialog.ActionDescribe{})

	a.HandleMsg(keyMsg("main"))
	a.HandleMsg(keyMsg("down"))

	require.Equal(t, dialog.ActionDescribe{Base: "main"}, a.HandleMsg(keyMsg("enter")))
}

// TestHandleDialogMsg_ActionDescribe_OutsideVSCodeOpensViewer drives the
// whole happy path outside VS Code: the comparison is read through the
// workspace shell, the agent is handed it unchanged, and the text lands in a
// Viewer dialog with no file written and no opener called.
func TestHandleDialogMsg_ActionDescribe_OutsideVSCodeOpensViewer(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectBranchChanges(ws, "main", "feature")
	ws.EXPECT().AgentGenerateChangeDescription(gomock.Any(), "s1", "main", "feature", describeTestChanges).
		Return("## Summary\nAdds x.", nil)
	stubBusyProbe(ws, true, false, permission.ModeManual, &workspace.ActiveAgent{})
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()

	m := newHandleDialogUI(t, ws)
	m.openInVSCode = func(context.Context, vscodemcp.OpenRequest) error {
		t.Error("the opener must not run outside VS Code")
		return nil
	}

	cmd := m.handleDialogMsg(dialog.ActionDescribe{SessionID: "s1", Base: "main", Head: "feature"})
	require.Equal(t, describingInfo, m.status.msg, "the user must be told the description is being written")

	ready, ok := workMsg(t, cmd).(describeReadyMsg)
	require.True(t, ok, "expected the description to come back for the viewer")
	require.Equal(t, "## Summary\nAdds x.", ready.description)
	require.NoError(t, ready.openErr)

	m.Update(ready)
	require.True(t, m.dialog.ContainsDialog(dialog.ViewerID))
	require.True(t, m.status.msg.IsEmpty(), "the pending notice must end")
}

// TestHandleDialogMsg_ActionDescribe_HeadDefaultsToCheckedOutCommit verifies
// an empty head compares against HEAD.
func TestHandleDialogMsg_ActionDescribe_HeadDefaultsToCheckedOutCommit(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectBranchChanges(ws, "main", "HEAD")
	ws.EXPECT().AgentGenerateChangeDescription(gomock.Any(), "", "main", "HEAD", describeTestChanges).
		Return("## Summary\nAdds x.", nil)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "main"})
	ready, ok := workMsg(t, cmd).(describeReadyMsg)
	require.True(t, ok)
	require.Equal(t, "HEAD", ready.head)
}

// TestHandleDialogMsg_ActionDescribe_InVSCodeWritesFileAndOpensIt verifies
// the description lands under the data directory's viewer directory, that
// exact path is handed to the opener, and no viewer is shown on top.
func TestHandleDialogMsg_ActionDescribe_InVSCodeWritesFileAndOpensIt(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	want := filepath.Join(dataDir, scratch.ViewerDir, describeViewerFile)

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().Config().Return(&config.Config{Options: &config.Options{DataDirectory: dataDir}}).AnyTimes()
	expectBranchChanges(ws, "main", "feature")
	ws.EXPECT().AgentGenerateChangeDescription(gomock.Any(), "s1", "main", "feature", describeTestChanges).
		Return("## Summary\nAdds x.", nil)

	var opened vscodemcp.OpenRequest
	m := newHandleDialogUI(t, ws)
	m.caps.Env = uv.Environ{"TERM_PROGRAM=vscode"}
	m.openInVSCode = func(_ context.Context, req vscodemcp.OpenRequest) error {
		opened = req
		return nil
	}

	cmd := m.handleDialogMsg(dialog.ActionDescribe{SessionID: "s1", Base: "main", Head: "feature"})

	msg := workMsg(t, cmd)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a result toast, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)
	require.False(t, m.dialog.ContainsDialog(dialog.ViewerID), "VS Code shows it, so no viewer")

	require.Equal(t, want, opened.Path)
	data, err := os.ReadFile(want)
	require.NoError(t, err)
	require.Equal(t, "## Summary\nAdds x.\n", string(data))
}

// TestHandleDialogMsg_ActionDescribe_OpenerFailureFallsBackToViewer verifies
// a failed VS Code open is not lost: the text is handed back so Update can
// show it in the TUI along with a warning.
func TestHandleDialogMsg_ActionDescribe_OpenerFailureFallsBackToViewer(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().Config().Return(&config.Config{Options: &config.Options{DataDirectory: t.TempDir()}}).AnyTimes()
	expectBranchChanges(ws, "main", "feature")
	ws.EXPECT().AgentGenerateChangeDescription(gomock.Any(), "", "main", "feature", describeTestChanges).
		Return("## Summary\nAdds x.", nil)
	stubBusyProbe(ws, true, false, permission.ModeManual, &workspace.ActiveAgent{})
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()

	m := newHandleDialogUI(t, ws)
	m.caps.Env = uv.Environ{"TERM_PROGRAM=vscode"}
	m.openInVSCode = func(context.Context, vscodemcp.OpenRequest) error {
		return errors.New("code: not found")
	}

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "main", Head: "feature"})

	ready, ok := workMsg(t, cmd).(describeReadyMsg)
	require.True(t, ok, "expected the description to come back for the fallback")
	require.ErrorContains(t, ready.openErr, "code: not found")
	require.Equal(t, "## Summary\nAdds x.", ready.description)

	_, next := m.Update(ready)
	require.True(t, m.dialog.ContainsDialog(dialog.ViewerID))
	require.True(t, m.status.msg.IsEmpty(), "the pending notice must end")

	var warned bool
	for _, msg := range runCmds(m, next) {
		if info, ok := msg.(util.InfoMsg); ok && info.Type == util.InfoTypeWarn {
			warned = true
		}
	}
	require.True(t, warned, "the user must be told why VS Code was skipped")
}

// TestHandleDialogMsg_ActionDescribe_QuotesUserTypedRefs verifies a branch
// name cannot add a command or an option to the git invocation: the typed
// text reaches the shell as one quoted word, and an unknown ref stops before
// anything is diffed or sent to the model.
func TestHandleDialogMsg_ActionDescribe_QuotesUserTypedRefs(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectGit(ws, "git rev-parse --is-inside-work-tree", "true\n", 0)
	expectGit(ws, "git rev-parse --verify --quiet '$(touch pwned)^{commit}'", "", 1)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "$(touch pwned)", Head: "feature"})
	info, ok := workMsg(t, cmd).(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, `unknown branch or commit "$(touch pwned)"`)
}

// TestHandleDialogMsg_ActionDescribe_RejectsOptionLookingRef verifies a ref
// that git would read as an option never reaches git at all beyond the
// repository check.
func TestHandleDialogMsg_ActionDescribe_RejectsOptionLookingRef(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectGit(ws, "git rev-parse --is-inside-work-tree", "true\n", 0)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "--output=/tmp/pwned", Head: "feature"})
	info, ok := workMsg(t, cmd).(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "invalid revision")
}

// TestHandleDialogMsg_ActionDescribe_OutsideRepositoryWarns verifies a
// workspace that is not a git repository is reported as a warning and goes no
// further.
func TestHandleDialogMsg_ActionDescribe_OutsideRepositoryWarns(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectGit(ws, "git rev-parse --is-inside-work-tree", "fatal: not a git repository\n", 128)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "main"})
	info, ok := workMsg(t, cmd).(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "git repository")
}

// TestHandleDialogMsg_ActionDescribe_NoChangesWarns verifies two sides that
// do not differ are a warning, not an error, and never reach the model.
func TestHandleDialogMsg_ActionDescribe_NoChangesWarns(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectGit(ws, "git rev-parse --is-inside-work-tree", "true\n", 0)
	expectGit(ws, "git rev-parse --verify --quiet 'main^{commit}'", "1111111\n", 0)
	expectGit(ws, "git rev-parse --verify --quiet 'feature^{commit}'", "1111111\n", 0)
	expectGit(ws, "git diff --no-color main...feature --", "", 0)

	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "main", Head: "feature"})
	info, ok := workMsg(t, cmd).(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "no changes between main and feature")
}

// TestHandleDialogMsg_ActionDescribe_GenerationErrorIsReported verifies a
// model failure surfaces as an error and opens nothing.
func TestHandleDialogMsg_ActionDescribe_GenerationErrorIsReported(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectBranchChanges(ws, "main", "feature")
	ws.EXPECT().AgentGenerateChangeDescription(gomock.Any(), "", "main", "feature", describeTestChanges).
		Return("", errors.New("model unavailable"))

	m := newHandleDialogUI(t, ws)
	m.openInVSCode = func(context.Context, vscodemcp.OpenRequest) error {
		t.Error("nothing was generated, so nothing may be opened")
		return nil
	}

	cmd := m.handleDialogMsg(dialog.ActionDescribe{Base: "main", Head: "feature"})
	info, ok := workMsg(t, cmd).(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "failed to generate change description")
	require.Contains(t, info.Msg, "model unavailable")
	require.False(t, m.dialog.ContainsDialog(dialog.ViewerID))
}
