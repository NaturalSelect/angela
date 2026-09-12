//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// seatbeltHelperEnv, when set to "1" in a subprocess re-running this
// same test binary, makes TestMain run runSeatbeltHelperProcess
// instead of the package's tests. EnterSandbox's relaunch replaces
// the calling process and the resulting sandbox is irreversible for
// its life, so it's only ever exercised in a disposable subprocess,
// the same way sandbox_linux_test.go isolates Landlock's real
// enforcement from the rest of this package's tests.
const seatbeltHelperEnv = "ANGELA_TEST_ENTER_SEATBELT_SANDBOX"

// seatbeltHelperRWDirEnv carries the one directory
// runSeatbeltHelperProcess is allowed to write to. A subprocess
// started from os.Args[0] alone can't see the parent test's
// t.TempDir(), so the parent passes it explicitly.
const seatbeltHelperRWDirEnv = "ANGELA_TEST_ENTER_SEATBELT_SANDBOX_RWDIR"

// seatbeltFileGrantHelperEnv, when set to "1" in a subprocess
// re-running this same test binary, makes TestMain run
// runSeatbeltFileGrantHelperProcess instead of the package's tests,
// for the same reason seatbeltHelperEnv does.
const seatbeltFileGrantHelperEnv = "ANGELA_TEST_ENTER_SEATBELT_SANDBOX_FILE_GRANT"

// seatbeltFileGrantHelperDirEnv carries the directory containing the
// two files runSeatbeltFileGrantHelperProcess probes: the sandboxed
// process only gets a ReadWriteFiles grant for one of them.
const seatbeltFileGrantHelperDirEnv = "ANGELA_TEST_ENTER_SEATBELT_SANDBOX_FILE_GRANT_DIR"

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(seatbeltHelperEnv) == "1":
		os.Exit(runSeatbeltHelperProcess())
	case os.Getenv(seatbeltFileGrantHelperEnv) == "1":
		os.Exit(runSeatbeltFileGrantHelperProcess())
	default:
		os.Exit(m.Run())
	}
}

