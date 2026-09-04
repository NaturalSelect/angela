package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustCreateSession creates a minimal session fixture and fails the
// test immediately if the insert does not succeed.
func mustCreateSession(t *testing.T, q *Queries, id string) Session {
	t.Helper()

	sess, err := q.CreateSession(t.Context(), CreateSessionParams{ID: id, Title: id})
	require.NoError(t, err)
	return sess
}

func TestCreateMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(sessionID string) CreateMessageParams
		check func(t *testing.T, got Message)
	}{
		{
			name: "minimal user message",
			setup: func(sessionID string) CreateMessageParams {
				return CreateMessageParams{ID: "msg-min", SessionID: sessionID, Role: "user", Parts: "[]"}
			},
			check: func(t *testing.T, got Message) {
				require.Equal(t, "user", got.Role)
				require.Equal(t, "[]", got.Parts)
				require.False(t, got.Model.Valid)
				require.False(t, got.Provider.Valid)
				require.False(t, got.Agent.Valid)
				require.False(t, got.FinishedAt.Valid)
				require.Zero(t, got.IsSummaryMessage)
				require.Positive(t, got.CreatedAt)
			},
		},
		{
			name: "full assistant message",
			setup: func(sessionID string) CreateMessageParams {
				return CreateMessageParams{
					ID:               "msg-full",
					SessionID:        sessionID,
					Role:             "assistant",
					Parts:            `[{"type":"text","data":{"text":"hi"}}]`,
					Model:            sql.NullString{String: "claude-sonnet", Valid: true},
					Provider:         sql.NullString{String: "anthropic", Valid: true},
					Agent:            sql.NullString{String: "coder", Valid: true},
					IsSummaryMessage: 1,
				}
			},
			check: func(t *testing.T, got Message) {
				require.Equal(t, "assistant", got.Role)
				require.Equal(t, "claude-sonnet", got.Model.String)
				require.Equal(t, "anthropic", got.Provider.String)
				require.Equal(t, "coder", got.Agent.String)
				require.EqualValues(t, 1, got.IsSummaryMessage)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			q, _ := newTestQueries(t)
			sess := mustCreateSession(t, q, "sess-for-"+tt.name)

			got, err := q.CreateMessage(t.Context(), tt.setup(sess.ID))
			require.NoError(t, err)
			tt.check(t, got)
		})
	}
}

func TestCreateMessage_RejectsUnknownSession(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	_, err := q.CreateMessage(t.Context(), CreateMessageParams{
		ID: "msg-orphan", SessionID: "does-not-exist", Role: "user", Parts: "[]",
	})
	require.Error(t, err, "foreign key constraint must reject an unknown session_id")
}

func TestGetMessage(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	created, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: "msg-1", SessionID: sess.ID, Role: "user", Parts: "[]"})
	require.NoError(t, err)

	t.Run("found", func(t *testing.T) {
		t.Parallel()

		got, err := q.GetMessage(t.Context(), created.ID)
		require.NoError(t, err)
		require.Equal(t, created, got)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, err := q.GetMessage(t.Context(), "does-not-exist")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

func TestListMessagesBySession_OrdersByInsertion(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	other := mustCreateSession(t, q, "sess-2")

	want := []string{"msg-0", "msg-1", "msg-2"}
	for _, id := range want {
		_, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: id, SessionID: sess.ID, Role: "user", Parts: "[]"})
		require.NoError(t, err)
	}
	_, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: "other-msg", SessionID: other.ID, Role: "user", Parts: "[]"})
	require.NoError(t, err)

	got, err := q.ListMessagesBySession(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	for i, id := range want {
		require.Equal(t, id, got[i].ID, "messages must be returned in insertion order")
	}
}

