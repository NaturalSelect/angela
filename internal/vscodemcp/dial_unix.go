//go:build !windows

package vscodemcp

import (
	"context"
	"fmt"
	"net"
)

// dialLock connects to the Unix domain socket a lock file advertises.
func dialLock(ctx context.Context, lock LockFile) (net.Conn, error) {
	if lock.Scheme != "unix" {
		return nil, fmt.Errorf("unsupported lock scheme %q on this platform", lock.Scheme)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", lock.SocketPath)
}
