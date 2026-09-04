package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestQueries opens a fresh, migrated SQLite database under a
// per-test temp directory and returns both the generated Queries API
// and the underlying connection. The raw connection is exposed for
// fixture setup the generated API cannot express, such as pinning a
// row's created_at to a known instant. Cleanup releases just this
// data dir's pool entry, so callers are safe to run under t.Parallel.
func newTestQueries(t *testing.T) (*Queries, *sql.DB) {
	t.Helper()

	dataDir := t.TempDir()
	conn, err := Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, Release(dataDir)) })

	return New(conn), conn
}

// setCreatedAt overwrites a row's created_at column directly so tests
// over time-bucketed aggregates assert against known instants instead
// of depending on wall-clock timing or second-resolution ties.
func setCreatedAt(t *testing.T, conn *sql.DB, table, id string, ts int64) {
	t.Helper()

	_, err := conn.ExecContext(t.Context(), "UPDATE "+table+" SET created_at = ? WHERE id = ?", ts, id)
	require.NoError(t, err)
}

// toFloat64 mirrors the driver-agnostic numeric coercion the stats
// command applies to the interface{}-typed COALESCE/AVG columns
// returned by this package's aggregate queries.
func toFloat64(t *testing.T, v any) float64 {
	t.Helper()

	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		t.Fatalf("unexpected numeric type %T (%v)", v, v)
		return 0
	}
}
