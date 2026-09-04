package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrepare documents Prepare's real behavior against several kinds
// of DBTX. Note the "migrated schema" case: ListNewFiles's query
// selects files.is_new, a column no migration ever creates, so
// Prepare always fails partway through preparing the real, migrated
// schema. Nothing in the codebase calls db.Prepare or
// db.Queries.ListNewFiles, so this pre-existing bug has never
// surfaced; this test pins down the actual behavior rather than an
// aspirational one. The remaining subtests each break a different
// table so Prepare fails at a different query, exercising more of
// its otherwise near-identical per-query error branches.
func TestPrepare(t *testing.T) {
	t.Parallel()

	t.Run("migrated schema fails on the ListNewFiles/is_new mismatch", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)

		q, err := Prepare(t.Context(), conn)
		require.Nil(t, q)
		require.ErrorContains(t, err, "ListNewFiles")
		require.ErrorContains(t, err, "is_new")
	})

	t.Run("unmigrated schema fails on the very first query", func(t *testing.T) {
		t.Parallel()

		raw, err := openDB(filepath.Join(t.TempDir(), "raw.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = raw.Close() })

		q, err := Prepare(t.Context(), raw)
		require.Nil(t, q)
		require.ErrorContains(t, err, "AddSessionCost")
	})

	t.Run("closed connection fails cleanly", func(t *testing.T) {
		t.Parallel()

		raw, err := openDB(filepath.Join(t.TempDir(), "raw.db"))
		require.NoError(t, err)
		require.NoError(t, raw.Close())

		q, err := Prepare(t.Context(), raw)
		require.Nil(t, q)
		require.Error(t, err)
	})

	t.Run("missing messages table fails on the first query needing it", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)
		_, err := conn.ExecContext(t.Context(), "DROP TABLE messages")
		require.NoError(t, err)

		q, err := Prepare(t.Context(), conn)
		require.Nil(t, q)
		require.ErrorContains(t, err, "CreateMessage")
	})

	t.Run("missing read_files table fails on the first query needing it", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)
		_, err := conn.ExecContext(t.Context(), "DROP TABLE read_files")
		require.NoError(t, err)

		q, err := Prepare(t.Context(), conn)
		require.Nil(t, q)
		require.ErrorContains(t, err, "GetFileRead")
	})

	t.Run("missing files table fails on the first query needing it", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)
		_, err := conn.ExecContext(t.Context(), "DROP TABLE files")
		require.NoError(t, err)

		q, err := Prepare(t.Context(), conn)
		require.Nil(t, q)
		require.ErrorContains(t, err, "CreateFile")
	})

	t.Run("missing parent_session_id column fails on the first query needing it", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)
		_, err := conn.ExecContext(t.Context(), "ALTER TABLE sessions DROP COLUMN parent_session_id")
		require.NoError(t, err)

		q, err := Prepare(t.Context(), conn)
		require.Nil(t, q)
		require.ErrorContains(t, err, "CreateSession")
	})
}

func TestQueriesClose(t *testing.T) {
	t.Parallel()

	t.Run("no prepared statements is a no-op", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		require.NoError(t, q.Close())
	})

	t.Run("closes real prepared statements and they become unusable", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)

		stmt1, err := conn.PrepareContext(t.Context(), addSessionCost)
		require.NoError(t, err)
		stmt2, err := conn.PrepareContext(t.Context(), getSessionByID)
		require.NoError(t, err)

		q := &Queries{db: conn, addSessionCostStmt: stmt1, getSessionByIDStmt: stmt2}
		require.NoError(t, q.Close())

		_, err = stmt1.ExecContext(t.Context(), 1.0, "missing-session")
		require.Error(t, err, "statement must be unusable once Close has run")
	})

	t.Run("closes every statement once Prepare has populated all of them", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)

		// ListNewFiles references files.is_new, a column no migration
		// creates (see TestListNewFiles), so Prepare cannot otherwise
		// succeed against a real, unmodified schema. Patching it in here
		// is pure test plumbing to get every statement genuinely
		// prepared, so Close's "close whichever fields are set" loop
		// runs across all of them instead of just a couple.
		_, err := conn.ExecContext(t.Context(), "ALTER TABLE files ADD COLUMN is_new INTEGER DEFAULT 0")
		require.NoError(t, err)

		q, err := Prepare(t.Context(), conn)
		require.NoError(t, err)

		require.NoError(t, q.Close())
	})
}

