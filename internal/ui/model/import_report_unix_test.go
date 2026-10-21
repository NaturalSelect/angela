//go:build !windows

package model

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadReportFileRejectsNamedPipe(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pipe.md")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("cannot create a named pipe here: %v", err)
	}

	_, err := readReportFile(path)
	require.ErrorContains(t, err, "not a regular file")
}
