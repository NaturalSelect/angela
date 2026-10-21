package model

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/stretchr/testify/require"
)

func TestReadReportFile(t *testing.T) {
	t.Parallel()

	t.Run("reads a regular file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "report.md")
		require.NoError(t, os.WriteFile(path, []byte("# Findings\n\nBody.\n"), 0o644))

		report, err := readReportFile(path)
		require.NoError(t, err)
		require.Equal(t, "report.md", report.Task)
		require.Equal(t, "# Findings\n\nBody.\n", report.Content)
	})

	t.Run("rejects a directory", func(t *testing.T) {
		t.Parallel()
		_, err := readReportFile(t.TempDir())
		require.ErrorContains(t, err, "is a directory")
	})

	t.Run("rejects a file over the limit", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "big.md")
		require.NoError(t, os.WriteFile(path, make([]byte, common.MaxAttachmentSize+1), 0o644))

		_, err := readReportFile(path)
		require.ErrorContains(t, err, "too large")
	})

	t.Run("accepts a file exactly at the limit", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "edge.md")
		require.NoError(t, os.WriteFile(path, make([]byte, common.MaxAttachmentSize), 0o644))

		_, err := readReportFile(path)
		require.NoError(t, err)
	})

	t.Run("rejects a named pipe without blocking", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pipe.md")
		if err := syscall.Mkfifo(path, 0o644); err != nil {
			t.Skipf("cannot create a named pipe here: %v", err)
		}

		_, err := readReportFile(path)
		require.ErrorContains(t, err, "not a regular file")
	})
}
