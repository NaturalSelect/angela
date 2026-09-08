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
	_, err = windows.WaitForSingleObject(h, 0)
	return err == windows.WAIT_TIMEOUT
}
