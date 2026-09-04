package db

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateFile(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	got, err := q.CreateFile(t.Context(), CreateFileParams{
		ID: "file-1", SessionID: sess.ID, Path: "main.go", Content: "package main", Version: 0,
	})
	require.NoError(t, err)
	require.Equal(t, "file-1", got.ID)
	require.Equal(t, sess.ID, got.SessionID)
	require.Equal(t, "main.go", got.Path)
	require.Equal(t, "package main", got.Content)
	require.Zero(t, got.Version)
	require.Positive(t, got.CreatedAt)
	require.Equal(t, got.CreatedAt, got.UpdatedAt)
}

func TestCreateFile_RejectsInvalidRows(t *testing.T) {
	t.Parallel()

	t.Run("unknown session violates the foreign key", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		_, err := q.CreateFile(t.Context(), CreateFileParams{
			ID: "file-orphan", SessionID: "does-not-exist", Path: "a.go", Content: "x", Version: 0,
		})
		require.Error(t, err)
	})

	t.Run("duplicate path/session/version violates the unique constraint", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		sess := mustCreateSession(t, q, "sess-1")
		_, err := q.CreateFile(t.Context(), CreateFileParams{ID: "f1", SessionID: sess.ID, Path: "a.go", Content: "x", Version: 0})
		require.NoError(t, err)

		_, err = q.CreateFile(t.Context(), CreateFileParams{ID: "f2", SessionID: sess.ID, Path: "a.go", Content: "y", Version: 0})
		require.Error(t, err)
	})
}

func TestGetFile(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	created, err := q.CreateFile(t.Context(), CreateFileParams{ID: "file-1", SessionID: sess.ID, Path: "a.go", Content: "x", Version: 0})
	require.NoError(t, err)

	t.Run("found", func(t *testing.T) {
		t.Parallel()

		got, err := q.GetFile(t.Context(), created.ID)
		require.NoError(t, err)
		require.Equal(t, created, got)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, err := q.GetFile(t.Context(), "does-not-exist")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

func TestGetFileByPathAndSession(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	for v := int64(0); v <= 2; v++ {
		_, err := q.CreateFile(t.Context(), CreateFileParams{
			ID: idFor(v), SessionID: sess.ID, Path: "a.go", Content: "x", Version: v,
		})
		require.NoError(t, err)
	}

	t.Run("returns the highest version", func(t *testing.T) {
		t.Parallel()

		got, err := q.GetFileByPathAndSession(t.Context(), GetFileByPathAndSessionParams{Path: "a.go", SessionID: sess.ID})
		require.NoError(t, err)
		require.EqualValues(t, 2, got.Version)
	})

	t.Run("not found for a different path", func(t *testing.T) {
		t.Parallel()

		_, err := q.GetFileByPathAndSession(t.Context(), GetFileByPathAndSessionParams{Path: "missing.go", SessionID: sess.ID})
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

func idFor(v int64) string {
	return fmt.Sprintf("file-v%d", v)
}

func TestListFilesByPath(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sessA := mustCreateSession(t, q, "sess-a")
	sessB := mustCreateSession(t, q, "sess-b")

	_, err := q.CreateFile(t.Context(), CreateFileParams{ID: "f-a-0", SessionID: sessA.ID, Path: "shared.go", Content: "x", Version: 0})
	require.NoError(t, err)
	_, err = q.CreateFile(t.Context(), CreateFileParams{ID: "f-a-1", SessionID: sessA.ID, Path: "shared.go", Content: "y", Version: 1})
	require.NoError(t, err)
	_, err = q.CreateFile(t.Context(), CreateFileParams{ID: "f-b-0", SessionID: sessB.ID, Path: "shared.go", Content: "z", Version: 0})
	require.NoError(t, err)
	_, err = q.CreateFile(t.Context(), CreateFileParams{ID: "f-other", SessionID: sessA.ID, Path: "other.go", Content: "w", Version: 0})
	require.NoError(t, err)

	got, err := q.ListFilesByPath(t.Context(), "shared.go")
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.EqualValues(t, 1, got[0].Version, "must be ordered by version DESC first")
}

func TestListFilesBySession(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	_, err := q.CreateFile(t.Context(), CreateFileParams{ID: "f-1", SessionID: sess.ID, Path: "a.go", Content: "x", Version: 1})
	require.NoError(t, err)
	_, err = q.CreateFile(t.Context(), CreateFileParams{ID: "f-0", SessionID: sess.ID, Path: "b.go", Content: "y", Version: 0})
	require.NoError(t, err)

	got, err := q.ListFilesBySession(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.EqualValues(t, 0, got[0].Version, "must be ordered by version ASC first")
	require.EqualValues(t, 1, got[1].Version)
}

func TestListLatestSessionFiles(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")

	// Two paths, several versions each; only the highest version per
	// path should come back.
	for v := int64(0); v <= 2; v++ {
		_, err := q.CreateFile(t.Context(), CreateFileParams{ID: "a-" + idFor(v), SessionID: sess.ID, Path: "a.go", Content: "x", Version: v})
		require.NoError(t, err)
	}
	for v := int64(0); v <= 1; v++ {
		_, err := q.CreateFile(t.Context(), CreateFileParams{ID: "b-" + idFor(v), SessionID: sess.ID, Path: "b.go", Content: "y", Version: v})
		require.NoError(t, err)
	}

	got, err := q.ListLatestSessionFiles(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)

	byPath := map[string]File{}
	for _, f := range got {
		byPath[f.Path] = f
	}
	require.EqualValues(t, 2, byPath["a.go"].Version)
	require.EqualValues(t, 1, byPath["b.go"].Version)
}

// TestListNewFiles documents real, current behavior: the query
// selects files.is_new, a column no migration ever creates. Nothing
// in the codebase calls ListNewFiles, so this pre-existing schema/
// query mismatch has never surfaced. This test pins down the actual
// (erroring) behavior rather than an aspirational one.
func TestListNewFiles(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	_, err := q.ListNewFiles(t.Context())
	require.ErrorContains(t, err, "is_new")
}

func TestDeleteFile(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	created, err := q.CreateFile(t.Context(), CreateFileParams{ID: "f-1", SessionID: sess.ID, Path: "a.go", Content: "x", Version: 0})
	require.NoError(t, err)

	require.NoError(t, q.DeleteFile(t.Context(), created.ID))

	_, err = q.GetFile(t.Context(), created.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestDeleteSessionFiles(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	_, err := q.CreateFile(t.Context(), CreateFileParams{ID: "f-1", SessionID: sess.ID, Path: "a.go", Content: "x", Version: 0})
	require.NoError(t, err)
	_, err = q.CreateFile(t.Context(), CreateFileParams{ID: "f-2", SessionID: sess.ID, Path: "b.go", Content: "y", Version: 0})
	require.NoError(t, err)

	require.NoError(t, q.DeleteSessionFiles(t.Context(), sess.ID))

	got, err := q.ListFilesBySession(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}
