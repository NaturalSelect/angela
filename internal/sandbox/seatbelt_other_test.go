//go:build !darwin

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSeatbeltSandbox_EnterSandbox_NotSupportedOffDarwin exercises
// EnterSandbox's full path, including building a profile and
// resolving the executable, on platforms where it can never actually
// relaunch: relaunchUnderSeatbelt's !darwin stub always fails, so
// this is safe to run in the normal, shared test binary. See
// seatbelt_darwin_test.go for the real, enforced relaunch.
func TestSeatbeltSandbox_EnterSandbox_NotSupportedOffDarwin(t *testing.T) {
	t.Setenv(seatbeltMarkerEnv, "")

	err := (SeatbeltSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}})
	require.ErrorIs(t, err, ErrNotSupported)
}
