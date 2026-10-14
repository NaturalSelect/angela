//go:build windows

package profiling

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
)

// pipeName names the pipe of the process pid whose marker lives in dir.
// NOTE: Pipe names are shared by the whole machine, unlike socket paths, so
// the name carries the directory too. Otherwise two endpoints for the same
// pid in different directories would fight over one pipe.
func pipeName(dir string, pid int) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(dir))
	return fmt.Sprintf(`\\.\pipe\angela-pprof-%016x-%d`, h.Sum64(), pid)
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
	ln, err := winio.ListenPipe(pipeName(dir, pid), cfg)
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

func dialEndpoint(ctx context.Context, dir string, pid int) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(dir, pid))
}
