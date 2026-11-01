package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// requireInfoMsg fails unless msg is an info message of the given type.
// A tea.Cmd here is the bug: Bubble Tea never runs a command that comes
// back as a message, so the status bar would stay empty.
func requireInfoMsg(t *testing.T, msg tea.Msg, typ util.InfoType) util.InfoMsg {
	t.Helper()
	info, ok := msg.(util.InfoMsg)
	require.Truef(t, ok, "expected util.InfoMsg, got %T", msg)
	require.Equal(t, typ, info.Type)
	return info
}

func TestHandleFilePathPaste_ReportsFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bigFile := filepath.Join(dir, "big.png")
	require.NoError(t, os.WriteFile(bigFile, make([]byte, common.MaxAttachmentSize+1), 0o644))

	tests := []struct {
		name string
		path string
		typ  util.InfoType
		msg  string
	}{
		{"missing file", filepath.Join(dir, "missing.png"), util.InfoTypeError, "missing.png"},
		{"directory", dir, util.InfoTypeWarn, "Cannot attach a directory"},
		{"file over the limit", bigFile, util.InfoTypeWarn, "File is too big"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newMockBusyUI(t)
			msg := m.handleFilePathPaste(tt.path)()

			info := requireInfoMsg(t, msg, tt.typ)
			require.Contains(t, info.Msg, tt.msg)
		})
	}
}

func TestHandleFilePathPaste_AttachesReadableFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pic.png")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o644))

	m, _ := newMockBusyUI(t)
	msg := m.handleFilePathPaste(path)()

	att, ok := msg.(message.Attachment)
	require.True(t, ok, "expected message.Attachment, got %T", msg)
	require.Equal(t, "pic.png", att.FileName)
	require.Equal(t, []byte("data"), att.Content)
}

func TestHandlePasteMsg_OversizedPasteReportsWarning(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)

	content := strings.Repeat("x", int(common.MaxAttachmentSize)+1)
	cmd := m.handlePasteMsg(tea.PasteMsg{Content: content})
	require.NotNil(t, cmd)

	info := requireInfoMsg(t, cmd(), util.InfoTypeWarn)
	require.Contains(t, info.Msg, "Paste is too big")
}

func TestEditorFinished(t *testing.T) {
	t.Parallel()

	t.Run("editor error is reported", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "msg.md")
		require.NoError(t, os.WriteFile(path, []byte("draft"), 0o644))

		msg := editorFinished(path, errors.New("editor exited with status 1"))

		info := requireInfoMsg(t, msg, util.InfoTypeError)
		require.Contains(t, info.Msg, "editor exited with status 1")
		require.NoFileExists(t, path)
	})

	t.Run("unreadable file is reported", func(t *testing.T) {
		t.Parallel()

		msg := editorFinished(filepath.Join(t.TempDir(), "gone.md"), nil)

		requireInfoMsg(t, msg, util.InfoTypeError)
	})

	t.Run("empty message is reported", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "msg.md")
		require.NoError(t, os.WriteFile(path, nil, 0o644))

		msg := editorFinished(path, nil)

		info := requireInfoMsg(t, msg, util.InfoTypeWarn)
		require.Equal(t, "Message is empty", info.Msg)
	})

	t.Run("content is returned trimmed", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "msg.md")
		require.NoError(t, os.WriteFile(path, []byte("  hello\n"), 0o644))

		msg := editorFinished(path, nil)

		got, ok := msg.(openEditorMsg)
		require.True(t, ok, "expected openEditorMsg, got %T", msg)
		require.Equal(t, "hello", got.Text)
		require.NoFileExists(t, path)
	})
}
