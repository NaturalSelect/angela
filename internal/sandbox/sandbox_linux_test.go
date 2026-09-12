//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// fileGrantHelperEnv, when set to "1" in a subprocess re-running this
// same test binary, makes TestMain (in child_net_linux_test.go) run
// runFileGrantHelperProcess instead of the package's tests, for the
// same reason devFilesHelperEnv does.
const fileGrantHelperEnv = "ANGELA_TEST_ENTER_SANDBOX_FILE_GRANT"

// runDevFilesHelperProcess enters a sandbox with a read-only "/",
// mirroring DefaultConfig's workspace profile, and reports on stdout
// whether /dev/null stays writable and /dev/zero, /dev/full,
// /dev/random, and /dev/urandom stay readable, which a bare
// read-only "/" rule would otherwise deny.
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

	for _, name := range []string{"zero", "full", "random", "urandom"} {
		f, err := os.Open("/dev/" + name)
		if err != nil {
			fmt.Println(strings.ToUpper(name)+"_OPEN_FAILED:", err)
			return 0
		}
		buf := make([]byte, 4)
		_, readErr := f.Read(buf)
		f.Close()
		if readErr != nil {
			fmt.Println(strings.ToUpper(name)+"_READ_FAILED:", readErr)
			return 0
		}
		fmt.Println(strings.ToUpper(name) + "_READ_OK")
	}
	return 0
}

// TestLandlockSandbox_EnterSandbox_AllowsSafeDevFiles exercises
// the real Landlock enforcement end to end, in a subprocess: a
// read-only "/" sandbox, matching DefaultConfig's workspace profile,
// must still allow writing to /dev/null and reading from /dev/zero,
// /dev/full, /dev/random, and /dev/urandom.
func TestLandlockSandbox_EnterSandbox_AllowsSafeDevFiles(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), devFilesHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper subprocess output: %s", output)
	require.Contains(t, string(output), "NULL_WRITE_OK")
	require.Contains(t, string(output), "ZERO_READ_OK")
	require.Contains(t, string(output), "FULL_READ_OK")
	require.Contains(t, string(output), "RANDOM_READ_OK")
	require.Contains(t, string(output), "URANDOM_READ_OK")
}

// fileGrantHelperDirEnv carries the directory containing the two
// files runFileGrantHelperProcess probes: the sandboxed process only
// gets a ReadWriteFiles grant for one of them. A subprocess started
// from os.Args[0] alone can't see the parent test's t.TempDir(), so
// the parent passes it explicitly.
const fileGrantHelperDirEnv = "ANGELA_TEST_ENTER_SANDBOX_FILE_GRANT_DIR"

// runFileGrantHelperProcess enters a sandbox that grants ReadWrite
// access to exactly one file (fileGrantHelperDirEnv/key.txt), then
// reports on stdout whether it can still write that file and whether
// it can write a different, pre-existing file sitting right next to
// it. The second write must fail: a ReadWriteFiles entry for one
// file must not implicitly cover its siblings the way a ReadWrite
// entry for their shared parent directory would (see
// permission.FilesystemAllowPaths, which this guards against feeding
// a literal single-file allow rule into RWDirs instead of RWFiles).
func runFileGrantHelperProcess() int {
	dir := os.Getenv(fileGrantHelperDirEnv)
	if dir == "" {
		fmt.Println("MISSING_DIR")
		return 10
	}
	keyFile := filepath.Join(dir, "key.txt")

	if err := (LandlockSandbox{}).EnterSandbox(Config{ReadWriteFiles: []string{keyFile}}); err != nil {
		fmt.Println("ENTER_FAILED:", err)
		return 10
	}

	if err := os.WriteFile(keyFile, []byte("x"), 0o644); err != nil {
		fmt.Println("KEY_WRITE_FAILED:", err)
	} else {
		fmt.Println("KEY_WRITE_OK")
	}

	otherFile := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(otherFile, []byte("x"), 0o644); err != nil {
		fmt.Println("OTHER_WRITE_BLOCKED")
	} else {
		fmt.Println("OTHER_WRITE_SUCCEEDED")
	}
	return 0
}

// TestLandlockSandbox_EnterSandbox_FileGrantDoesNotCoverSiblings is
// the regression test for the vulnerability where a permission rule
// approving edits to a single literal file ended up granting write
// access to every file in its parent directory once fed into an
// OS-level sandbox. It exercises the real Landlock enforcement end to
// end, in a subprocess: granting ReadWriteFiles for exactly one file
// must still let it write that file, but must block writing to a
// different, pre-existing file in the same directory.
func TestLandlockSandbox_EnterSandbox_FileGrantDoesNotCoverSiblings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.txt"), []byte("secret"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("sibling"), 0o644))

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), fileGrantHelperEnv+"=1", fileGrantHelperDirEnv+"="+dir)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper subprocess output: %s", output)

	out := string(output)
	require.Contains(t, out, "KEY_WRITE_OK")
	require.Contains(t, out, "OTHER_WRITE_BLOCKED")
}
