package profiling

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"time"
)

// Capture writes profile p of the calling process to w, in the binary format
// read by `go tool pprof` (or `go tool trace` for the execution trace). A
// timed profile records for the given number of seconds and stops early when
// ctx is cancelled. A snapshot is taken immediately and ignores seconds.
func Capture(ctx context.Context, w io.Writer, p Profile, seconds int) error {
	if p.Timed {
		if seconds <= 0 {
			return fmt.Errorf("seconds must be positive, got %d", seconds)
		}
		switch p.Name {
		case "cpu":
			return record(ctx, seconds, func() error { return pprof.StartCPUProfile(w) }, pprof.StopCPUProfile)
		case "trace":
			return record(ctx, seconds, func() error { return trace.Start(w) }, trace.Stop)
		}
		return fmt.Errorf("unknown profile %q", p.Name)
	}

	snapshot := pprof.Lookup(p.Name)
	if snapshot == nil {
		return fmt.Errorf("unknown profile %q", p.Name)
	}
	return snapshot.WriteTo(w, 0)
}

// CaptureToFile is Capture into a new file at path, creating missing parent
// directories. It never overwrites an existing file, and removes the file
// again if the capture fails so no truncated profile is left behind.
func CaptureToFile(ctx context.Context, path string, p Profile, seconds int) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	return Capture(ctx, f, p, seconds)
}

// record keeps a process-wide recorder running for the given duration. The
// recorder keeps writing to its destination until stop returns, so stop must
// run on every path out.
func record(ctx context.Context, seconds int, start func() error, stop func()) error {
	if err := start(); err != nil {
		return err
	}
	defer stop()

	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
