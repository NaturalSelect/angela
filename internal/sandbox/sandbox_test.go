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
