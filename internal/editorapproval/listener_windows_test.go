//go:build windows

package editorapproval

import (
	"net"
	"testing"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeLockListener starts a listener for startFakeVSCodeServer using the
// transport dial_windows.go expects a lock file to advertise on this
// platform (a named pipe, not the Unix domain socket real VS Code only
// ever advertises on macOS/Linux), and returns the scheme/path a lock
// file should name for it.
func fakeLockListener(t *testing.T) (l net.Listener, scheme, path string) {
	t.Helper()
	path = `\\.\pipe\angela-test-` + uuid.NewString()
	l, err := winio.ListenPipe(path, nil)
	require.NoError(t, err)
	return l, "pipe", path
}