// TestQueriesRouting_PreparedWithoutTx exercises the exec/query/
// queryRow branch used when a statement is prepared but the Queries
// is not bound to a transaction. Ordinary CRUD tests only exercise
// the unprepared default branch (via New) and the prepared-with-tx
// branch (via WithTx), leaving this middle branch otherwise dark.
func TestQueriesRouting_PreparedWithoutTx(t *testing.T) {
	t.Parallel()

	_, conn := newTestQueries(t)

	getStmt, err := conn.PrepareContext(t.Context(), getSessionByID)
	require.NoError(t, err)
	deleteStmt, err := conn.PrepareContext(t.Context(), deleteSession)
	require.NoError(t, err)
	listStmt, err := conn.PrepareContext(t.Context(), listSessions)
	require.NoError(t, err)

	q := &Queries{
		db:                 conn,
		getSessionByIDStmt: getStmt,
		deleteSessionStmt:  deleteStmt,
		listSessionsStmt:   listStmt,
	}

	unprepared := New(conn)
	_, err = unprepared.CreateSession(t.Context(), CreateSessionParams{ID: "routed", Title: "x"})
	require.NoError(t, err)

	got, err := q.GetSessionByID(t.Context(), "routed")
	require.NoError(t, err)
	require.Equal(t, "routed", got.ID)

	list, err := q.ListSessions(t.Context())
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, q.DeleteSession(t.Context(), "routed"))
	_, err = unprepared.GetSessionByID(t.Context(), "routed")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestQueriesWithTx(t *testing.T) {
	t.Parallel()

	t.Run("commit persists rows written through the tx-bound Queries", func(t *testing.T) {
		t.Parallel()

		q, conn := newTestQueries(t)

		tx, err := conn.BeginTx(t.Context(), nil)
		require.NoError(t, err)

		txQ := q.WithTx(tx)
		created, err := txQ.CreateSession(t.Context(), CreateSessionParams{ID: "tx-commit", Title: "in tx"})
		require.NoError(t, err)
		require.Equal(t, "tx-commit", created.ID)

		require.NoError(t, tx.Commit())

		got, err := q.GetSessionByID(t.Context(), "tx-commit")
		require.NoError(t, err)
		require.Equal(t, "in tx", got.Title)
	})

	t.Run("rollback discards rows written through the tx-bound Queries", func(t *testing.T) {
		t.Parallel()

		q, conn := newTestQueries(t)

		tx, err := conn.BeginTx(t.Context(), nil)
		require.NoError(t, err)

		txQ := q.WithTx(tx)
		_, err = txQ.CreateSession(t.Context(), CreateSessionParams{ID: "tx-rollback", Title: "in tx"})
		require.NoError(t, err)

		require.NoError(t, tx.Rollback())

		_, err = q.GetSessionByID(t.Context(), "tx-rollback")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("routes through a prepared statement rebound to the tx", func(t *testing.T) {
		t.Parallel()

		_, conn := newTestQueries(t)

		stmt, err := conn.PrepareContext(t.Context(), createSession)
		require.NoError(t, err)
		t.Cleanup(func() { _ = stmt.Close() })

		q := &Queries{db: conn, createSessionStmt: stmt}
		tx, err := conn.BeginTx(t.Context(), nil)
		require.NoError(t, err)

		txQ := q.WithTx(tx)
		created, err := txQ.CreateSession(t.Context(), CreateSessionParams{ID: "tx-prepared", Title: "prepared+tx"})
		require.NoError(t, err)
		require.Equal(t, "prepared+tx", created.Title)
		require.NoError(t, tx.Commit())
	})
}
