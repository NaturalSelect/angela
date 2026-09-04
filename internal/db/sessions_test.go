package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, q *Queries) CreateSessionParams
		check func(t *testing.T, got Session)
	}{
		{
			name: "minimal fields get sensible defaults",
			setup: func(t *testing.T, q *Queries) CreateSessionParams {
				return CreateSessionParams{ID: "sess-minimal", Title: "untitled"}
			},
			check: func(t *testing.T, got Session) {
				require.Equal(t, "sess-minimal", got.ID)
				require.Equal(t, "untitled", got.Title)
				require.False(t, got.ParentSessionID.Valid)
				require.Zero(t, got.MessageCount)
				require.Zero(t, got.PromptTokens)
				require.Zero(t, got.CompletionTokens)
				require.Zero(t, got.Cost)
				require.False(t, got.SummaryMessageID.Valid, "summary_message_id is always null on create")
				require.False(t, got.Todos.Valid)
				require.False(t, got.Agent.Valid)
				require.False(t, got.ActiveAgent.Valid)
				require.Positive(t, got.CreatedAt)
				require.Equal(t, got.CreatedAt, got.UpdatedAt)
			},
		},
		{
			name: "full fields round-trip including a real parent session",
			setup: func(t *testing.T, q *Queries) CreateSessionParams {
				parent, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-parent", Title: "parent"})
				require.NoError(t, err)
				return CreateSessionParams{
					ID:               "sess-child",
					ParentSessionID:  sql.NullString{String: parent.ID, Valid: true},
					Title:            "child",
					MessageCount:     3,
					PromptTokens:     100,
					CompletionTokens: 50,
					Cost:             1.25,
					Agent:            sql.NullString{String: "coder", Valid: true},
					ActiveAgent:      sql.NullString{String: "task", Valid: true},
				}
			},
			check: func(t *testing.T, got Session) {
				require.Equal(t, "sess-child", got.ID)
				require.True(t, got.ParentSessionID.Valid)
				require.Equal(t, "sess-parent", got.ParentSessionID.String)
				require.EqualValues(t, 3, got.MessageCount)
				require.EqualValues(t, 100, got.PromptTokens)
				require.EqualValues(t, 50, got.CompletionTokens)
				require.InDelta(t, 1.25, got.Cost, 1e-9)
				require.Equal(t, "coder", got.Agent.String)
				require.Equal(t, "task", got.ActiveAgent.String)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			q, _ := newTestQueries(t)
			params := tt.setup(t, q)

			got, err := q.CreateSession(t.Context(), params)
			require.NoError(t, err)
			tt.check(t, got)
		})
	}
}

func TestCreateSession_RejectsInvalidRows(t *testing.T) {
	t.Parallel()

	t.Run("duplicate id violates the primary key", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		_, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "dup", Title: "first"})
		require.NoError(t, err)

		_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "dup", Title: "second"})
		require.Error(t, err)
	})

	t.Run("negative cost violates the check constraint", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		_, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "neg-cost", Title: "x", Cost: -1})
		require.Error(t, err)
	})

	t.Run("negative prompt tokens violates the check constraint", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		_, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "neg-tokens", Title: "x", PromptTokens: -1})
		require.Error(t, err)
	})
}

