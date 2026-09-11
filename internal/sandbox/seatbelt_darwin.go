//go:build darwin

package sandbox

import (
	"os"
	"syscall"
)

// relaunchUnderSeatbelt replaces this process with sandbox-exec
// applying profile, which execs back into exe (Angela's own binary)
// with args once it has restricted itself. The Seatbelt restriction
// sandbox-exec installs on itself persists across that final exec,
// the same way it persists across any exec once installed, so exe
// ends up running under profile with the PID this process already
// had. It only returns on failure: on success it never returns, and
// os.Args[1:] (passed by EnterSandbox) carries the same arguments
// this process was invoked with, so a successful relaunch is
// indistinguishable from a normal startup other than IsInSandbox now
// reporting true.
func relaunchUnderSeatbelt(profile, exe string) error {
	env := append(os.Environ(), seatbeltMarkerEnv+"=1")
	argv := seatbeltRelaunchArgv(profile, exe, os.Args[1:])
	return syscall.Exec(sandboxExecPath, argv, env)
}
