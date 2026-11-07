package model

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
)

// openInVSCodeTimeout bounds the whole open so a stuck `code` CLI cannot
// leave the pending notice up forever.
const openInVSCodeTimeout = 30 * time.Second

// inVSCode reports whether the TUI runs in VS Code's integrated terminal,
// read from the environment the terminal reported rather than os.Getenv.
func (m *UI) inVSCode() bool {
	return vscodemcp.InIntegratedTerminal(m.caps.Env.Getenv)
}

// handleOpenImage handles dialog.ActionOpenImage.
func (m *UI) handleOpenImage(msg dialog.ActionOpenImage) tea.Cmd {
	if msg.ImageID == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		if m.session == nil {
			return util.ReportWarn("Open a session before opening an image.")
		}
		return tea.Batch(m.showPending(imagesListingInfo), m.loadSessionImagesCmd(m.session.ID, dialog.ImagesOpen))
	}

	m.dialog.CloseDialog(dialog.ImagesID)
	imageID := msg.ImageID
	open := m.openInVSCode
	if open == nil {
		open = (&vscodemcp.Client{}).Open
	}
	return tea.Batch(m.showPending(openingImageInfo), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openInVSCodeTimeout)
		defer cancel()

		img, err := m.com.Workspace.GetGeneratedImage(ctx, imageID)
		if err != nil {
			return util.ReportError(err)()
		}

		// NOTE: The image only lives in SQLite and `code` opens files, so it
		// goes to a fixed name under the temp dir. Reopening an image reuses
		// the file, and VS Code reuses its tab.
		dir := filepath.Join(os.TempDir(), "angela-images")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return util.ReportError(err)()
		}
		path := filepath.Join(dir, imageID+chat.ExtensionForMIME(img.MIMEType))
		if err := os.WriteFile(path, img.Data, 0o600); err != nil {
			return util.ReportError(err)()
		}

		if err := open(ctx, vscodemcp.OpenRequest{Path: path}); err != nil {
			return util.ReportError(err)()
		}
		return util.ReportInfo("Opened image in VS Code")()
	})
}
