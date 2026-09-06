package sandbox

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapForChildNetworkRestriction(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	require.NoError(t, err)

	wrapped, err := WrapForChildNetworkRestriction("/usr/bin/curl", []string{"curl", "https://example.com"})
	require.NoError(t, err)
	require.Equal(t, []string{self, childExecMarker, "/usr/bin/curl", "curl", "https://example.com"}, wrapped)
}

func TestWrapForChildNetworkRestriction_PreservesArgv0(t *testing.T) {
	t.Parallel()

	// The wrapped argv keeps the original args[0] (e.g. a bare command
	// name) distinct from the resolved path, matching how exec.Cmd
	// itself lets Path and Args[0] differ.
	wrapped, err := WrapForChildNetworkRestriction("/bin/busybox", []string{"sh", "-c", "echo hi"})
	require.NoError(t, err)
	require.Equal(t, "/bin/busybox", wrapped[2])
	require.Equal(t, []string{"sh", "-c", "echo hi"}, wrapped[3:])
}

// TestRunChildExecLauncherIfRequested_NoMarker verifies the launcher
// check is a no-op whenever argv doesn't request it. This is the only
// branch safe to exercise in-process: the marker branch never returns
// (it installs a real seccomp filter and execs), so it's covered by
// the subprocess-based tests in child_net_linux_test.go instead.
func TestRunChildExecLauncherIfRequested_NoMarker(t *testing.T) {
	t.Parallel()

	RunChildExecLauncherIfRequested([]string{"angela", "run", "hello"})
	RunChildExecLauncherIfRequested([]string{"angela"})
	RunChildExecLauncherIfRequested(nil)
}
