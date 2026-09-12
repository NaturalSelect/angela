// Package sandbox restricts the current process's filesystem access,
// and outbound network access for commands it later spawns via the
// shell tool, using the strongest OS-level isolation available on the
// running platform.
package sandbox

import (
	"errors"
	"os"
	"runtime"
	"sync/atomic"
)

// ErrNotSupported indicates the running platform has no supported
// sandboxing mechanism.
var ErrNotSupported = errors.ErrUnsupported

// Config describes the restrictions to apply when entering a sandbox.
// Config describes the restrictions to apply when entering a sandbox.
type Config struct {
	// ReadWrite lists directories the process may read from and
	// write to.
	ReadWrite []string
	// ReadOnly lists directories the process may only read from.
	// Paths outside both ReadWrite and ReadOnly become inaccessible
	// once EnterSandbox succeeds.
	ReadOnly []string
	// ReadWriteFiles lists individual files the process may read
	// from and write to, without granting access to any other file
	// in their parent directory the way a ReadWrite entry for that
	// directory would. Use this for a path known to name one file
	// rather than a directory.
	ReadWriteFiles []string
	// ReadOnlyFiles lists individual files the process may only
	// read from, without granting access to any other file in their
	// parent directory the way a ReadOnly entry for that directory
	// would.
	ReadOnlyFiles []string
	// AllowNetwork leaves outbound network access unrestricted for
	// commands the shell tool spawns when true. When false, those
	// commands have their outbound network syscalls blocked (see
	// ShouldRestrictChildNetwork). It never affects the sandboxed
	// process's own network access, which Angela needs for its own
	// provider calls. Not enforced on macOS: SeatbeltSandbox logs a
	// warning instead of restricting anything, since Seatbelt can't
	// spare just this process's children the way Landlock's
	// restrictChildNetwork side channel does.
	AllowNetwork bool
}

// DefaultConfig returns the "workspace" profile: workingDir and
// dataDir (Angela's own state directory, skipped when empty) stay
// writable along with globalConfigDir and the system temp directory,
// the rest of the disk stays read-only, and outbound network is
// allowed.
func DefaultConfig(workingDir, dataDir, globalConfigDir string) Config {
	paths := []string{workingDir}
	if dataDir != "" {
		paths = append(paths, dataDir)
	}
	paths = append(paths, globalConfigDir, os.TempDir())
	return Config{
		ReadWrite:    DedupePaths(paths),
		ReadOnly:     []string{"/"},
		AllowNetwork: true,
	}
}

// DedupePaths drops empty and repeated entries while preserving order,
// so callers don't repeat a row when e.g. the data directory already
// lives under the working directory.
func DedupePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Sandbox restricts the current process to a set of filesystem paths
// (and, where supported, network access) using the strongest OS-level
// isolation available on the running platform.
type Sandbox interface {
	// IsInSandbox reports whether the current process is already
	// confined by an external sandbox (e.g. a Docker/OCI container)
	// or by an earlier call to EnterSandbox.
	IsInSandbox() bool

	// EnterSandbox restricts the process according to cfg. Filesystem
	// restrictions cover every goroutine in this process and are
	// inherited by every child process spawned afterwards. Network
	// restriction is narrower and never applies to this process
	// itself, which may still need outbound access (e.g. for its own
	// provider calls): it only marks commands the shell tool spawns
	// afterward for restriction, see ShouldRestrictChildNetwork. It is
	// irreversible for the life of the process: once entered, access
	// can only be narrowed further, never widened. On platforms
	// without a supported enforcement mechanism, it fails with
	// ErrNotSupported instead of restricting anything. If Landlock is
	// merely unavailable on the running (Linux) kernel, it degrades to
	// a safe no-op instead of failing. On macOS, SeatbeltSandbox
	// applies cfg by relaunching the process under sandbox-exec and
	// never returns on success; see its doc for why that limits it to
	// startup, before MarkStartupComplete is called.
	EnterSandbox(cfg Config) error
}

// New returns the Sandbox implementation appropriate for the current
// process: NoneSandbox on platforms without a supported enforcement
// mechanism, DockerSandbox if a Linux process is already confined by
// a Docker/OCI container, LandlockSandbox on any other Linux host,
// and SeatbeltSandbox on macOS. noDockerSandbox disables the
// Docker/OCI shortcut so a container is treated like any other Linux
// host, still getting Landlock enforcement on top of it; it has no
// effect outside Linux.
func New(noDockerSandbox bool) Sandbox {
	switch runtime.GOOS {
	case "linux":
		if !noDockerSandbox && InDocker() {
			return DockerSandbox{}
		}
		return LandlockSandbox{}
	case "darwin":
		return SeatbeltSandbox{}
	default:
		return NoneSandbox{}
	}
}

// NoneSandbox represents a platform with no supported sandboxing
// mechanism.
type NoneSandbox struct{}

// IsInSandbox always reports false: this platform has no mechanism
// to confine the process.
func (NoneSandbox) IsInSandbox() bool { return false }

// EnterSandbox always fails with ErrNotSupported: this platform has
// no mechanism to confine the process.
func (NoneSandbox) EnterSandbox(Config) error { return ErrNotSupported }

// restrictChildNetwork tracks whether EnterSandbox was called with
// Config.AllowNetwork false. It never restricts the sandboxed
// process's own network access: only ShouldRestrictChildNetwork's
// callers (the shell tool) consult it, to block a spawned command's
// own outbound network instead.
var restrictChildNetwork atomic.Bool

// ShouldRestrictChildNetwork reports whether a command the shell tool
// is about to spawn should have its outbound network access blocked.
// Use WrapForChildNetworkRestriction to apply the restriction.
func ShouldRestrictChildNetwork() bool {
	return restrictChildNetwork.Load()
}

// startupComplete tracks whether the process has moved past the
// point where SeatbeltSandbox can safely relaunch it under
// sandbox-exec: once app.New returns, the process holds a database
// connection, background goroutines, and, in the TUI, terminal state
// that a relaunch would silently discard. LandlockSandbox and
// DockerSandbox don't consult it: restricting them in place has no
// such window.
var startupComplete atomic.Bool

// MarkStartupComplete records that the process now holds state a
// relaunch would lose, so SeatbeltSandbox.EnterSandbox must no longer
// attempt one. Call it once, after startup's own EnterSandbox callers
// (e.g. --sandbox in setupLocalWorkspace) have already run.
func MarkStartupComplete() {
	startupComplete.Store(true)
}
