// Package sandbox restricts the current process's filesystem access
// using the strongest OS-level isolation available on the running
// platform.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// ErrNotSupported indicates the running platform has no supported
// sandboxing mechanism.
var ErrNotSupported = errors.ErrUnsupported

// Config describes the restrictions to apply when entering a sandbox.
type Config struct {
	// ReadWrite lists directories the process may read from and
	// write to.
	ReadWrite []string
	// ReadOnly lists directories the process may only read from.
	// Paths outside both ReadWrite and ReadOnly become inaccessible
	// once EnterSandbox succeeds.
	ReadOnly []string
	// AllowNetwork leaves outbound network access unrestricted when
	// true. When false, EnterSandbox blocks it. Since restriction is
	// process-wide, this also blocks the sandboxed process's own
	// network calls, not just its children's.
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

	// EnterSandbox restricts the process according to cfg. The
	// restriction covers every goroutine and is inherited by every
	// child process spawned afterwards. It is irreversible for the
	// life of the process: once entered, access can only be narrowed
	// further, never widened. On platforms without a supported
	// enforcement mechanism, it fails with ErrNotSupported instead of
	// restricting anything. If Landlock is merely unavailable on the
	// running (Linux) kernel, it degrades to a safe no-op instead of
	// failing.
	EnterSandbox(cfg Config) error
}

// New returns the Sandbox implementation appropriate for the current
// process: a NoneSandbox on platforms without a supported enforcement
// mechanism, a DockerSandbox if the process is already confined by a
// Docker/OCI container, otherwise a LandlockSandbox.
func New() Sandbox {
	if runtime.GOOS != "linux" {
		return NoneSandbox{}
	}
	if InDocker() {
		return DockerSandbox{}
	}
	return LandlockSandbox{}
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

// DockerSandbox represents a process already confined by an external
// Docker (or other OCI) container. The container itself already
// provides the isolation, so EnterSandbox is a no-op.
type DockerSandbox struct{}

// IsInSandbox always reports true: a Docker/OCI container was
// detected at startup.
func (DockerSandbox) IsInSandbox() bool { return true }

// EnterSandbox is a no-op: the surrounding container already confines
// the process.
func (DockerSandbox) EnterSandbox(Config) error { return nil }

// entered tracks whether LandlockSandbox.EnterSandbox has already
// restricted this process. Landlock confinement is process-wide and
// irreversible, so this is process-global state rather than
// per-instance state.
var entered atomic.Bool

// LandlockSandbox restricts the process using the Linux Landlock LSM.
// On platforms or kernels without Landlock support, EnterSandbox
// degrades to a safe no-op rather than failing.
type LandlockSandbox struct{}

// IsInSandbox reports whether EnterSandbox has already restricted
// this process.
func (LandlockSandbox) IsInSandbox() bool {
	return entered.Load()
}

// EnterSandbox applies cfg using Landlock's best-effort mode: it
// enforces as much as the running kernel supports and never fails
// just because a stronger ABI version isn't available.
func (LandlockSandbox) EnterSandbox(cfg Config) error {
	cf := landlock.V10.BestEffort()

	rules := make([]landlock.Rule, 0, 2)
	if len(cfg.ReadOnly) > 0 {
		rules = append(rules, landlock.RODirs(cfg.ReadOnly...).IgnoreIfMissing())
	}
	if len(cfg.ReadWrite) > 0 {
		rules = append(rules, landlock.RWDirs(cfg.ReadWrite...).IgnoreIfMissing())
	}
	if len(rules) > 0 {
		if err := cf.RestrictPaths(rules...); err != nil {
			return fmt.Errorf("enter sandbox: restrict paths: %w", err)
		}
	}

	if !cfg.AllowNetwork {
		// No rules permitted means no TCP bind/connect is allowed.
		if err := cf.RestrictNet(); err != nil {
			return fmt.Errorf("enter sandbox: restrict network: %w", err)
		}
	}

	entered.Store(true)
	return nil
}

// InDocker reports whether the current process is running inside a
// Docker (or other OCI) container. The result is cached for the
// process's lifetime since container status never changes during a
// process's lifetime.
var InDocker = sync.OnceValue(func() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "docker") ||
		strings.Contains(content, "containerd") ||
		strings.Contains(content, "kubepods")
})
