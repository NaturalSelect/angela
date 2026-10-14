//go:build windows

package profiling

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
)

func pipeName(pid int) string {
	return fmt.Sprintf(`\\.\pipe\angela-pprof-%d`, pid)
}

// markerListener removes the marker file that advertises the pipe once the
// pipe closes.
type markerListener struct {
	net.Listener
	marker string
}

func (l markerListener) Close() error {
	err := l.Listener.Close()
	_ = os.Remove(l.marker)
	return err
}

func listenEndpoint(dir string, pid int) (net.Listener, error) {
	cfg := &winio.PipeConfig{
		MessageMode:      true,
		InputBufferSize:  65536,
		OutputBufferSize: 65536,
	}
	ln, err := winio.ListenPipe(pipeName(pid), cfg)
	if err != nil {
		return nil, err
	}

	marker := endpointPath(dir, pid)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return markerListener{Listener: ln, marker: marker}, nil
}

func dialEndpoint(ctx context.Context, _ string, pid int) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(pid))
}
