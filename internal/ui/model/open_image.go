package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/scratch"
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

// viewerDir returns the directory for files written so VS Code can open
// them.
//
// NOTE: It sits in the data directory, not a fixed name under the system
// temp dir. On a shared host another user could create that name first and
// swap the file before `code` opens it. The data directory is also what
// `angela vacuum` cleans.
func (m *UI) viewerDir() (string, error) {
	cfg := m.com.Config()
	if cfg == nil || cfg.Options == nil || cfg.Options.DataDirectory == "" {
		return "", errors.New("the data directory is not set")
	}
	return filepath.Join(cfg.Options.DataDirectory, scratch.ViewerDir), nil
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
	dir, err := m.viewerDir()
	if err != nil {
		return util.ReportError(err)
	}
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
		// goes to a fixed name in the viewer directory. Reopening an image
		// reuses the file, and VS Code reuses its tab.
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
