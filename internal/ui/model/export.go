package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/charmbracelet/x/ansi"
)

// handleExportImage handles dialog.ActionExportImage.
func (m *UI) handleExportImage(msg dialog.ActionExportImage) tea.Cmd {
	var cmds []tea.Cmd
	if strings.TrimSpace(msg.ImageID) == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		if m.session == nil {
			cmds = append(cmds, util.ReportWarn("Open a session before exporting an image."))
			return tea.Batch(cmds...)
		}
		sessionID := m.session.ID
		cmds = append(cmds, m.showPending(imagesListingInfo), func() tea.Msg {
			ctx := context.Background()
			imgs, err := m.com.Workspace.ListSessionImages(ctx, sessionID)
			if err != nil {
				return util.ReportError(err)()
			}
			if len(imgs) == 0 {
				return util.ReportWarn("No generated images in this session")()
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return util.ReportError(err)()
			}

			return exportImagesLoadedMsg{sessionID: sessionID, images: imgs, workingDir: workingDir}
		})
		return tea.Batch(cmds...)
	}
	m.dialog.CloseDialog(dialog.ArgumentsID)
	imageID := msg.ImageID
	output := msg.Output
	cmds = append(cmds, m.showPending(exportingImageInfo), func() tea.Msg {
		ctx := context.Background()
		img, err := m.com.Workspace.GetGeneratedImage(ctx, imageID)
		if err != nil {
			return util.ReportError(err)()
		}

		workingDir, err := os.Getwd()
		if err != nil {
			return util.ReportError(err)()
		}

		target := chat.ResolveExportTarget(output, imageID+chat.ExtensionForMIME(img.MIMEType), workingDir)

		outPath, err := chat.UniqueExportPath(target)
		if err != nil {
			return util.ReportError(err)()
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return util.ReportError(err)()
		}
		if err := os.WriteFile(outPath, img.Data, 0o644); err != nil {
			return util.ReportError(err)()
		}

		return util.ReportInfo("Exported image to " + outPath)()
	})
	return tea.Batch(cmds...)
}

// handleExportReport handles dialog.ActionExportReport.
func (m *UI) handleExportReport(msg dialog.ActionExportReport) tea.Cmd {
	var cmds []tea.Cmd
	if msg.Report.ID == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		if m.session == nil {
			cmds = append(cmds, util.ReportWarn("Open a session before exporting a report."))
			return tea.Batch(cmds...)
		}
		sessionID := m.session.ID
		cmds = append(cmds, m.showPending(reportsListingInfo), func() tea.Msg {
			msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
			if err != nil {
				return util.ReportError(err)()
			}
			reports := agenttools.ReportsFromMessages(m.com.Workspace, msgs)
			if len(reports) == 0 {
				return util.ReportWarn("No agent reports in this session")()
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return util.ReportError(err)()
			}

			return exportReportsLoadedMsg{sessionID: sessionID, reports: reports, workingDir: workingDir}
		})
		return tea.Batch(cmds...)
	}
	m.dialog.CloseDialog(dialog.ArgumentsID)
	report := msg.Report
	output := msg.Output
	cmds = append(cmds, m.showPending(exportingReportInfo), func() tea.Msg {
		workingDir, err := os.Getwd()
		if err != nil {
			return util.ReportError(err)()
		}

		target := chat.ResolveExportTarget(output, report.ID+".md", workingDir)

		outPath, err := chat.UniqueExportPath(target)
		if err != nil {
			return util.ReportError(err)()
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return util.ReportError(err)()
		}
		if err := os.WriteFile(outPath, []byte(chat.ReportToMarkdown(report)), 0o644); err != nil {
			return util.ReportError(err)()
		}

		return util.ReportInfo("Exported report to " + outPath)()
	})
	return tea.Batch(cmds...)
}

// handleExportSession handles dialog.ActionExportSession.
func (m *UI) handleExportSession(msg dialog.ActionExportSession) tea.Cmd {
	var cmds []tea.Cmd
	m.dialog.CloseDialog(dialog.CommandsID)
	sessionID := msg.SessionID
	cmds = append(cmds, m.showPending(exportingSessionInfo), func() tea.Msg {
		ctx := context.Background()
		sess, err := m.com.Workspace.GetSession(ctx, sessionID)
		if err != nil {
			return util.ReportError(err)()
		}
		sessionMessages, err := m.com.Workspace.ListMessages(ctx, sessionID)
		if err != nil {
			return util.ReportError(err)()
		}

		workingDir, err := os.Getwd()
		if err != nil {
			return util.ReportError(err)()
		}

		outPath, err := chat.UniqueExportPath(filepath.Join(workingDir, chat.SessionExportSlug(sess.Title, sess.ID)+".md"))
		if err != nil {
			return util.ReportError(err)()
		}

		doc, err := chat.SessionToMarkdown(sess, sessionMessages, chat.AssetsDirName(outPath))
		if err != nil {
			return util.ReportError(err)()
		}
		if err := os.WriteFile(outPath, []byte(doc), 0o644); err != nil {
			return util.ReportError(err)()
		}

		return util.ReportInfo("Exported session to " + outPath)()
	})
	return tea.Batch(cmds...)
}

// handleSelectExportImage handles dialog.ActionSelectExportImage.
func (m *UI) handleSelectExportImage(msg dialog.ActionSelectExportImage) tea.Cmd {
	m.dialog.CloseDialog(dialog.ImagesID)
	defaultPath := filepath.Join(msg.WorkingDir, msg.Image.ID+chat.ExtensionForMIME(msg.Image.MIMEType))
	description := "Writes the generated image's full-size original to a file on disk."
	if prompt := strings.Join(strings.Fields(msg.Image.Prompt), " "); prompt != "" {
		description = fmt.Sprintf("Output path for %q.", ansi.Truncate(prompt, 60, "…"))
	}
	argsDialog := dialog.NewArguments(
		m.com,
		"Export Image",
		description,
		[]commands.Argument{{ID: "OUTPUT", Title: "Output Path", Required: true}},
		dialog.ActionExportImage{ImageID: msg.Image.ID},
	)
	argsDialog.SetValue("OUTPUT", defaultPath)
	m.dialog.OpenDialog(argsDialog)
	return nil
}

// handleSelectExportReport handles dialog.ActionSelectExportReport.
func (m *UI) handleSelectExportReport(msg dialog.ActionSelectExportReport) tea.Cmd {
	m.dialog.CloseDialog(dialog.ReportsID)
	defaultPath := filepath.Join(msg.WorkingDir, msg.Report.ID+".md")
	description := "Writes the agent report to a Markdown file on disk."
	if task := strings.Join(strings.Fields(msg.Report.Task), " "); task != "" {
		description = fmt.Sprintf("Output path for %q.", ansi.Truncate(task, 60, "…"))
	}
	argsDialog := dialog.NewArguments(
		m.com,
		"Export Report",
		description,
		[]commands.Argument{{ID: "OUTPUT", Title: "Output Path", Required: true}},
		dialog.ActionExportReport{Report: msg.Report},
	)
	argsDialog.SetValue("OUTPUT", defaultPath)
	m.dialog.OpenDialog(argsDialog)
	return nil
}
