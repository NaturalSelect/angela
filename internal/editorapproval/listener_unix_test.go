//go:build !windows

package editorapproval

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// shortSocketDir returns a fresh temp directory suitable as the base for
// a Unix socket path. Unlike t.TempDir(), it doesn't embed the test
// name, so paths built under it stay well below the 104-byte macOS
// sun_path limit regardless of how long the test name is (see the same
// pattern in internal/herdr/client_test.go and internal/server).
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vscodemcp-sock")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// fakeLockListener starts a listener for startFakeVSCodeServer using the
// transport dial_unix.go expects a lock file to advertise on this
// platform (a Unix domain socket), and returns the scheme/path a lock
// file should name for it.
func fakeLockListener(t *testing.T) (l net.Listener, scheme, path string) {
	t.Helper()
	path = filepath.Join(shortSocketDir(t), "mcp.sock")
	l, err := net.Listen("unix", path) //nolint:noctx
	require.NoError(t, err)
	return l, "unix", path
}
