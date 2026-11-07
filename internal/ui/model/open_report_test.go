package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestHandleDialogMsg_ActionOpenReport_EmptyIDOpensPicker verifies that
// picking "Open Report" from the palette collects the session's reports
// and opens the picker for opening rather than exporting.
func TestHandleDialogMsg_ActionOpenReport_EmptyIDOpensPicker(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	expectAgentToolSessionIDs(ws)
	msgs, _ := agentDispatchMessages(ws, "parser lives in parse.go")
	ws.EXPECT().ListMessages(gomock.Any(), "s1").Return(msgs, nil)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionOpenReport{})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the picker opens")
	require.NotNil(t, cmd)

	loaded, ok := workMsg(t, cmd).(sessionReportsLoadedMsg)
	require.True(t, ok)
	require.Equal(t, dialog.ReportsOpen, loaded.purpose)

	m.Update(loaded)
	require.True(t, m.dialog.ContainsDialog(dialog.ReportsID))
}

func TestHandleDialogMsg_ActionOpenReport_NoSession(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = nil

	cmd := m.handleDialogMsg(dialog.ActionOpenReport{})
	require.NotNil(t, cmd)

	msg := workMsg(t, cmd)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionOpenReport_OutsideVSCodeOpensViewer verifies
// that without VS Code the report is shown in the TUI and nothing is
// written to disk or handed to the opener.
func TestHandleDialogMsg_ActionOpenReport_OutsideVSCodeOpensViewer(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.openInVSCode = func(context.Context, vscodemcp.OpenRequest) error {
		t.Error("the opener must not run outside VS Code")
		return nil
	}

	report := agenttools.Report{ID: "rpt_a1b2c3d4", AgentType: "explore", Task: "find the parser", Content: "parse.go"}
	cmd := m.handleDialogMsg(dialog.ActionOpenReport{Report: report})
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.ViewerID))
	require.True(t, m.status.msg.IsEmpty(), "no pending notice for an instant open")
}

// TestHandleDialogMsg_ActionOpenReport_ClosesPickerBeforeViewer verifies
// the picker does not stay underneath the viewer it opened.
func TestHandleDialogMsg_ActionOpenReport_ClosesPickerBeforeViewer(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	report := agenttools.Report{ID: "rpt_a1b2c3d4", AgentType: "explore", Task: "find the parser", Content: "parse.go"}
	m.dialog.OpenDialog(dialog.NewReports(m.com, []agenttools.Report{report}, "/work", dialog.ReportsOpen))

	m.handleOpenReport(dialog.ActionOpenReport{Report: report})
	require.False(t, m.dialog.ContainsDialog(dialog.ReportsID))
	require.True(t, m.dialog.ContainsDialog(dialog.ViewerID))
}

// TestHandleDialogMsg_ActionOpenReport_WritesTempFileAndOpensIt verifies
// the report's Markdown lands on disk under the temp dir and that exact
// path is handed to the opener.
func TestHandleDialogMsg_ActionOpenReport_WritesTempFileAndOpensIt(t *testing.T) {
	t.Parallel()

	report := agenttools.Report{ID: "rpt_0a1b2c3d", AgentType: "explore", Task: "find the parser", Content: "parse.go"}
	want := filepath.Join(os.TempDir(), "angela-reports", report.ID+".md")
	t.Cleanup(func() { _ = os.Remove(want) })

	var opened vscodemcp.OpenRequest
	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.caps.Env = uv.Environ{"TERM_PROGRAM=vscode"}
	m.openInVSCode = func(_ context.Context, req vscodemcp.OpenRequest) error {
		opened = req
		return nil
	}

	cmd := m.handleDialogMsg(dialog.ActionOpenReport{Report: report})
	require.NotNil(t, cmd)
	require.Equal(t, openingReportInfo, m.status.msg, "the user must be told the open is running")

	msg := workMsg(t, cmd)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a result toast, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)
	require.False(t, m.dialog.ContainsDialog(dialog.ViewerID), "VS Code shows it, so no viewer")

	require.Equal(t, want, opened.Path)
	data, err := os.ReadFile(want)
	require.NoError(t, err)
	require.Equal(t, chat.ReportToMarkdown(report), string(data))
}

// TestHandleDialogMsg_ActionOpenReport_OpenerFailureFallsBackToViewer
// verifies a failed VS Code open is not lost: the report is handed back
// so Update can show it in the TUI along with a warning.
func TestHandleDialogMsg_ActionOpenReport_OpenerFailureFallsBackToViewer(t *testing.T) {
	t.Parallel()

	report := agenttools.Report{ID: "rpt_9f8e7d6c", AgentType: "explore", Task: "find the parser", Content: "parse.go"}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(os.TempDir(), "angela-reports", report.ID+".md")) })

	ws := NewMockWorkspace(gomock.NewController(t))
	stubBusyProbe(ws, true, false, permission.ModeManual, &workspace.ActiveAgent{})
	ws.EXPECT().LSPGetStates().Return(nil).AnyTimes()
	m := newHandleDialogUI(t, ws)
	m.caps.Env = uv.Environ{"TERM_PROGRAM=vscode"}
	m.openInVSCode = func(context.Context, vscodemcp.OpenRequest) error {
		return errors.New("code: not found")
	}

	cmd := m.handleDialogMsg(dialog.ActionOpenReport{Report: report})
	require.NotNil(t, cmd)

	fallback, ok := workMsg(t, cmd).(reportOpenFallbackMsg)
	require.True(t, ok, "expected the report to come back for the fallback")
	require.Equal(t, report, fallback.report)

	_, next := m.Update(fallback)
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
