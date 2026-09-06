//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestBuildChildNetworkFilter(t *testing.T) {
	t.Parallel()

	filter := buildChildNetworkFilter()
	require.Len(t, filter, len(blockedNetworkSyscalls)+3)

	// First instruction loads the syscall number (seccomp_data.nr).
	require.Equal(t, uint16(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS), filter[0].Code)
	require.EqualValues(t, 0, filter[0].K)

	// One JEQ check per blocked syscall, each jumping past the
	// remaining checks straight to the EPERM return on a match.
	for i, sys := range blockedNetworkSyscalls {
		insn := filter[i+1]
		require.Equal(t, uint16(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K), insn.Code)
		require.Equal(t, sys, insn.K)
		remaining := len(blockedNetworkSyscalls) - i - 1
		require.EqualValues(t, remaining+1, insn.Jt)
		require.EqualValues(t, 0, insn.Jf)
	}

	allow := filter[len(filter)-2]
	require.Equal(t, uint16(unix.BPF_RET|unix.BPF_K), allow.Code)
	require.Equal(t, seccompRetAllow, allow.K)

	deny := filter[len(filter)-1]
	require.Equal(t, uint16(unix.BPF_RET|unix.BPF_K), deny.Code)
	require.Equal(t, seccompRetErrno|uint32(unix.EPERM), deny.K)
}

// networkFilterHelperEnv and its siblings below, when set to "1" in a
// subprocess re-running this same test binary, make TestMain act as a
// standalone helper instead of running the package's tests. Installing
// the real outbound-network filter in the normal test process would be
// unsafe: it's irreversible and would break every later test in this
// binary that needs the network, so the real enforcement is only ever
// exercised in a disposable subprocess.
const (
	networkFilterHelperEnv = "ANGELA_TEST_INSTALL_NETWORK_FILTER"
	launcherDriverEnv      = "ANGELA_TEST_RUN_CHILD_EXEC_LAUNCHER"
	dialCheckEnv           = "ANGELA_TEST_DIAL_CHECK"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(networkFilterHelperEnv) == "1":
		os.Exit(runNetworkFilterHelperProcess())
	case os.Getenv(launcherDriverEnv) == "1":
		// Hands off to dialCheckEnv instead of re-triggering this same
		// branch: runChildExecLauncher execs into this very binary
		// again with the current environment, and it never returns.
		_ = os.Unsetenv(launcherDriverEnv)
		_ = os.Setenv(dialCheckEnv, "1")
		runChildExecLauncher([]string{os.Args[0], os.Args[0]})
	case os.Getenv(dialCheckEnv) == "1":
		os.Exit(runDialCheckProcess())
	default:
		os.Exit(m.Run())
	}
}

// runDialCheckProcess is the "real command" runChildExecLauncher execs
// into in TestRunChildExecLauncher_BlocksTargetNetwork. It reports the
// outcome of a dial attempt on stdout instead of via exit code, since
// by the time it runs it no longer has anything else to hand a code
// back through other than this process's own exit.
func runDialCheckProcess() int {
	_, err := net.DialTimeout("tcp", "127.0.0.1:1", 2*time.Second)
	switch {
	case err == nil:
		fmt.Println("CONNECTED")
	case errors.Is(err, syscall.EPERM):
		fmt.Println("BLOCKED")
	default:
		fmt.Println("ERROR:", err)
	}
	return 0
}

// runNetworkFilterHelperProcess installs the filter and tries to dial
// out; its exit code tells the parent test what happened. It locks to
// the current OS thread first for the same reason
// runChildExecLauncher does: a non-TSYNC seccomp filter only applies
// to the thread that installed it, and an unlocked goroutine could
// have its later syscalls, including the dial below, land on a
// different thread than the one that just installed the filter.
func runNetworkFilterHelperProcess() int {
	runtime.LockOSThread()

	if err := installChildNetworkFilter(); err != nil {
		fmt.Fprintln(os.Stderr, "install filter:", err)
		return 10
	}

	conn, dialErr := net.DialTimeout("tcp", "127.0.0.1:1", 2*time.Second)
	if dialErr == nil {
		_ = conn.Close()
		fmt.Fprintln(os.Stderr, "dial unexpectedly succeeded")
		return 11
	}
	if !errors.Is(dialErr, syscall.EPERM) {
		fmt.Fprintln(os.Stderr, "dial failed with unexpected error:", dialErr)
		return 12
	}
	return 0
}

// TestInstallChildNetworkFilter_BlocksOutboundConnect exercises the
// real seccomp enforcement end to end, in a subprocess: it verifies
// that once installChildNetworkFilter runs, an outbound connect is
// rejected with EPERM rather than reaching the network.
func TestInstallChildNetworkFilter_BlocksOutboundConnect(t *testing.T) {
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), networkFilterHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper subprocess output: %s", output)
}

// TestRunChildExecLauncher_BlocksTargetNetwork exercises the actual
// mechanism WrapForChildNetworkRestriction relies on: a process
// installs the filter on itself and then execs into a *different*
// program (runDialCheckProcess, reached via a second env var so the
// exec really does reload this binary from scratch), verifying the
// filter survives that exec and still blocks the new program's
// outbound connect.
func TestRunChildExecLauncher_BlocksTargetNetwork(t *testing.T) {
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), launcherDriverEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "driver subprocess output: %s", output)
	require.Contains(t, string(output), "BLOCKED")
}
