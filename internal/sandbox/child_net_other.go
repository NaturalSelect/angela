//go:build !linux

package sandbox

import (
	"fmt"
	"os"
)

// runChildExecLauncher exists only so RunChildExecLauncherIfRequested
// compiles on every platform. ShouldRestrictChildNetwork is always
// false outside Linux (SeatbeltSandbox.EnterSandbox on macOS
// deliberately never sets it; see its doc), so
// WrapForChildNetworkRestriction never runs and this is never reached
// in normal operation.
func runChildExecLauncher(_ []string) {
	fmt.Fprintln(os.Stderr, "angela: sandboxed child exec is only supported on Linux")
	os.Exit(127)
}
