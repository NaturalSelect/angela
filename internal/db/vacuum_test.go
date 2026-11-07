package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	require.NoError(t, err)
	return info.Size()
}

func TestVacuum_ReleasesSpaceOfDeletedRows(t *testing.T) {
	dataDir := t.TempDir()
	conn, err := Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, Release(dataDir)) })
	q := New(conn)

	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "s1", Title: "t"})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "keep", Title: "kept"})
	require.NoError(t, err)
	_, err = q.CreateGeneratedImage(t.Context(), CreateGeneratedImageParams{
		ID: "img_1", SessionID: "s1", Prompt: "p", Provider: "p", Model: "m",
		MimeType: "image/png", Data: make([]byte, 4<<20),
	})
	require.NoError(t, err)
	require.NoError(t, q.DeleteSession(t.Context(), "s1"), "the image cascades away with its session")

	dbPath := filepath.Join(dataDir, "angela.db")
	before := fileSize(t, dbPath) + fileSize(t, dbPath+"-wal")
	require.Greater(t, before, int64(4<<20), "the freed pages are still on disk before the vacuum")

	require.NoError(t, Vacuum(t.Context(), conn))

	require.Less(t, fileSize(t, dbPath), int64(1<<20), "the database file shrinks")
	require.Zero(t, fileSize(t, dbPath+"-wal"), "the write-ahead log is emptied")

	sess, err := q.GetSessionByID(t.Context(), "keep")
	require.NoError(t, err)
	require.Equal(t, "kept", sess.Title, "rows that were not deleted survive")
}

func TestVacuum_RestoresMemoryTempStore(t *testing.T) {
	dataDir := t.TempDir()
	conn, err := Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, Release(dataDir)) })

	require.NoError(t, Vacuum(t.Context(), conn))

	var tempStore int
	require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA temp_store").Scan(&tempStore))
	require.Equal(t, 2, tempStore, "temp_store=MEMORY is 2 and must be back after the vacuum")
}
