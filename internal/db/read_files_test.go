package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordFileRead_And_GetFileRead(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sess.ID, Path: "a.go"}))

	got, err := q.GetFileRead(t.Context(), GetFileReadParams{SessionID: sess.ID, Path: "a.go"})
	require.NoError(t, err)
	require.Equal(t, sess.ID, got.SessionID)
	require.Equal(t, "a.go", got.Path)
	require.Positive(t, got.ReadAt)
}

func TestGetFileRead_NotFound(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	_, err := q.GetFileRead(t.Context(), GetFileReadParams{SessionID: sess.ID, Path: "never-read.go"})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// TestRecordFileRead_UpsertsRatherThanDuplicates pins an old read_at
// via raw SQL, then re-records the same (session, path) pair through
// the real RecordFileRead method and asserts the row was updated in
// place rather than duplicated.
func TestRecordFileRead_UpsertsRatherThanDuplicates(t *testing.T) {
	t.Parallel()

	q, conn := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sess.ID, Path: "a.go"}))

	_, err := conn.ExecContext(t.Context(),
		"UPDATE read_files SET read_at = ? WHERE session_id = ? AND path = ?", 100, sess.ID, "a.go")
	require.NoError(t, err)

	require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sess.ID, Path: "a.go"}))

	got, err := q.GetFileRead(t.Context(), GetFileReadParams{SessionID: sess.ID, Path: "a.go"})
	require.NoError(t, err)
	require.Greater(t, got.ReadAt, int64(100), "re-recording must refresh read_at")

	all, err := q.ListSessionReadFiles(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, all, 1, "re-recording must update the existing row, not insert a duplicate")
}

func TestListSessionReadFiles(t *testing.T) {
	t.Parallel()

	t.Run("returns only reads for the requested session", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		sessA := mustCreateSession(t, q, "sess-a")
		sessB := mustCreateSession(t, q, "sess-b")

		require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sessA.ID, Path: "a.go"}))
		require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sessA.ID, Path: "b.go"}))
		require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sessB.ID, Path: "c.go"}))

		got, err := q.ListSessionReadFiles(t.Context(), sessA.ID)
		require.NoError(t, err)

		paths := make([]string, len(got))
		for i, r := range got {
			paths[i] = r.Path
		}
		require.ElementsMatch(t, []string{"a.go", "b.go"}, paths)
	})

	t.Run("orders by read_at descending", func(t *testing.T) {
		t.Parallel()

		q, conn := newTestQueries(t)
		sess := mustCreateSession(t, q, "sess-1")

		require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sess.ID, Path: "old.go"}))
		require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sess.ID, Path: "new.go"}))

		// Pin distinct read_at values: both rows may otherwise land in
		// the same wall-clock second, making the DESC order ambiguous.
		_, err := conn.ExecContext(t.Context(), "UPDATE read_files SET read_at = ? WHERE session_id = ? AND path = ?", 1000, sess.ID, "old.go")
		require.NoError(t, err)
		_, err = conn.ExecContext(t.Context(), "UPDATE read_files SET read_at = ? WHERE session_id = ? AND path = ?", 2000, sess.ID, "new.go")
		require.NoError(t, err)

		got, err := q.ListSessionReadFiles(t.Context(), sess.ID)
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, "new.go", got[0].Path)
		require.Equal(t, "old.go", got[1].Path)
	})
}

func TestDeleteSession_CascadesToReadFiles(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: sess.ID, Path: "a.go"}))

	require.NoError(t, q.DeleteSession(t.Context(), sess.ID))

	_, err := q.GetFileRead(t.Context(), GetFileReadParams{SessionID: sess.ID, Path: "a.go"})
	require.ErrorIs(t, err, sql.ErrNoRows)
}
