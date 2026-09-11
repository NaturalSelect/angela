//go:build !darwin

package sandbox

// relaunchUnderSeatbelt exists only so SeatbeltSandbox.EnterSandbox
// compiles on every platform. New only ever returns a SeatbeltSandbox
// on darwin, so this is never reached in normal operation.
func relaunchUnderSeatbelt(_, _ string) error {
	return ErrNotSupported
}
