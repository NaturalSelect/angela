package sandbox

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// DockerSandbox represents a process already confined by an external
// Docker (or other OCI) container. The container is trusted to
// already provide both the filesystem and network isolation its
// operator wants, so EnterSandbox is a no-op; pass
// --no-docker-sandbox to have Angela apply LandlockSandbox's own
// restrictions on top of the container instead of trusting it.
type DockerSandbox struct{}

// IsInSandbox always reports true: a Docker/OCI container was
// detected at startup.
func (DockerSandbox) IsInSandbox() bool { return true }

// EnterSandbox is a no-op: the surrounding container is trusted to
// already confine both the filesystem and cfg.AllowNetwork's intent
// the way its operator wants, unlike LandlockSandbox which must
// enforce them itself.
func (DockerSandbox) EnterSandbox(Config) error { return nil }

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
