//go:build windows

package vscodemcptest

import (
	"net"
	"testing"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// listen starts a listener using the transport vscodemcp's
// dial_windows.go expects a lock file to advertise on this platform (a
// named pipe, not the Unix domain socket real VS Code only ever
// advertises on macOS/Linux), and returns the scheme/path a lock file
// should name for it.
func listen(t testing.TB) (l net.Listener, scheme, path string) {
	t.Helper()
	path = `\\.\pipe\angela-test-` + uuid.NewString()
	l, err := winio.ListenPipe(path, nil)
	require.NoError(t, err)
	return l, "pipe", path
}
