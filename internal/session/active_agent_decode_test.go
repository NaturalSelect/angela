package session

import (
	"database/sql"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/stretchr/testify/require"
)

// newActiveAgentTestService builds a session service over a real
// SQLite file, and hands back the connection so a test can write a
// column value the service itself would never produce.
func newActiveAgentTestService(t *testing.T) (Service, *sql.DB) {
	t.Helper()

	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	return NewService(db.New(conn), conn), conn
}

// corruptActiveAgent writes a blob the decoder cannot read, standing in
// for a truncated write, a downgrade, or a hand-edited database.
func corruptActiveAgent(t *testing.T, conn *sql.DB, sessionID string) {
	t.Helper()
	_, err := conn.ExecContext(t.Context(),
		"UPDATE sessions SET active_agent = ? WHERE id = ?", `{"agent":`, sessionID)
	require.NoError(t, err)
}

// TestACorruptActiveAgentIsNotAFreshSession is A4. An undecodable blob
// used to be logged and dropped, leaving the zero state — which reads
// downstream as "this session never picked anything" and quietly moved
// it onto the default agent, model and provider. The failure has to
// reach the caller instead.
func TestACorruptActiveAgentIsNotAFreshSession(t *testing.T) {
	sessions, conn := newActiveAgentTestService(t)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	require.NoError(t, sessions.UpdateActiveAgent(t.Context(), created.ID, config.ActiveAgentState{
		Agent:     "reviewer",
		ModelName: config.ModelMain,
		Model:     config.SelectedModel{Provider: "mock", Model: "large-model"},
	}))

	corruptActiveAgent(t, conn, created.ID)

	_, err = sessions.Get(t.Context(), created.ID)
	require.Error(t, err, "a session whose agent cannot be decoded must not load as a default one")
	require.Contains(t, err.Error(), created.ID,
		"the error must name the session so it can be repaired")

	_, err = sessions.GetLast(t.Context())
	require.Error(t, err)

	_, err = sessions.List(t.Context())
	require.Error(t, err)
}

// TestAnEmptyActiveAgentIsAFreshSession is the other half: absent is
// not corrupt. A session that has never picked anything must still load
// cleanly, or every session would fail closed on its first turn.
func TestAnEmptyActiveAgentIsAFreshSession(t *testing.T) {
	sessions, _ := newActiveAgentTestService(t)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, fetched.ActiveAgent.IsZero(),
		"a session that picked nothing carries no delta")

	listed, err := sessions.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
}

// TestAStoredActiveAgentRoundTrips guards the happy path the two tests
// above bracket: a decodable delta must survive the write and the read
// unchanged, or "fail closed on corruption" would be indistinguishable
// from "fail closed on everything".
func TestAStoredActiveAgentRoundTrips(t *testing.T) {
	sessions, _ := newActiveAgentTestService(t)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)

	want := config.ActiveAgentState{
		Agent:     "reviewer",
		ModelName: config.ModelMain,
		Model:     config.SelectedModel{Provider: "mock", Model: "large-model"},
	}
	require.NoError(t, sessions.UpdateActiveAgent(t.Context(), created.ID, want))

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, want, fetched.ActiveAgent)
}

// TestDeletingASessionWithACorruptAgentStillAnnounces pins the one
// caller that must tolerate the bad blob. The row is already gone, so
// what it used to run is moot — refusing to publish the deletion would
// leave every listener caching a session that no longer exists.
func TestDeletingASessionWithACorruptAgentStillAnnounces(t *testing.T) {
	sessions, conn := newActiveAgentTestService(t)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	corruptActiveAgent(t, conn, created.ID)

	events := sessions.Subscribe(t.Context())
	require.NoError(t, sessions.Delete(t.Context(), created.ID))

	select {
	case ev := <-events:
		require.Equal(t, created.ID, ev.Payload.ID,
			"the deletion must carry the identity subscribers evict on")
	default:
		t.Fatal("deleting a session with a corrupt agent published nothing")
	}
}
