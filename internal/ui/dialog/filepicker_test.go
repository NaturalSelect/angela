package dialog

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	fimage "github.com/NaturalSelect/angela/internal/ui/image"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// dirWorkspace is the least workspace the file picker needs: the
// working directory it opens on.
type dirWorkspace struct {
	workspace.Workspace

	dir string
}

func (w *dirWorkspace) WorkingDir() string { return w.dir }

// newTestFilePicker builds the dialog rooted at dir and drains its
// construction command, which performs one real (local, no network)
// directory listing, so the picker starts out populated exactly like
// it would in the app.
func newTestFilePicker(t *testing.T, dir string) *FilePicker {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s, Workspace: &dirWorkspace{dir: dir}}
	f, cmd := NewFilePicker(com)
	require.NotNil(t, cmd)
	f.HandleMsg(cmd())
	return f
}

// writeTempPNG writes a minimal valid PNG to dir/name and returns its
// path.
func writeTempPNG(t *testing.T, dir, name string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	return path
}

func TestFilePicker_ID(t *testing.T) {
	t.Parallel()

	f := newTestFilePicker(t, t.TempDir())
	require.Equal(t, FilePickerID, f.ID())
}

func TestFilePicker_CellSize(t *testing.T) {
	t.Parallel()

	f := &FilePicker{cellSizeW: 8, cellSizeH: 16}
	require.Equal(t, 8, f.CellSize().Width)
	require.Equal(t, 16, f.CellSize().Height)
}

// TestFilePicker_WorkingDir verifies the workspace's directory wins
// when it reports one, and the process working directory is used as
// the fallback otherwise.
func TestFilePicker_WorkingDir(t *testing.T) {
	t.Parallel()

	t.Run("workspace reports a directory", func(t *testing.T) {
		t.Parallel()
		s := styles.CharmtonePantera()
		f := &FilePicker{com: &common.Common{Styles: &s, Workspace: &dirWorkspace{dir: "/somewhere"}}}
		require.Equal(t, "/somewhere", f.WorkingDir())
	})

	t.Run("empty workspace directory falls back to the process cwd", func(t *testing.T) {
		t.Parallel()
		s := styles.CharmtonePantera()
		f := &FilePicker{com: &common.Common{Styles: &s, Workspace: &dirWorkspace{dir: ""}}}
		cwd, err := os.Getwd()
		require.NoError(t, err)
		require.Equal(t, cwd, f.WorkingDir())
	})
}

func TestFilePicker_ShortHelpAndFullHelp(t *testing.T) {
	t.Parallel()

	f := newTestFilePicker(t, t.TempDir())
	require.Len(t, f.ShortHelp(), 3)

	var flat []int
	for _, row := range f.FullHelp() {
		flat = append(flat, len(row))
	}
	require.Equal(t, []int{4, 2}, flat)
}

// TestFilePicker_SetImageCapabilities verifies a nil capabilities is a
// no-op, and a populated one derives the encoding, cell size, and tmux
// flag.
func TestFilePicker_SetImageCapabilities(t *testing.T) {
	t.Parallel()

	t.Run("nil capabilities changes nothing", func(t *testing.T) {
		t.Parallel()
		f := &FilePicker{}
		f.SetImageCapabilities(nil)
		require.Zero(t, f.cellSizeW)
		require.False(t, f.isTmux)
	})

	t.Run("kitty graphics inside tmux", func(t *testing.T) {
		t.Parallel()
		f := &FilePicker{}
		f.SetImageCapabilities(&common.Capabilities{
			KittyGraphics: true,
			Columns:       80,
			Rows:          24,
			PixelX:        800,
			PixelY:        480,
			Env:           uv.Environ{"TMUX=/tmp/tmux-1000/default,1,0"},
		})
		require.Equal(t, 10, f.cellSizeW)
		require.Equal(t, 20, f.cellSizeH)
		require.True(t, f.isTmux)
	})

	t.Run("no kitty graphics outside tmux", func(t *testing.T) {
		t.Parallel()
		f := &FilePicker{}
		f.SetImageCapabilities(&common.Capabilities{Columns: 80, Rows: 24, PixelX: 800, PixelY: 480})
		require.False(t, f.isTmux)
	})
}

