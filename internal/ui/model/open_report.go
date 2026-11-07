package model

import (
	"context"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
)

// reportOpenFallbackMsg carries a report VS Code could not show, so Update
// can open it in a Viewer dialog instead.
type reportOpenFallbackMsg struct {
	report agenttools.Report
	err    error
}

// handleOpenReport handles dialog.ActionOpenReport.
func (m *UI) handleOpenReport(msg dialog.ActionOpenReport) tea.Cmd {
	if msg.Report.ID == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		if m.session == nil {
			return util.ReportWarn("Open a session before opening a report.")
		}
		return tea.Batch(m.showPending(reportsListingInfo), m.loadSessionReportsCmd(m.session.ID, dialog.ReportsOpen))
	}

	m.dialog.CloseDialog(dialog.ReportsID)
	if !m.inVSCode() {
		m.openReportViewer(msg.Report)
		return nil
	}

	open := m.openInVSCode
	if open == nil {
		open = (&vscodemcp.Client{}).Open
	}
	return tea.Batch(m.showPending(openingReportInfo), m.openReportInVSCodeCmd(msg.Report, open))
}

// openReportInVSCodeCmd writes report to a temp file and opens it in VS Code,
// handing the report back as a reportOpenFallbackMsg when any step fails.
func (m *UI) openReportInVSCodeCmd(report agenttools.Report, open func(context.Context, vscodemcp.OpenRequest) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openInVSCodeTimeout)
		defer cancel()

		// NOTE: The report only lives in the session's messages and `code`
		// opens files, so it goes to a fixed name under the temp dir.
		// Reopening a report reuses the file, and VS Code reuses its tab.
		dir := filepath.Join(os.TempDir(), "angela-reports")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return reportOpenFallbackMsg{report: report, err: err}
		}
		path := filepath.Join(dir, report.ID+".md")
		if err := os.WriteFile(path, []byte(chat.ReportToMarkdown(report)), 0o600); err != nil {
			return reportOpenFallbackMsg{report: report, err: err}
		}

		if err := open(ctx, vscodemcp.OpenRequest{Path: path}); err != nil {
			return reportOpenFallbackMsg{report: report, err: err}
		}
		return util.ReportInfo("Opened report in VS Code")()
	}
}

// openReportViewer shows report in a Viewer dialog.
func (m *UI) openReportViewer(report agenttools.Report) {
	// NOTE: Closing first keeps a second open from stacking a second viewer.
	m.dialog.CloseDialog(dialog.ViewerID)
	m.dialog.OpenDialog(dialog.NewViewer(m.com, dialog.ViewerSpec{
		Title:     report.ID,
		TitleInfo: report.AgentType,
		Content:   chat.ReportToMarkdown(report),
		Markdown:  true,
	}))
}
