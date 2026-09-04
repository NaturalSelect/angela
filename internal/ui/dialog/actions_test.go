package dialog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// TestActionFilePickerSelected_Cmd_EmptyPath pins that an empty selection
// produces no command at all, rather than one that resolves to an error
// message about a missing file.
func TestActionFilePickerSelected_Cmd_EmptyPath(t *testing.T) {
	t.Parallel()

	action := ActionFilePickerSelected{}
	require.Nil(t, action.Cmd())
}

// TestActionFilePickerSelected_Cmd_ReadsFile verifies a valid, small file
// is read and reported as a message.Attachment carrying its path, name,
// detected MIME type, and raw bytes.
func TestActionFilePickerSelected_Cmd_ReadsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	content := []byte("hello world")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	action := ActionFilePickerSelected{Path: path}
	msg := action.Cmd()()

	attachment, ok := msg.(message.Attachment)
	require.True(t, ok, "expected a message.Attachment, got %T", msg)
	require.Equal(t, path, attachment.FilePath)
	require.Equal(t, "note.txt", attachment.FileName)
	require.True(t, bytes.Equal(content, attachment.Content))
	require.Contains(t, attachment.MimeType, "text/plain")
}

// TestActionFilePickerSelected_Cmd_FileTooLarge verifies a file over the
// attachment size limit is rejected with a user-facing error instead of
// being read into memory.
func TestActionFilePickerSelected_Cmd_FileTooLarge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	big := make([]byte, common.MaxAttachmentSize+1)
	require.NoError(t, os.WriteFile(path, big, 0o644))

	action := ActionFilePickerSelected{Path: path}
	msg := action.Cmd()()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a util.InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "file too large")
}

// TestActionFilePickerSelected_Cmd_MissingFile verifies a path that
// cannot be stat'd surfaces a readable error instead of panicking.
func TestActionFilePickerSelected_Cmd_MissingFile(t *testing.T) {
	t.Parallel()

	action := ActionFilePickerSelected{Path: filepath.Join(t.TempDir(), "missing.png")}
	msg := action.Cmd()()

	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected a util.InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "unable to read the image")
}