// TestLoadImage covers a valid PNG, a file that fails to decode, and a
// missing path.
func TestLoadImage(t *testing.T) {
	t.Parallel()

	t.Run("a valid PNG decodes", func(t *testing.T) {
		t.Parallel()
		path := writeTempPNG(t, t.TempDir(), "pic.png")
		img, err := loadImage(path)
		require.NoError(t, err)
		require.NotNil(t, img)
	})

	t.Run("a non-image file fails to decode", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "notes.txt")
		require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))
		_, err := loadImage(path)
		require.Error(t, err)
	})

	t.Run("a missing path fails to open", func(t *testing.T) {
		t.Parallel()
		_, err := loadImage(filepath.Join(t.TempDir(), "missing.png"))
		require.Error(t, err)
	})
}

func TestFilePicker_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	f := newTestFilePicker(t, t.TempDir())
	require.Equal(t, ActionClose{}, f.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestFilePicker_HandleMsg_Navigation verifies a plain navigation key
// reaches the embedded filepicker model and comes back as a command
// to run, without selecting anything.
func TestFilePicker_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "adir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bfile.txt"), []byte("x"), 0o644))

	f := newTestFilePicker(t, dir)
	action := f.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	_, ok := action.(ActionCmd)
	require.True(t, ok)
}

// TestFilePicker_HandleMsg_SelectFile verifies choosing an allowed
// image file reports it back as the selection. Only files matching
// common.AllowedImageTypes can be selected; anything else just
// navigates.
func TestFilePicker_HandleMsg_SelectFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := writeTempPNG(t, dir, "pic.png")

	f := newTestFilePicker(t, dir)
	require.Equal(t, filePath, f.fp.HighlightedPath(), "the only entry must be highlighted by default")

	action := f.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionFilePickerSelected)
	require.True(t, ok)
	require.Equal(t, filePath, resp.Path)
}

// TestFilePicker_HandleMsg_HighlightingAnImageLoadsIt verifies that
// highlighting an allowed image type reads it from disk (a real, local
// file read — no network) and marks it as previewing. The cache is
// pre-seeded via the same Transmit path the dialog itself uses, so the
// dialog finds the image already transmitted instead of queuing an
// async command to load it.
func TestFilePicker_HandleMsg_HighlightingAnImageLoadsIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeTempPNG(t, dir, "pic.png")

	img, err := loadImage(path)
	require.NoError(t, err)
	transmit := fimage.EncodingBlocks.Transmit(path, img, fimage.CellSize{}, 0, 0, false)
	require.NotNil(t, transmit)
	transmit()
	require.True(t, fimage.HasTransmitted(path, 0, 0))

	f := newTestFilePicker(t, dir)
	require.True(t, f.previewingImage)
}

// TestFilePicker_Draw covers both the narrow layout (no room for a
// preview) and the tall layout (preview and list share the body).
func TestFilePicker_Draw(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "afile.txt"), []byte("x"), 0o644))

	t.Run("short dialog skips the image preview", func(t *testing.T) {
		t.Parallel()
		f := newTestFilePicker(t, dir)
		scr := uv.NewScreenBuffer(80, 12)
		f.Draw(scr, uv.Rect(0, 0, 80, 12))
		content := ansi.Strip(scr.Render())
		require.Contains(t, content, "Add Image")
		require.Contains(t, content, "afile.txt")
	})

	t.Run("tall dialog splits room with a preview", func(t *testing.T) {
		t.Parallel()
		f := newTestFilePicker(t, dir)
		scr := uv.NewScreenBuffer(80, 30)
		f.Draw(scr, uv.Rect(0, 0, 80, 30))
		content := ansi.Strip(scr.Render())
		require.Contains(t, content, "Add Image")
		require.Contains(t, content, "afile.txt")
	})
}

// TestFilePicker_ImagePreview_NotPreviewing verifies the placeholder
// block fills the requested dimensions and is cached by size.
func TestFilePicker_ImagePreview_NotPreviewing(t *testing.T) {
	t.Parallel()

	f := &FilePicker{}
	out := f.imagePreview(3, 2)
	require.Equal(t, "███\n███", out)

	// A second call at the same size must hit the cache and return the
	// identical string.
	require.Equal(t, out, f.imagePreview(3, 2))
}
