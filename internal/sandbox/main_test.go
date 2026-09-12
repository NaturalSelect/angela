//go:build linux || darwin

package sandbox

import (
	"os"
	"testing"
)

// TestMain lets a subprocess spawned by this package's own tests
// re-run this same test binary as a disposable helper instead of the
// normal test suite: real sandbox enforcement (Landlock restricting a
// process for good, a seccomp filter blocking a process's own
// network, an actual Seatbelt profile) is irreversible, or otherwise
// unsafe to apply to the shared test binary itself, so every test
// that needs to observe it for real spawns os.Args[0] as a
// subprocess with an env var set, and that subprocess comes back
// through here instead of through m.Run(). See enforce_test.go for
// the shared cross-platform scenarios, and child_net_linux_test.go /
// seatbelt_darwin_test.go for the remaining platform-specific ones.
func TestMain(m *testing.M) {
	if code, ok := runEnforceHelperIfRequested(); ok {
		os.Exit(code)
	}
	if code, ok := runPlatformHelperIfRequested(); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
