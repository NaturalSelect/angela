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
// TestLandlockSandbox_IsInSandbox_PreEntry; it also saves and restores
// the shared restrictChildNetwork package var, since its last case
// uses AllowNetwork: false.
func TestLandlockSandbox_EnterSandbox_RuleBuilding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	orig := restrictChildNetwork.Load()
	t.Cleanup(func() { restrictChildNetwork.Store(orig) })

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

// TestLandlockSandbox_EnterSandbox_RestrictChildNetwork verifies
// EnterSandbox's Config.AllowNetwork only ever marks children for
// restriction (via ShouldRestrictChildNetwork), never the calling
// process itself, since RestrictNet was dropped from this path. Not
// parallel: it mutates the shared restrictChildNetwork package var,
// saving and restoring it so it doesn't leak into other tests.
func TestLandlockSandbox_EnterSandbox_RestrictChildNetwork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	orig := restrictChildNetwork.Load()
	t.Cleanup(func() { restrictChildNetwork.Store(orig) })

	restrictChildNetwork.Store(false)
	require.NoError(t, (LandlockSandbox{}).EnterSandbox(Config{ReadWrite: []string{"/"}, AllowNetwork: true}))
	require.False(t, ShouldRestrictChildNetwork(), "AllowNetwork true must not mark children for network restriction")

	require.NoError(t, (LandlockSandbox{}).EnterSandbox(Config{ReadWrite: []string{"/"}, AllowNetwork: false}))
	require.True(t, ShouldRestrictChildNetwork(), "AllowNetwork false must mark children for network restriction")
}
