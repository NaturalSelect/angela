package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestInVSCode(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	require.False(t, m.inVSCode(), "an unreported environment is not VS Code")

	m.caps.Env = uv.Environ{"TERM_PROGRAM=vscode"}
	require.True(t, m.inVSCode())

	m.caps.Env = uv.Environ{"TERM_PROGRAM=iTerm.app"}
	require.False(t, m.inVSCode())
}

// TestHandleDialogMsg_ActionOpenImage_EmptyIDOpensPicker verifies that
// picking "Open Image" from the palette lists the session's images and
// opens the picker for opening rather than exporting.
func TestHandleDialogMsg_ActionOpenImage_EmptyIDOpensPicker(t *testing.T) {
	t.Parallel()

	ws := NewMockWorkspace(gomock.NewController(t))
	imgs := []images.Image{{ID: "img_1a2b3c4d5e6f", Prompt: "a cat", CreatedAt: 2000}}
	ws.EXPECT().ListSessionImages(gomock.Any(), "s1").Return(imgs, nil)

	m := newHandleDialogUI(t, ws)
	m.session = &session.Session{ID: "s1"}

	cmd := m.handleDialogMsg(dialog.ActionOpenImage{})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the picker opens")
	require.NotNil(t, cmd)

	loaded, ok := workMsg(t, cmd).(sessionImagesLoadedMsg)
	require.True(t, ok)
	require.Equal(t, dialog.ImagesOpen, loaded.purpose)

	m.Update(loaded)
	require.True(t, m.dialog.ContainsDialog(dialog.ImagesID))
}

func TestHandleDialogMsg_ActionOpenImage_NoSession(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = nil

	cmd := m.handleDialogMsg(dialog.ActionOpenImage{})
	require.NotNil(t, cmd)

	msg := workMsg(t, cmd)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a warning, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
}

// TestHandleDialogMsg_ActionOpenImage_WritesTempFileAndOpensIt verifies
// the image bytes land on disk under the temp dir and that exact path is
// handed to the opener.
func TestHandleDialogMsg_ActionOpenImage_WritesTempFileAndOpensIt(t *testing.T) {
	t.Parallel()

	const id = "img_0a1b2c3d4e5f"
	img := images.Image{ID: id, MIMEType: "image/png", Data: []byte("fake-png-bytes")}
	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().GetGeneratedImage(gomock.Any(), id).Return(img, nil)

	want := filepath.Join(os.TempDir(), "angela-images", id+".png")
	t.Cleanup(func() { _ = os.Remove(want) })

	var opened vscodemcp.OpenRequest
	m := newHandleDialogUI(t, ws)
	m.openInVSCode = func(_ context.Context, req vscodemcp.OpenRequest) error {
		opened = req
		return nil
	}

	cmd := m.handleDialogMsg(dialog.ActionOpenImage{ImageID: id})
	require.NotNil(t, cmd)

	msg := workMsg(t, cmd)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a result toast, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeInfo, info.Type)

	require.Equal(t, want, opened.Path)
	data, err := os.ReadFile(want)
	require.NoError(t, err)
	require.Equal(t, img.Data, data)
}

func TestHandleDialogMsg_ActionOpenImage_OpenerFailureIsReported(t *testing.T) {
	t.Parallel()

	const id = "img_9f8e7d6c5b4a"
	img := images.Image{ID: id, MIMEType: "image/png", Data: []byte("fake-png-bytes")}
	ws := NewMockWorkspace(gomock.NewController(t))
	ws.EXPECT().GetGeneratedImage(gomock.Any(), id).Return(img, nil)

	t.Cleanup(func() { _ = os.Remove(filepath.Join(os.TempDir(), "angela-images", id+".png")) })

	m := newHandleDialogUI(t, ws)
	m.openInVSCode = func(context.Context, vscodemcp.OpenRequest) error {
		return errors.New("code: not found")
	}

	cmd := m.handleDialogMsg(dialog.ActionOpenImage{ImageID: id})
	require.NotNil(t, cmd)

	msg := workMsg(t, cmd)
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected an error toast, got %T: %v", msg, msg)
	require.Equal(t, util.InfoTypeError, info.Type)
}
