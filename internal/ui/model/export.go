package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
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
		cmds = append(cmds, func() tea.Msg {
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
	cmds = append(cmds, func() tea.Msg {
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