// runSeatbeltFileGrantHelperProcess enters a sandbox that grants
// ReadWrite access to exactly one file
// (seatbeltFileGrantHelperDirEnv/key.txt), then reports on stdout
// whether it can still write that file and whether it can write a
// different, pre-existing file sitting right next to it. The second
// write must fail: a ReadWriteFiles entry for one file must not
// implicitly cover its siblings the way a ReadWrite entry for their
// shared parent directory would (see permission.FilesystemAllowPaths,
// which this guards against feeding a literal single-file allow rule
// into a "subpath" rule instead of a "literal" one).
func runSeatbeltFileGrantHelperProcess() int {
	dir := os.Getenv(seatbeltFileGrantHelperDirEnv)
	if dir == "" {
		fmt.Println("MISSING_DIR")
		return 10
	}
	keyFile := filepath.Join(dir, "key.txt")

	if err := (SeatbeltSandbox{}).EnterSandbox(Config{ReadWriteFiles: []string{keyFile}}); err != nil {
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

// runSeatbeltHelperProcess enters a Seatbelt sandbox restricted to
// the directory named by seatbeltHelperRWDirEnv, mirroring
// DefaultConfig's workspace profile, then probes it from both sides.
// Each check reports its own outcome on stdout instead of relying on
// a single overall exit code, so the parent test can assert on
// exactly which restriction (if any) failed.
func runSeatbeltHelperProcess() int {
	rwDir := os.Getenv(seatbeltHelperRWDirEnv)
	if rwDir == "" {
		fmt.Println("MISSING_RWDIR")
		return 10
	}

	cfg := Config{ReadOnly: []string{"/"}, ReadWrite: []string{rwDir}}
	if err := (SeatbeltSandbox{}).EnterSandbox(cfg); err != nil {
		fmt.Println("ENTER_FAILED:", err)
		return 10
	}

	// A second EnterSandbox call, now that the marker set by the
	// first is already present, must be a no-op rather than
	// attempting to relaunch again.
	if err := (SeatbeltSandbox{}).EnterSandbox(cfg); err != nil {
		fmt.Println("REENTER_FAILED:", err)
		return 10
	}
	if !(SeatbeltSandbox{}).IsInSandbox() {
		fmt.Println("NOT_IN_SANDBOX")
		return 10
	}
	fmt.Println("ENTER_OK")

	allowedFile := filepath.Join(rwDir, "allowed")
	if err := os.WriteFile(allowedFile, []byte("x"), 0o644); err != nil {
		fmt.Println("ALLOWED_WRITE_FAILED:", err)
	} else {
		fmt.Println("ALLOWED_WRITE_OK")
	}

	// deniedDir is rwDir's own parent: covered by the read-only "/"
	// rule, but never itself granted read-write, so writes into it
	// must fail even though the sandboxed process created rwDir
	// itself moments ago (as far as ordinary Unix permissions go,
	// nothing else would stop the write).
	deniedDir := filepath.Dir(rwDir)
	deniedFile := filepath.Join(deniedDir, fmt.Sprintf("angela-seatbelt-denied-%d", os.Getpid()))
	if err := os.WriteFile(deniedFile, []byte("x"), 0o644); err != nil {
		fmt.Println("DENIED_WRITE_BLOCKED")
	} else {
		fmt.Println("DENIED_WRITE_SUCCEEDED")
		os.Remove(deniedFile)
	}

	if err := os.WriteFile("/dev/null", []byte("x"), 0o644); err != nil {
		fmt.Println("DEVNULL_WRITE_FAILED:", err)
	} else {
		fmt.Println("DEVNULL_WRITE_OK")
	}

	if _, err := os.ReadFile("/etc/hosts"); err != nil {
		fmt.Println("READONLY_READ_FAILED:", err)
	} else {
		fmt.Println("READONLY_READ_OK")
	}

	// A child process inherits the same restriction, and must be
	// able to start at all: it needs the profile's dynamic-linker and
	// system-library allowances to run /usr/bin/touch in the first
	// place, then the same file-write denial that blocked the direct
	// write above must block it too.
	if err := exec.Command("/usr/bin/touch", deniedFile).Run(); err != nil {
		fmt.Println("CHILD_DENIED_WRITE_BLOCKED")
	} else {
		fmt.Println("CHILD_DENIED_WRITE_SUCCEEDED")
		os.Remove(deniedFile)
	}

	return 0
}

// requireSandboxExec skips t when /usr/bin/sandbox-exec isn't
// present. Apple's man page marks it deprecated and it could be
// removed from a future macOS release, at which point --sandbox would
// fail on startup rather than this test failing to relaunch.
func requireSandboxExec(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(sandboxExecPath); err != nil {
		t.Skipf("%s not available: %v", sandboxExecPath, err)
	}
}

// TestSeatbeltSandbox_EnterSandbox_RestrictsFilesystem exercises the
// real Seatbelt enforcement end to end, in a subprocess: entering a
// sandbox restricted to a single directory must allow writes inside
// it and block writes to its parent, both directly and from a
// spawned child, while still allowing the /dev/null write and
// /etc/hosts read a bare read-only "/" rule would otherwise deny.
func TestSeatbeltSandbox_EnterSandbox_RestrictsFilesystem(t *testing.T) {
	requireSandboxExec(t)
	rwDir := t.TempDir()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), seatbeltHelperEnv+"=1", seatbeltHelperRWDirEnv+"="+rwDir)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper subprocess output: %s", output)

	out := string(output)
	require.Contains(t, out, "ENTER_OK")
	require.Contains(t, out, "ALLOWED_WRITE_OK")
	require.Contains(t, out, "DENIED_WRITE_BLOCKED")
	require.Contains(t, out, "DEVNULL_WRITE_OK")
	require.Contains(t, out, "READONLY_READ_OK")
	require.Contains(t, out, "CHILD_DENIED_WRITE_BLOCKED")
}

// TestSeatbeltSandbox_EnterSandbox_FileGrantDoesNotCoverSiblings is
// the regression test for the vulnerability where a permission rule
// approving edits to a single literal file ended up granting write
// access to every file in its parent directory once fed into an
// OS-level sandbox. It exercises the real Seatbelt enforcement end to
// end, in a subprocess: granting ReadWriteFiles for exactly one file
// must still let it write that file, but must block writing to a
// different, pre-existing file in the same directory.
func TestSeatbeltSandbox_EnterSandbox_FileGrantDoesNotCoverSiblings(t *testing.T) {
	requireSandboxExec(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.txt"), []byte("secret"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("sibling"), 0o644))

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), seatbeltFileGrantHelperEnv+"=1", seatbeltFileGrantHelperDirEnv+"="+dir)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper subprocess output: %s", output)

	out := string(output)
	require.Contains(t, out, "KEY_WRITE_OK")
	require.Contains(t, out, "OTHER_WRITE_BLOCKED")
}
