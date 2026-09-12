//go:build !darwin

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSeatbeltSandbox_EnterSandbox_NotSupportedOffDarwin exercises
// EnterSandbox's full path, including resolving cfg and building a
// profile, on platforms where applySeatbeltProfile's !darwin stub
// always fails: safe to run in the normal, shared test binary. See
// seatbelt_darwin_test.go for the real, enforced restriction. Not
// parallel: it forces the shared entered package var false for the
// duration of the call (a real EnterSandbox call earlier in this
// binary would otherwise make this a silent no-op) and restores it
// afterward.
func TestSeatbeltSandbox_EnterSandbox_NotSupportedOffDarwin(t *testing.T) {
	orig := entered.Swap(false)
	t.Cleanup(func() { entered.Store(orig) })

	err := (SeatbeltSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}, AllowNetwork: true})
	require.ErrorIs(t, err, ErrNotSupported)
}
