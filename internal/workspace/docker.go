package workspace

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// detectInDocker reports whether the current process is running inside a
// Docker (or other OCI) container. The result is computed once per process
// since container status never changes during a process's lifetime.
var detectInDocker = sync.OnceValue(func() bool {
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
