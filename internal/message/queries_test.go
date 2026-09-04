package message

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/stretchr/testify/require"
)

func TestListUserMessages_FiltersToSessionAndRoleNewestFirst(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "u1"}}})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "a1"}}})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessionID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "u2"}}})
	require.NoError(t, err)

	got, err := svc.ListUserMessages(t.Context(), sessionID)
	require.NoError(t, err)
	// ListUserMessagesBySession orders by rowid DESC: most recent first.
	require.Equal(t, []string{"u2", "u1"}, texts(got))
}

func TestListUserMessages_EmptyWhenSessionHasNoUserMessages(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "a1"}}})
	require.NoError(t, err)

	got, err := svc.ListUserMessages(t.Context(), sessionID)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestListAllUserMessages_SpansEverySession(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	svc := NewService(q)
	sessA := newTestSession(t, q)
	sessB := newTestSession(t, q)

	_, err = svc.Create(t.Context(), sessA.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "a-u1"}}})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessA.ID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "a-asst"}}})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessB.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "b-u1"}}})
	require.NoError(t, err)

	got, err := svc.ListAllUserMessages(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"b-u1", "a-u1"}, texts(got), "ListAllUserMessages orders rowid DESC across every session")
}

func TestListAllUserMessages_EmptyWhenNoUserMessagesExist(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "a1"}}})
	require.NoError(t, err)

	got, err := svc.ListAllUserMessages(t.Context())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestGetLastAssistantMessage_ReturnsNewest(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "first"}}})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "second"}}})
	require.NoError(t, err)

	got, err := svc.GetLastAssistantMessage(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "second", got.Content().Text)
}

func TestGetLastAssistantMessage_SkipsSummaryMessages(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant, Parts: []ContentPart{TextContent{Text: "real"}}})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:             Assistant,
		Parts:            []ContentPart{TextContent{Text: "summary"}},
		IsSummaryMessage: true,
	})
	require.NoError(t, err)

	got, err := svc.GetLastAssistantMessage(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "real", got.Content().Text, "a newer summary message must not be returned as the last assistant message")
}

func TestGetLastAssistantMessage_NotFoundWhenNoneExists(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	_, err := svc.GetLastAssistantMessage(t.Context(), sessionID)
	require.Error(t, err)
}

func TestDeleteSessionMessages_RemovesOnlyThatSessionsMessages(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	svc := NewService(q)
	sessA := newTestSession(t, q)
	sessB := newTestSession(t, q)

	for _, text := range []string{"a1", "a2"} {
		_, err := svc.Create(t.Context(), sessA.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: text}}})
		require.NoError(t, err)
	}
	_, err = svc.Create(t.Context(), sessB.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "b1"}}})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteSessionMessages(t.Context(), sessA.ID))

	remainingA, err := svc.List(t.Context(), sessA.ID)
	require.NoError(t, err)
	require.Empty(t, remainingA)

	remainingB, err := svc.List(t.Context(), sessB.ID)
	require.NoError(t, err)
	require.Len(t, remainingB, 1)
}

func TestDeleteSessionMessages_EmptySessionIsNoOp(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	require.NoError(t, svc.DeleteSessionMessages(t.Context(), sessionID))
}