func TestGetSessionByID(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	created, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-1", Title: "hello"})
	require.NoError(t, err)

	t.Run("found", func(t *testing.T) {
		t.Parallel()

		got, err := q.GetSessionByID(t.Context(), created.ID)
		require.NoError(t, err)
		require.Equal(t, created, got)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, err := q.GetSessionByID(t.Context(), "does-not-exist")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

func TestGetLastSession(t *testing.T) {
	t.Parallel()

	t.Run("no sessions", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		_, err := q.GetLastSession(t.Context())
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("returns the most recently updated session", func(t *testing.T) {
		t.Parallel()

		q, conn := newTestQueries(t)

		// Insert directly with pinned timestamps rather than going
		// through CreateSession: both rows would otherwise land in the
		// same wall-clock second, making the DESC ordering ambiguous,
		// and the update_sessions_updated_at trigger overwrites any
		// updated_at set via a later UPDATE back to the real "now".
		insertSessionWithTimestamps(t, conn, "older", 1000)
		insertSessionWithTimestamps(t, conn, "newer", 2000)

		got, err := q.GetLastSession(t.Context())
		require.NoError(t, err)
		require.Equal(t, "newer", got.ID)
	})
}

func TestListSessions_OnlyReturnsRootSessions(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	_, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "root-1", Title: "root 1"})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{ID: "root-2", Title: "root 2"})
	require.NoError(t, err)
	_, err = q.CreateSession(t.Context(), CreateSessionParams{
		ID:              "child",
		Title:           "child",
		ParentSessionID: sql.NullString{String: "root-1", Valid: true},
	})
	require.NoError(t, err)

	got, err := q.ListSessions(t.Context())
	require.NoError(t, err)

	ids := make([]string, len(got))
	for i, s := range got {
		ids[i] = s.ID
	}
	require.ElementsMatch(t, []string{"root-1", "root-2"}, ids)
}

func TestUpdateSession(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	created, err := q.CreateSession(t.Context(), CreateSessionParams{
		ID:           "sess-update",
		Title:        "before",
		MessageCount: 7,
		Agent:        sql.NullString{String: "coder", Valid: true},
	})
	require.NoError(t, err)

	updated, err := q.UpdateSession(t.Context(), UpdateSessionParams{
		ID:               created.ID,
		Title:            "after",
		PromptTokens:     10,
		CompletionTokens: 20,
		Cost:             0.5,
		SummaryMessageID: sql.NullString{String: "msg-summary", Valid: true},
		Todos:            sql.NullString{String: `[{"content":"x"}]`, Valid: true},
	})
	require.NoError(t, err)

	require.Equal(t, "after", updated.Title)
	require.EqualValues(t, 10, updated.PromptTokens)
	require.EqualValues(t, 20, updated.CompletionTokens)
	require.InDelta(t, 0.5, updated.Cost, 1e-9)
	require.Equal(t, "msg-summary", updated.SummaryMessageID.String)
	require.Equal(t, `[{"content":"x"}]`, updated.Todos.String)

	// Fields outside the SET list must survive untouched.
	require.EqualValues(t, 7, updated.MessageCount)
	require.Equal(t, "coder", updated.Agent.String)
}

func TestUpdateSession_MissingIDIsNotFound(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	_, err := q.UpdateSession(t.Context(), UpdateSessionParams{ID: "missing", Title: "x"})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestRenameSession(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	created, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-rename", Title: "before", Cost: 2})
	require.NoError(t, err)

	require.NoError(t, q.RenameSession(t.Context(), RenameSessionParams{ID: created.ID, Title: "after"}))

	got, err := q.GetSessionByID(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "after", got.Title)
	require.InDelta(t, 2, got.Cost, 1e-9, "rename must not disturb unrelated columns")
}

func TestAddSessionCost(t *testing.T) {
	t.Parallel()

	t.Run("accumulates onto an existing session", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		created, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-cost", Title: "x", Cost: 1})
		require.NoError(t, err)

		rows, err := q.AddSessionCost(t.Context(), AddSessionCostParams{ID: created.ID, Cost: 0.5})
		require.NoError(t, err)
		require.EqualValues(t, 1, rows)

		got, err := q.GetSessionByID(t.Context(), created.ID)
		require.NoError(t, err)
		require.InDelta(t, 1.5, got.Cost, 1e-9)
	})

	t.Run("missing session affects no rows without erroring", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		rows, err := q.AddSessionCost(t.Context(), AddSessionCostParams{ID: "missing", Cost: 1})
		require.NoError(t, err)
		require.Zero(t, rows)
	})
}

