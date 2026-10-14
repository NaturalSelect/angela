//go:build !windows

package profiling

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
)

func listenEndpoint(dir string, pid int) (net.Listener, error) {
	path := endpointPath(dir, pid)
	// NOTE: A file under our own pid can only be left over from a dead
	// process whose pid was reused, so it is safe to replace.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var lc net.ListenConfig
	return lc.Listen(context.Background(), "unix", path)
}

func dialEndpoint(ctx context.Context, dir string, pid int) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpointPath(dir, pid))
}
