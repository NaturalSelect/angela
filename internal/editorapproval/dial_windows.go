//go:build windows

package editorapproval

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// dialLock connects to the named pipe a lock file advertises.
func dialLock(ctx context.Context, lock lockFile) (net.Conn, error) {
	if lock.Scheme != "pipe" {
		return nil, fmt.Errorf("unsupported lock scheme %q on this platform", lock.Scheme)
	}
	return winio.DialPipeContext(ctx, lock.SocketPath)
}
