package message

import (
	"context"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDeleteFromRemovesTheCutMessageAndEverythingAfterIt(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)

	var ids []string
	for _, text := range []string{"a", "b", "c", "d"} {
		created, err := svc.Create(t.Context(), sess, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: text}},
		})
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}

	n, err := svc.DeleteFrom(t.Context(), sess, ids[1])
	require.NoError(t, err)
	require.Equal(t, 3, n)

	remaining, err := svc.List(t.Context(), sess)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, texts(remaining))
}

// TestDeleteFromPublishesADeletedEventPerMessageNewestFirst pins the
// deletion order: it must delete tail-to-head so a reader observing
// the session mid-delete never sees a later message survive an
// earlier one it depends on.
func TestDeleteFromPublishesADeletedEventPerMessageNewestFirst(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)

	var ids []string
	for _, text := range []string{"a", "b", "c"} {
		created, err := svc.Create(t.Context(), sess, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: text}},
		})
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}

	subCtx, cancelSub := context.WithCancel(t.Context())
	defer cancelSub()
	collector := collect(subCtx, svc.Subscribe(subCtx))
	time.Sleep(5 * time.Millisecond)
	collector.reset()

	n, err := svc.DeleteFrom(t.Context(), sess, ids[0])
	require.NoError(t, err)
	require.Equal(t, 3, n)

	time.Sleep(20 * time.Millisecond)
	events := collector.snapshot()
	require.Len(t, events, 3)
	var deletedTexts []string
	for _, ev := range events {
		require.Equal(t, pubsub.DeletedEvent, ev.Type)
		deletedTexts = append(deletedTexts, ev.Payload.Content().Text)
	}
	require.Equal(t, []string{"c", "b", "a"}, deletedTexts, "messages are deleted newest first")
}

func TestDeleteFromRejectsAMessageIDFromAnotherSession(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)

	_, err := svc.Create(t.Context(), sess, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "a"}},
	})
	require.NoError(t, err)

	n, err := svc.DeleteFrom(t.Context(), sess, uuid.New().String())
	require.Error(t, err)
	require.Zero(t, n)

	remaining, err := svc.List(t.Context(), sess)
	require.NoError(t, err)
	require.Len(t, remaining, 1, "a rejected DeleteFrom must not remove anything")
}