func TestListUserMessagesBySession_And_ListAllUserMessages(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sessA := mustCreateSession(t, q, "sess-a")
	sessB := mustCreateSession(t, q, "sess-b")

	create := func(id, sessionID, role string) {
		_, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: id, SessionID: sessionID, Role: role, Parts: "[]"})
		require.NoError(t, err)
	}
	create("a-user-1", sessA.ID, "user")
	create("a-assistant-1", sessA.ID, "assistant")
	create("a-user-2", sessA.ID, "user")
	create("b-user-1", sessB.ID, "user")

	t.Run("ListUserMessagesBySession filters to one session and role", func(t *testing.T) {
		t.Parallel()

		got, err := q.ListUserMessagesBySession(t.Context(), sessA.ID)
		require.NoError(t, err)

		ids := make([]string, len(got))
		for i, m := range got {
			ids[i] = m.ID
			require.Equal(t, "user", m.Role)
		}
		require.ElementsMatch(t, []string{"a-user-1", "a-user-2"}, ids)
	})

	t.Run("ListAllUserMessages spans every session", func(t *testing.T) {
		t.Parallel()

		got, err := q.ListAllUserMessages(t.Context())
		require.NoError(t, err)

		ids := make([]string, len(got))
		for i, m := range got {
			ids[i] = m.ID
			require.Equal(t, "user", m.Role)
		}
		require.ElementsMatch(t, []string{"a-user-1", "a-user-2", "b-user-1"}, ids)
	})
}

func TestGetLastAssistantMessageBySession(t *testing.T) {
	t.Parallel()

	t.Run("skips summary messages and user messages, returns the latest", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		sess := mustCreateSession(t, q, "sess-1")

		create := func(id, role string, isSummary int64) {
			_, err := q.CreateMessage(t.Context(), CreateMessageParams{
				ID: id, SessionID: sess.ID, Role: role, Parts: "[]", IsSummaryMessage: isSummary,
			})
			require.NoError(t, err)
		}
		create("m1", "user", 0)
		create("m2", "assistant", 0)
		create("m3", "user", 0)
		create("m4", "assistant", 1) // summary message, must be skipped
		create("m5", "assistant", 0) // the real latest assistant reply

		got, err := q.GetLastAssistantMessageBySession(t.Context(), sess.ID)
		require.NoError(t, err)
		require.Equal(t, "m5", got.ID)
	})

	t.Run("not found when no assistant message exists", func(t *testing.T) {
		t.Parallel()

		q, _ := newTestQueries(t)
		sess := mustCreateSession(t, q, "sess-1")
		_, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: "m1", SessionID: sess.ID, Role: "user", Parts: "[]"})
		require.NoError(t, err)

		_, err = q.GetLastAssistantMessageBySession(t.Context(), sess.ID)
		require.ErrorIs(t, err, sql.ErrNoRows)
	})
}

func TestUpdateMessage(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	created, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: "msg-1", SessionID: sess.ID, Role: "assistant", Parts: "[]"})
	require.NoError(t, err)
	require.False(t, created.FinishedAt.Valid)

	finishedAt := created.CreatedAt + 42
	require.NoError(t, q.UpdateMessage(t.Context(), UpdateMessageParams{
		ID:         created.ID,
		Parts:      `[{"type":"text","data":{"text":"done"}}]`,
		FinishedAt: sql.NullInt64{Int64: finishedAt, Valid: true},
	}))

	got, err := q.GetMessage(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, `[{"type":"text","data":{"text":"done"}}]`, got.Parts)
	require.True(t, got.FinishedAt.Valid)
	require.Equal(t, finishedAt, got.FinishedAt.Int64)
}

func TestDeleteMessage(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	created, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: "msg-1", SessionID: sess.ID, Role: "user", Parts: "[]"})
	require.NoError(t, err)

	before, err := q.GetSessionByID(t.Context(), sess.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, before.MessageCount, "insert trigger must bump message_count")

	require.NoError(t, q.DeleteMessage(t.Context(), created.ID))

	_, err = q.GetMessage(t.Context(), created.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)

	after, err := q.GetSessionByID(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Zero(t, after.MessageCount, "delete trigger must decrement message_count")
}

func TestDeleteSessionMessages(t *testing.T) {
	t.Parallel()

	q, _ := newTestQueries(t)
	sess := mustCreateSession(t, q, "sess-1")
	for _, id := range []string{"m1", "m2", "m3"} {
		_, err := q.CreateMessage(t.Context(), CreateMessageParams{ID: id, SessionID: sess.ID, Role: "user", Parts: "[]"})
		require.NoError(t, err)
	}

	require.NoError(t, q.DeleteSessionMessages(t.Context(), sess.ID))

	got, err := q.ListMessagesBySession(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, got)

	after, err := q.GetSessionByID(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Zero(t, after.MessageCount, "bulk delete must still run the per-row decrement trigger")
}
