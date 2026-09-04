package common

import (
	"image"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

// stubWorkspace is the least workspace.Workspace needed to exercise the
// Common/largeModelProviderID helpers: just a way to inject a
// *config.Config. Every other method panics via the nil embedded
// interface if called, which is intentional — these tests must not
// exercise anything beyond Config().
type stubWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *stubWorkspace) Config() *config.Config { return w.cfg }

func TestCommon_Config(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	c := &Common{Workspace: &stubWorkspace{cfg: cfg}}
	require.Same(t, cfg, c.Config())
}

func TestDefaultCommon(t *testing.T) {
	t.Parallel()

	ws := &stubWorkspace{cfg: &config.Config{}}
	c := DefaultCommon(ws)
	require.NotNil(t, c)
	require.Same(t, workspace.Workspace(ws), c.Workspace)
	require.NotNil(t, c.Styles)
}

func TestLargeModelProviderID(t *testing.T) {
	t.Parallel()

	t.Run("nil workspace returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, largeModelProviderID(nil))
	})

	t.Run("nil config returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, largeModelProviderID(&stubWorkspace{}))
	})

	t.Run("returns the main slot provider", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{
			Slots: map[config.SlotName]config.SelectedModel{
				config.SlotMain: {Provider: "acme", Model: "gpt-x"},
			},
		}
		require.Equal(t, "acme", largeModelProviderID(&stubWorkspace{cfg: cfg}))
	})
}

func TestCenterRect(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 100, 50)
	require.Equal(t, image.Rect(40, 20, 60, 30), CenterRect(area, 20, 10))
}

func TestBottomLeftRect(t *testing.T) {
	t.Parallel()

	area := image.Rect(0, 0, 100, 50)
	require.Equal(t, image.Rect(0, 40, 20, 50), BottomLeftRect(area, 20, 10))
}

func TestIsFileTooBig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	tooBig, err := IsFileTooBig(path, 3)
	require.NoError(t, err)
	require.True(t, tooBig)

	tooBig, err = IsFileTooBig(path, 100)
	require.NoError(t, err)
	require.False(t, tooBig)

	_, err = IsFileTooBig(filepath.Join(dir, "missing.txt"), 100)
	require.Error(t, err)
}

// TestCopyToClipboard checks the tea.Cmd wiring without ever invoking a
// sub-command: tea.Sequence's returned Cmd only bundles its sub-commands
// into a sequence message for the Bubble Tea runtime to execute later, so
// calling it here never touches the real clipboard.
func TestCopyToClipboard(t *testing.T) {
	t.Parallel()

	cmd := CopyToClipboard("hello", "Copied!")
	require.NotNil(t, cmd)

	msg := cmd()
	require.NotNil(t, msg)
	v := reflect.ValueOf(msg)
	require.Equal(t, reflect.Slice, v.Kind())
	require.Equal(t, 3, v.Len(), "clipboard set + native write + report info, no callback")
}

func TestCopyToClipboardWithCallback(t *testing.T) {
	t.Parallel()

	t.Run("a nil callback is dropped from the sequence", func(t *testing.T) {
		t.Parallel()
		cmd := CopyToClipboardWithCallback("hello", "Copied!", nil)
		require.NotNil(t, cmd)
		v := reflect.ValueOf(cmd())
		require.Equal(t, 3, v.Len())
	})

	t.Run("a non-nil callback is included in the sequence", func(t *testing.T) {
		t.Parallel()
		called := false
		callback := func() tea.Msg { called = true; return nil }
		cmd := CopyToClipboardWithCallback("hello", "Copied!", callback)
		require.NotNil(t, cmd)
		v := reflect.ValueOf(cmd())
		require.Equal(t, 4, v.Len())
		require.False(t, called, "building the sequence must not execute the callback")
	})
}
