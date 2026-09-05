package sandbox

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

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
