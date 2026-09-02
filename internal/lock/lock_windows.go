//go:build windows

package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// retrySleep is the interval between non-blocking lock retries in the
// blocking File path.
const retrySleep = 100 * time.Millisecond

// lockRegionOffset is the byte offset of the single-byte region locked
// via LockFileEx/UnlockFileEx. Unlike POSIX flock, Windows byte-range
// locks are mandatory: any other handle to the same file — even
// another handle opened by this same process — is rejected if it
// touches a locked byte range, not just other lock-aware callers.
// Locking one byte far past any realistic file content (rather than
// the whole file, as a naive port of the flock semantics would do)
// preserves mutual exclusion while leaving actual file content, such
// as the data-dir lock's owner-info payload, freely readable and
// writable through separate handles while the lock is held. Locking
// beyond the current end of file is valid on Windows and does not
// require the file to actually be that large.
const lockRegionOffset = 1 << 30

func lockFile(ctx context.Context, f *os.File) (func(), error) {
	h := windows.Handle(f.Fd())
	for {
		ol := &windows.Overlapped{Offset: lockRegionOffset}
		flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
		err := windows.LockFileEx(h, flags, 0, 1, 0, ol)
		if err == nil {
			return func() {
				ol := &windows.Overlapped{Offset: lockRegionOffset}
				_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
			}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("LockFileEx: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire lock: %w", ctx.Err())
		case <-time.After(retrySleep):
		}
	}
}

func tryLockFile(f *os.File) (func(), error) {
	h := windows.Handle(f.Fd())
	ol := &windows.Overlapped{Offset: lockRegionOffset}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(h, flags, 0, 1, 0, ol); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrContended
		}
		return nil, fmt.Errorf("LockFileEx: %w", err)
	}
	return func() {
		ol := &windows.Overlapped{Offset: lockRegionOffset}
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
	}, nil
}
