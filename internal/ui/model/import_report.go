package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/home"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// readReportFile reads the report file at path from this process's own
// filesystem. A relative path is taken from the working directory and a
// leading "~" from the home directory.
//
// NOTE: The workspace's working directory is never used here, because the
// user typed a path relative to the directory they launched Angela from.
func readReportFile(path string) (agenttools.Report, error) {
	path = home.Long(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		workingDir, err := os.Getwd()
		if err != nil {
			return agenttools.Report{}, err
		}
		path = filepath.Join(workingDir, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return agenttools.Report{}, err
	}
	if info.IsDir() {
		return agenttools.Report{}, fmt.Errorf("%s is a directory, not a report file", path)
	}
	// NOTE: A pipe or device reports a size of zero, so only a regular file
	// can be trusted to end.
	if !info.Mode().IsRegular() {
		return agenttools.Report{}, fmt.Errorf("%s is not a regular file", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return agenttools.Report{}, err
	}
	defer f.Close()

	// NOTE: The cap is enforced on the bytes read, not on the size from
	// Stat, because a file can grow after it was statted.
	data, err := io.ReadAll(io.LimitReader(f, common.MaxAttachmentSize+1))
	if err != nil {
		return agenttools.Report{}, err
	}
	if int64(len(data)) > common.MaxAttachmentSize {
		return agenttools.Report{}, fmt.Errorf("report file is too large (the limit is %d bytes)", common.MaxAttachmentSize)
	}
	return chat.ReportFromMarkdown(data, path), nil
}

// importReportSummary tells the user what an import did and which id the
// agent can load the report by.
func importReportSummary(result agenttools.ImportResult) string {
	id := result.Report.ID
	switch {
	case result.AlreadyPresent:
		return fmt.Sprintf("Report %s is already in this session", id)
	case result.RequestedID != "" && result.RequestedID != id:
		return fmt.Sprintf("Imported as %s (%s is taken by a different report)", id, result.RequestedID)
	default:
		return "Imported report " + id
	}
}

// handleImportReport handles dialog.ActionImportReport.
func (m *UI) handleImportReport(msg dialog.ActionImportReport) tea.Cmd {
	var cmds []tea.Cmd
	if msg.Path == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		if m.session == nil {
			cmds = append(cmds, util.ReportWarn("Open a session before importing a report."))
			return tea.Batch(cmds...)
		}
		m.dialog.OpenDialog(dialog.NewArguments(
			m.com,
			"Import Report",
			"Reads an agent report from a file into this session so the agent can load it with LoadReport.",
			[]commands.Argument{{
				ID:          "PATH",
				Title:       "Report File",
				Description: "Path to an exported report file (absolute or relative)",
				Required:    true,
			}},
			dialog.ActionImportReport{},
		))
		return tea.Batch(cmds...)
	}
	m.dialog.CloseDialog(dialog.ArgumentsID)
	if m.session == nil {
		cmds = append(cmds, util.ReportWarn("Open a session before importing a report."))
		return tea.Batch(cmds...)
	}
	sessionID := m.session.ID
	// Session-scoped, like ActionUndo: the busy cache answers for the
	// whole process.
	if m.com.Workspace.AgentIsSessionBusy(sessionID) {
		cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before importing a report..."))
		return tea.Batch(cmds...)
	}
	path := msg.Path
	cmds = append(cmds, func() tea.Msg {
		report, err := readReportFile(path)
		if err != nil {
			return util.ReportError(err)()
		}
		result, err := m.com.Workspace.ImportReport(context.Background(), sessionID, report)
		if err != nil {
			if errors.Is(err, agenttools.ErrEmptyReport) {
				return util.ReportWarn("Report file is empty")()
			}
			if errors.Is(err, agenttools.ErrReportTooLarge) {
				return util.ReportWarn(err.Error())()
			}
			if errors.Is(err, agent.ErrSessionBusy) {
				return util.ReportWarn("Agent is busy, please wait before importing a report...")()
			}
			return util.ReportError(err)()
		}
		return util.ReportInfo(importReportSummary(result))()
	})
	return tea.Batch(cmds...)
}
