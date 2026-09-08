//go:build windows

package editorapproval

import "golang.org/x/sys/windows"

// processAlive reports whether a process with the given pid is currently
// running.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	// A zero timeout polls the handle instead of blocking: WAIT_TIMEOUT
	// means the process hasn't signaled (exited) yet, i.e. it's alive.
	// That status comes back as the event return value, not err: the
	// generated wrapper only sets err when the wait itself fails, so
	// checking err here reported every live process as dead.
	event, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return false
	}
	return event == uint32(windows.WAIT_TIMEOUT)
}
