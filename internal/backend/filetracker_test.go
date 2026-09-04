package backend

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackendFileTracker_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	tests := []struct {
		name string
		call func(t *testing.T) error
	}{
		{"FileTrackerRecordRead", func(t *testing.T) error {
			return b.FileTrackerRecordRead(t.Context(), "nope", "s1", "/tmp/a.go")
		}},
		{"FileTrackerLastReadTime", func(t *testing.T) error {
			_, err := b.FileTrackerLastReadTime(t.Context(), "nope", "s1", "/tmp/a.go")
			return err
		}},
		{"FileTrackerListReadFiles", func(t *testing.T) error {
			_, err := b.FileTrackerListReadFiles(t.Context(), "nope", "s1")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.call(t), ErrWorkspaceNotFound)
		})
	}
}

// TestBackendFileTracker_RecordAndList drives a real filetracker.Service
// through the backend wrapper and checks the recorded reads are
// actually observable afterward, not just that the calls returned no
// error.
func TestBackendFileTracker_RecordAndList(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sess, err := b.CreateSession(t.Context(), ws.ID, "s")
	require.NoError(t, err)

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.go")
	pathB := filepath.Join(dir, "b.go")

	before, err := b.FileTrackerLastReadTime(t.Context(), ws.ID, sess.ID, pathA)
	require.NoError(t, err)
	require.True(t, before.IsZero(), "no read recorded yet")

	require.NoError(t, b.FileTrackerRecordRead(t.Context(), ws.ID, sess.ID, pathA))
	require.NoError(t, b.FileTrackerRecordRead(t.Context(), ws.ID, sess.ID, pathB))

	after, err := b.FileTrackerLastReadTime(t.Context(), ws.ID, sess.ID, pathA)
	require.NoError(t, err)
	require.False(t, after.IsZero())

	files, err := b.FileTrackerListReadFiles(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{pathA, pathB}, files)
}
