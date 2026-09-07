//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// devFilesHelperEnv, when set to "1" in a subprocess re-running this
// same test binary, makes TestMain (in child_net_linux_test.go) run
// runDevFilesHelperProcess instead of the package's tests. Applying a
// real read-only "/" Landlock restriction in the main test process
// would be unsafe: it's irreversible and would break every later
// test in this binary that touches the filesystem, so the real
// enforcement is only ever exercised in a disposable subprocess.
const devFilesHelperEnv = "ANGELA_TEST_ENTER_SANDBOX_DEV_FILES"

// runDevFilesHelperProcess enters a sandbox with a read-only "/",
// mirroring DefaultConfig's workspace profile, and reports on stdout
// whether /dev/null stays writable and /dev/zero stays readable,
// which a bare read-only "/" rule would otherwise deny.
func runDevFilesHelperProcess() int {
	if err := (LandlockSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}}); err != nil {
		fmt.Fprintln(os.Stderr, "enter sandbox:", err)
		return 10
	}

	null, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		fmt.Println("NULL_OPEN_FAILED:", err)
		return 0
	}
	_, writeErr := null.Write([]byte("x"))
	null.Close()
	if writeErr != nil {
		fmt.Println("NULL_WRITE_FAILED:", writeErr)
		return 0
	}
	fmt.Println("NULL_WRITE_OK")

	zero, err := os.Open("/dev/zero")
	if err != nil {
		fmt.Println("ZERO_OPEN_FAILED:", err)
		return 0
	}
	defer zero.Close()
	buf := make([]byte, 4)
	if _, err := zero.Read(buf); err != nil {
		fmt.Println("ZERO_READ_FAILED:", err)
		return 0
	}
	fmt.Println("ZERO_READ_OK")
	return 0
}

// TestLandlockSandbox_EnterSandbox_AllowsDevNullAndDevZero exercises
// the real Landlock enforcement end to end, in a subprocess: a
// read-only "/" sandbox, matching DefaultConfig's workspace profile,
// must still allow writing to /dev/null and reading from /dev/zero.
func TestLandlockSandbox_EnterSandbox_AllowsDevNullAndDevZero(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), devFilesHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper subprocess output: %s", output)
	require.Contains(t, string(output), "NULL_WRITE_OK")
	require.Contains(t, string(output), "ZERO_READ_OK")
}
