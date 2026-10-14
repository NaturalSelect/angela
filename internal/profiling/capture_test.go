package profiling

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func profileNamed(t *testing.T, name string) Profile {
	t.Helper()
	for _, p := range Profiles {
		if p.Name == name {
			return p
		}
	}
	require.FailNow(t, "no such profile", name)
	return Profile{}
}

func TestCaptureSnapshotProfiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"goroutine", "heap", "allocs", "threadcreate"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			require.NoError(t, Capture(t.Context(), &buf, profileNamed(t, name), 0))
			require.GreaterOrEqual(t, buf.Len(), 2)
			require.Equal(t, []byte{0x1f, 0x8b}, buf.Bytes()[:2], "binary profiles are gzip-compressed protobuf")
		})
	}
}

// NOTE: Not parallel. The CPU profiler and execution tracer are process-wide,
// so tests recording them must not overlap with each other.
func TestCaptureTimedProfilesRecordForTheRequestedDuration(t *testing.T) {
	for _, name := range []string{"cpu", "trace"} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			start := time.Now()
			require.NoError(t, Capture(t.Context(), &buf, profileNamed(t, name), 1))
			require.GreaterOrEqual(t, time.Since(start), time.Second)
			require.NotZero(t, buf.Len())
		})
	}
}

func TestCaptureStopsRecordingWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Capture(ctx, &bytes.Buffer{}, profileNamed(t, "cpu"), 30)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 10*time.Second)

	// The recorder must have been released, or the next recording fails.
	require.NoError(t, Capture(t.Context(), &bytes.Buffer{}, profileNamed(t, "cpu"), 1))
}

func TestCaptureRejectsBadInput(t *testing.T) {
	t.Parallel()

	t.Run("non-positive seconds for a timed profile", func(t *testing.T) {
		t.Parallel()
		err := Capture(t.Context(), &bytes.Buffer{}, profileNamed(t, "cpu"), 0)
		require.ErrorContains(t, err, "seconds must be positive")
	})
	t.Run("unknown snapshot", func(t *testing.T) {
		t.Parallel()
		err := Capture(t.Context(), &bytes.Buffer{}, Profile{Name: "nosuch"}, 0)
		require.ErrorContains(t, err, "unknown profile")
	})
	t.Run("unknown timed profile", func(t *testing.T) {
		t.Parallel()
		err := Capture(t.Context(), &bytes.Buffer{}, Profile{Name: "nosuch", Timed: true}, 1)
		require.ErrorContains(t, err, "unknown profile")
	})
}

func TestCaptureToFileCreatesParentDirectories(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "dir", "heap.pb.gz")
	require.NoError(t, CaptureToFile(t.Context(), path, profileNamed(t, "heap"), 0))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte{0x1f, 0x8b}, data[:2])
}

func TestCaptureToFileNeverOverwrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "heap.pb.gz")
	require.NoError(t, os.WriteFile(path, []byte("precious"), 0o600))

	err := CaptureToFile(t.Context(), path, profileNamed(t, "heap"), 0)
	require.ErrorIs(t, err, os.ErrExist)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "precious", string(data), "an existing file must survive a failed export")
}

func TestCaptureToFileRemovesFileOnFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.pb.gz")
	err := CaptureToFile(t.Context(), path, Profile{Name: "nosuch"}, 0)
	require.Error(t, err)
	require.NoFileExists(t, path, "a failed export must not leave a truncated profile behind")
}

func TestProfileExtensionAndFileName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "pb.gz", profileNamed(t, "heap").Extension())
	require.Equal(t, "pb.gz", profileNamed(t, "cpu").Extension())
	require.Equal(t, "trace", profileNamed(t, "trace").Extension())

	at := time.Date(2026, 10, 15, 8, 9, 10, 0, time.UTC)
	require.Equal(t, "heap-42-20261015-080910.pb.gz", profileNamed(t, "heap").FileName(42, at, "pb.gz"))
}
