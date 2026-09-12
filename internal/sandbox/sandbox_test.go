package sandbox

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig("/work", "/data", "/global")
	require.Equal(t, []string{"/work", "/data", "/global", os.TempDir()}, cfg.ReadWrite)
	require.Equal(t, []string{"/"}, cfg.ReadOnly)
	require.True(t, cfg.AllowNetwork)
}

func TestDefaultConfig_SkipsEmptyDataDir(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig("/work", "", "/global")
	require.Equal(t, []string{"/work", "/global", os.TempDir()}, cfg.ReadWrite)
}

func TestDefaultConfig_DedupesRepeatedPaths(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig("/same", "/same", "/same")
	require.Equal(t, []string{"/same", os.TempDir()}, cfg.ReadWrite)
}

func TestNoneSandbox(t *testing.T) {
	t.Parallel()

	var s Sandbox = NoneSandbox{}
	require.False(t, s.IsInSandbox())
	require.ErrorIs(t, s.EnterSandbox(Config{ReadWrite: []string{"/tmp"}}), ErrNotSupported)
}

func TestNew_Other(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("Skipping test on Linux/Darwin")
	}
	t.Parallel()

	require.IsType(t, NoneSandbox{}, New(false))
}

func TestNew_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping test on non-Darwin")
	}
	t.Parallel()

	require.IsType(t, SeatbeltSandbox{}, New(false))
	require.IsType(t, SeatbeltSandbox{}, New(true))
}

func TestNew_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	t.Parallel()

	got := New(false)
	if InDocker() {
		require.IsType(t, DockerSandbox{}, got)
	} else {
		require.IsType(t, LandlockSandbox{}, got)
	}
}

// TestDockerSandbox verifies that DockerSandbox.EnterSandbox is a
// true no-op: the container is trusted to already provide the
// network isolation its operator wants, so unlike LandlockSandbox,
// AllowNetwork false must not mark children for restriction. Not
// parallel: it mutates the shared restrictChildNetwork package var,
// saving and restoring it so it doesn't leak into other tests.
func TestDockerSandbox(t *testing.T) {
	orig := restrictChildNetwork.Load()
	t.Cleanup(func() { restrictChildNetwork.Store(orig) })
	restrictChildNetwork.Store(false)

	var s Sandbox = DockerSandbox{}
	require.True(t, s.IsInSandbox())
	require.NoError(t, s.EnterSandbox(Config{ReadOnly: []string{"/"}, AllowNetwork: false}))
	require.False(t, ShouldRestrictChildNetwork(), "DockerSandbox must trust the container's own network configuration")
}

// TestNew_Linux_InDocker covers the DockerSandbox branch of New()
// deterministically by overriding the InDocker package var, rather
// than depending on whether the test happens to run inside a
// container. Not parallel: it mutates package state that TestNew_Linux
// also reads, and its cleanup must restore InDocker before any
// parallel test resumes past its own t.Parallel() call.
func TestNew_Linux_InDocker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	orig := InDocker
	InDocker = func() bool { return true }
	t.Cleanup(func() { InDocker = orig })

	require.IsType(t, DockerSandbox{}, New(false))
}

// TestNew_Linux_InDocker_NoDockerSandboxOverride covers the
// noDockerSandbox=true branch of New(): even with InDocker forced
// true, it must skip the DockerSandbox passthrough and fall back to
// LandlockSandbox, since noDockerSandbox disables the Docker/OCI
// shortcut. Not parallel for the same reason as TestNew_Linux_InDocker:
// it mutates the shared InDocker package var.
func TestNew_Linux_InDocker_NoDockerSandboxOverride(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	orig := InDocker
	InDocker = func() bool { return true }
	t.Cleanup(func() { InDocker = orig })

	require.IsType(t, LandlockSandbox{}, New(true))
}

// TestLandlockSandbox_IsInSandbox_PreEntry pins the natural pre-entry
// state. Not parallel, and declared before
// TestLandlockSandbox_EnterSandbox_Real: entered is process-global
// and irreversible once set, so this must run first.
func TestLandlockSandbox_IsInSandbox_PreEntry(t *testing.T) {
	var s Sandbox = LandlockSandbox{}
	require.False(t, s.IsInSandbox())
}

// TestLandlockSandbox_EnterSandbox_Real is the one real, in-process
// EnterSandbox call in this shared test binary: entered is
// process-global and one-shot across both LandlockSandbox and
// SeatbeltSandbox (see sandbox.go), so every other test that needs to
// observe real filesystem enforcement runs a disposable subprocess
// instead (see sandbox_linux_test.go). It grants ReadWrite on "/",
// which was verified in isolation to leave the process able to read,
// write, and dial out normally afterward, so unlike a narrower
// config it cannot regress any test that runs later in this shared
// binary. resolve's own field-building logic (which paths land in
// which ruleSet field) is covered exhaustively in profile_test.go;
// this only needs to prove EnterSandbox itself wires a resolved
// ruleSet into real landlock.Rule values and into
// ShouldRestrictChildNetwork without error. Not parallel, and
// declared after TestLandlockSandbox_IsInSandbox_PreEntry.
func TestLandlockSandbox_EnterSandbox_Real(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	err := (LandlockSandbox{}).EnterSandbox(Config{
		ReadOnly:     []string{"/"},
		ReadWrite:    []string{"/"},
		AllowNetwork: false,
	})
	require.NoError(t, err)
	require.True(t, (LandlockSandbox{}).IsInSandbox())
	require.True(t, ShouldRestrictChildNetwork(), "AllowNetwork false must mark children for network restriction")
}

// TestSandbox_EnterSandbox_AlreadyEnteredIsNoop verifies a second
// EnterSandbox call, once entered is already set (by an earlier real
// call, on either backend), is a no-op on both LandlockSandbox and
// SeatbeltSandbox rather than attempting to restrict the process
// again, which could error, or, on macOS, is rejected by the kernel
// outright for an already-sandboxed process. Not parallel: it
// mutates the shared entered package var, saving and restoring it so
// it doesn't leak into other tests.
func TestSandbox_EnterSandbox_AlreadyEnteredIsNoop(t *testing.T) {
	orig := entered.Swap(true)
	t.Cleanup(func() { entered.Store(orig) })

	require.NoError(t, (LandlockSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}}))
	require.NoError(t, (SeatbeltSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}}))
	require.True(t, (LandlockSandbox{}).IsInSandbox())
	require.True(t, (SeatbeltSandbox{}).IsInSandbox())
}
