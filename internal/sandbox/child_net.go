package sandbox

import (
	"fmt"
	"os"
)

// childExecMarker, when passed as argv[1] to Angela's own executable,
// tells RunChildExecLauncherIfRequested to skip normal command-line
// handling and instead act as a network-restricting launcher for a
// single command: see WrapForChildNetworkRestriction.
const childExecMarker = "__angela_sandbox_child_exec__"

// WrapForChildNetworkRestriction returns the argv Angela should exec
// instead of (path, args) so that, once the real command starts, its
// outbound network access is blocked. The returned slice's first
// element is both the executable to run (Angela's own binary,
// relaunched as a launcher) and its own argv[0]; callers that need
// them separately can use wrapped[0] as the path and wrapped as Args.
//
// Landlock, the mechanism EnterSandbox otherwise uses, restricts
// network access process-wide with no way to spare just this
// process's children, so blocking a spawned command's network instead
// goes through a short-lived re-exec: the restriction is installed
// with a seccomp filter in that freshly started process, not in
// Angela's long-lived one, and it persists across the exec that
// replaces the launcher with path.
func WrapForChildNetworkRestriction(path string, args []string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve angela executable for sandboxed exec: %w", err)
	}
	wrapped := make([]string, 0, len(args)+3)
	wrapped = append(wrapped, self, childExecMarker, path)
	wrapped = append(wrapped, args...)
	return wrapped, nil
}

// RunChildExecLauncherIfRequested checks whether argv (os.Args)
// requests the launcher path installed by
// WrapForChildNetworkRestriction and, if so, never returns: it
// installs the outbound-network filter on the current process and
// execs into the real command, or exits the process with a
// diagnostic on failure. Callers must invoke this before any normal
// command-line handling, since argv[1] here is not a real Angela
// flag.
func RunChildExecLauncherIfRequested(argv []string) {
	if len(argv) < 3 || argv[1] != childExecMarker {
		return
	}
	runChildExecLauncher(argv[2:])
}