func TestUpdateSessionActiveAgent(t *testing.T) {
	t.Parallel()

	t.Run("updates agent fields", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		created, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-agent", Title: "x"})
		require.NoError(t, err)

		rows, err := q.UpdateSessionActiveAgent(t.Context(), UpdateSessionActiveAgentParams{
			ID:          created.ID,
			Agent:       sql.NullString{String: "coder", Valid: true},
			ActiveAgent: sql.NullString{String: "task", Valid: true},
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, rows)

		got, err := q.GetSessionByID(t.Context(), created.ID)
		require.NoError(t, err)
		require.Equal(t, "coder", got.Agent.String)
		require.Equal(t, "task", got.ActiveAgent.String)
	})

	t.Run("missing session affects no rows without erroring", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		rows, err := q.UpdateSessionActiveAgent(t.Context(), UpdateSessionActiveAgentParams{ID: "missing"})
		require.NoError(t, err)
		require.Zero(t, rows)
	})
}

func TestUpdateSessionTitleAndUsage(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	created, err := q.CreateSession(t.Context(), CreateSessionParams{
		ID:               "sess-usage",
		Title:            "before",
		PromptTokens:     10,
		CompletionTokens: 5,
		Cost:             1,
	})
	require.NoError(t, err)

	require.NoError(t, q.UpdateSessionTitleAndUsage(t.Context(), UpdateSessionTitleAndUsageParams{
		ID:               created.ID,
		Title:            "after",
		PromptTokens:     3,
		CompletionTokens: 2,
		Cost:             0.25,
	}))

	got, err := q.GetSessionByID(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "after", got.Title)
	require.EqualValues(t, 13, got.PromptTokens, "prompt tokens must accumulate, not overwrite")
	require.EqualValues(t, 7, got.CompletionTokens)
	require.InDelta(t, 1.25, got.Cost, 1e-9)
}

func TestDeleteSession(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	created, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-delete", Title: "x"})
	require.NoError(t, err)

	require.NoError(t, q.DeleteSession(t.Context(), created.ID))

	_, err = q.GetSessionByID(t.Context(), created.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestDeleteSession_CascadesToFilesAndMessages(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	session, err := q.CreateSession(t.Context(), CreateSessionParams{ID: "sess-cascade", Title: "x"})
	require.NoError(t, err)

	_, err = q.CreateFile(t.Context(), CreateFileParams{
		ID: "file-1", SessionID: session.ID, Path: "a.go", Content: "package a", Version: 0,
	})
	require.NoError(t, err)
	_, err = q.CreateMessage(t.Context(), CreateMessageParams{
		ID: "msg-1", SessionID: session.ID, Role: "user", Parts: "[]",
	})
	require.NoError(t, err)
	require.NoError(t, q.RecordFileRead(t.Context(), RecordFileReadParams{SessionID: session.ID, Path: "a.go"}))

	require.NoError(t, q.DeleteSession(t.Context(), session.ID))

	files, err := q.ListFilesBySession(t.Context(), session.ID)
	require.NoError(t, err)
	require.Empty(t, files, "files must cascade-delete with their session")

	messages, err := q.ListMessagesBySession(t.Context(), session.ID)
	require.NoError(t, err)
	require.Empty(t, messages, "messages must cascade-delete with their session")

	readFiles, err := q.ListSessionReadFiles(t.Context(), session.ID)
	require.NoError(t, err)
	require.Empty(t, readFiles, "read_files must cascade-delete with their session")
}

// insertSessionWithTimestamps inserts a minimal session row with
// pinned created_at/updated_at values. It bypasses CreateSession
// because that query hardcodes both timestamps to strftime('%s',
// 'now'), and the update_sessions_updated_at trigger resets
// updated_at to "now" on every UPDATE, so neither leaves a way to
// pin updated_at through the generated API alone.
func insertSessionWithTimestamps(t *testing.T, conn *sql.DB, id string, ts int64) {
	t.Helper()

	_, err := conn.ExecContext(t.Context(),
		"INSERT INTO sessions (id, title, updated_at, created_at) VALUES (?, ?, ?, ?)",
		id, id, ts, ts)
	require.NoError(t, err)
}
