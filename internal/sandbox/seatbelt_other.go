//go:build !darwin

package sandbox

// applySeatbeltProfile exists only so SeatbeltSandbox.EnterSandbox
// compiles on every platform. New only ever returns a SeatbeltSandbox
// on darwin, so this is never reached in normal operation.
func applySeatbeltProfile(_ string) error {
	return ErrNotSupported
}
