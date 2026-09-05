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

func TestNew_NonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Skipping test on Linux")
	}
	t.Parallel()

	require.IsType(t, NoneSandbox{}, New())
}

func TestNew_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	t.Parallel()

	got := New()
	if InDocker() {
		require.IsType(t, DockerSandbox{}, got)
	} else {
		require.IsType(t, LandlockSandbox{}, got)
	}
}

func TestDockerSandbox(t *testing.T) {
	t.Parallel()

	var s Sandbox = DockerSandbox{}
	require.True(t, s.IsInSandbox())
	require.NoError(t, s.EnterSandbox(Config{ReadOnly: []string{"/"}, AllowNetwork: false}))
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

	require.IsType(t, DockerSandbox{}, New())
}

// TestLandlockSandbox_IsInSandbox_PreEntry pins the natural pre-entry
// state. Not parallel, and declared before
// TestLandlockSandbox_EnterSandbox_RuleBuilding: entered is
// process-global and irreversible once set, so this must run first.
func TestLandlockSandbox_IsInSandbox_PreEntry(t *testing.T) {
	var s Sandbox = LandlockSandbox{}
	require.False(t, s.IsInSandbox())
}

// TestLandlockSandbox_EnterSandbox_RuleBuilding exercises the real
// rule-building branches of EnterSandbox. Landlock confinement is
// process-global and irreversible, so every case grants ReadWrite on
// "/", which was verified in isolation to leave the process able to
// read, write, and dial out normally afterward. Unlike a narrower
// config, it cannot regress any test that runs later in this shared
// binary. Not parallel, and declared after
// TestLandlockSandbox_IsInSandbox_PreEntry.
func TestLandlockSandbox_EnterSandbox_RuleBuilding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	tests := []struct {
		name         string
		readOnly     []string
		allowNetwork bool
	}{
		{"read-only and read-write rules, network allowed", []string{"/"}, true},
		{"no read-only rule, network restricted", nil, false},
	}
	for _, tt := range tests {
		err := (LandlockSandbox{}).EnterSandbox(Config{
			ReadOnly:     tt.readOnly,
			ReadWrite:    []string{"/"},
			AllowNetwork: tt.allowNetwork,
		})
		require.NoError(t, err, tt.name)
	}
}
