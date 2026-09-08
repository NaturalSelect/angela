//go:build !windows

package editorapproval

import "syscall"

// processAlive reports whether a process with the given pid is currently
// running. Signal 0 doesn't affect the process; it only checks whether
// we're allowed to signal it. EPERM still means it exists, just owned by
// another user.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
