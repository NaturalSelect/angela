// Package profiling exposes the Go runtime profiles of a running Angela
// process over a private per-process endpoint, so they can be exported
// without opening a TCP port.
//
// Each serving process owns one endpoint under a per-user directory: a Unix
// domain socket, or a named pipe on Windows. The directory is only
// accessible to the current user, which is what keeps the profiles private.
package profiling

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	registryDirName = "angela-pprof"
	endpointExt     = ".sock"
	pprofPrefix     = "/debug/pprof/"

	// maxSocketPathLen is the macOS sun_path limit, the smaller of the
	// platform limits.
	maxSocketPathLen = 104
	// longestPID is the widest pid any supported platform hands out.
	longestPID = 9999999
)

// Start serves the profiles of the current process. The returned stop
// function removes the endpoint and is safe to call more than once.
func Start() (stop func(), err error) {
	return serve(registryDir(), os.Getpid())
}

func serve(dir string, pid int) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create profile endpoint directory: %w", err)
	}
	// NOTE: The directory may already exist with looser permissions or
	// belong to another user. Chmod fails in the second case, which is
	// what stops us from publishing an endpoint inside someone else's
	// directory.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to secure profile endpoint directory: %w", err)
	}

	ln, err := listenEndpoint(dir, pid)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on profile endpoint: %w", err)
	}

	// No WriteTimeout: cpu and trace profiles stream for their whole
	// duration.
	srv := &http.Server{Handler: newHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	return sync.OnceFunc(func() {
		_ = srv.Close()
		_ = ln.Close()
	}), nil
}

// newHandler serves the pprof handlers on a private mux rather than
// http.DefaultServeMux, which the ANGELA_PROFILE TCP listener shares.
func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(pprofPrefix, pprof.Index)
	mux.HandleFunc(pprofPrefix+"cmdline", pprof.Cmdline)
	mux.HandleFunc(pprofPrefix+"profile", pprof.Profile)
	mux.HandleFunc(pprofPrefix+"trace", pprof.Trace)
	return mux
}

// registryDir returns the per-user directory holding one endpoint per
// serving process.
func registryDir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}

	name := registryDirName
	// The per-user suffix keeps users from colliding in a shared /tmp.
	// Getuid is -1 on Windows, whose temp directory is already per-user.
	if uid := os.Getuid(); uid >= 0 {
		name = fmt.Sprintf("%s-%d", name, uid)
	}
	dir := filepath.Join(base, name)
	if runtime.GOOS != "windows" && len(endpointPath(dir, longestPID)) > maxSocketPathLen {
		dir = filepath.Join("/tmp", name)
	}
	return dir
}

// endpointPath is the Unix socket itself, or on Windows the marker file
// that advertises the named pipe of the same pid.
func endpointPath(dir string, pid int) string {
	return filepath.Join(dir, fmt.Sprintf("%d%s", pid, endpointExt))
}
