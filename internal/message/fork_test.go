package message

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// forkFixture builds one service over two sessions: a source to fork from and
// an empty destination to fork into.
func forkFixture(t *testing.T) (Service, string, string) {
	t.Helper()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	q := db.New(conn)
	return NewService(q), newTestSession(t, q).ID, newTestSession(t, q).ID
}

// texts reads the text of each message, for order assertions.
func texts(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i := range msgs {
		out[i] = msgs[i].Content().Text
	}
	return out
}

func TestForkSessionCopiesOnlyWhatPrecedesTheForkPoint(t *testing.T) {
	t.Parallel()

	svc, src, dst := forkFixture(t)

	var ids []string
	for _, text := range []string{"a", "b", "c", "d"} {
		created, err := svc.Create(t.Context(), src, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: text}},
		})
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}

	// Fork at "c": the branch inherits everything the parent had decided
	// before it, and neither the forking message nor anything after it.
	require.NoError(t, svc.ForkSession(t.Context(), src, dst, ids[2]))

	forked, err := svc.List(t.Context(), dst)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, texts(forked))

	original, err := svc.List(t.Context(), src)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c", "d"}, texts(original),
		"forking must not disturb the session it read from")
}

func TestForkSessionRebuildsIdentityOfEveryCopy(t *testing.T) {
	t.Parallel()

	svc, src, dst := forkFixture(t)

	for i := range 3 {
		_, err := svc.Create(t.Context(), src, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: fmt.Sprint(i)}},
		})
		require.NoError(t, err)
	}
	cut, err := svc.Create(t.Context(), src, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	require.NoError(t, svc.ForkSession(t.Context(), src, dst, cut.ID))

	original, err := svc.List(t.Context(), src)
	require.NoError(t, err)
	forked, err := svc.List(t.Context(), dst)
	require.NoError(t, err)
	require.Len(t, forked, 3)

	srcIDs := map[string]bool{}
	for _, m := range original {
		srcIDs[m.ID] = true
	}
	seen := map[string]bool{}
	for _, m := range forked {
		require.Equal(t, dst, m.SessionID, "a copy still points at the source session")
		require.False(t, srcIDs[m.ID], "a copy reused the source's message ID")
		require.False(t, seen[m.ID], "two copies share an ID")
		seen[m.ID] = true
	}
}

// Create appends a Finish part to every non-assistant message. Copies must go
// through the raw insert instead, or each fork would staple another one on.
func TestForkSessionDoesNotRestampFinishOnCopies(t *testing.T) {
	t.Parallel()

	svc, src, dst := forkFixture(t)

	_, err := svc.Create(t.Context(), src, CreateMessageParams{
		Role:  Tool,
		Parts: []ContentPart{ToolResult{ToolCallID: "call-1", Name: "grep", Content: "hit"}},
	})
	require.NoError(t, err)
	cut, err := svc.Create(t.Context(), src, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	require.NoError(t, svc.ForkSession(t.Context(), src, dst, cut.ID))

	forked, err := svc.List(t.Context(), dst)
	require.NoError(t, err)
	require.Len(t, forked, 1)

	original, err := svc.List(t.Context(), src)
	require.NoError(t, err)
	require.Equal(t, len(original[0].Parts), len(forked[0].Parts),
		"the copy grew or lost parts relative to its original")

	results := forked[0].ToolResults()
	require.Len(t, results, 1)
	require.Equal(t, "call-1", results[0].ToolCallID)
	require.Equal(t, "hit", results[0].Content)
}

// The destination has no reader yet, and the user is looking at the source.
// A create event per copied message would replay the parent's whole tool
// history into the branch's block on their screen.
func TestForkSessionPublishesNothing(t *testing.T) {
	t.Parallel()

	svc, src, dst := forkFixture(t)

	for _, text := range []string{"a", "b", "c"} {
		_, err := svc.Create(t.Context(), src, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: text}},
		})
		require.NoError(t, err)
	}
	cut, err := svc.Create(t.Context(), src, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	subCtx, cancelSub := context.WithCancel(t.Context())
	defer cancelSub()
	collector := collect(subCtx, svc.Subscribe(subCtx))
	time.Sleep(5 * time.Millisecond)
	collector.reset()

	require.NoError(t, svc.ForkSession(t.Context(), src, dst, cut.ID))

	time.Sleep(20 * time.Millisecond)
	require.Empty(t, collector.snapshot(), "forking published events")
}

func TestForkSessionRejectsAForkPointFromAnotherSession(t *testing.T) {
	t.Parallel()

	svc, src, dst := forkFixture(t)

	_, err := svc.Create(t.Context(), src, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "a"}},
	})
	require.NoError(t, err)

	// A well-formed ID that simply is not part of the source.
	err = svc.ForkSession(t.Context(), src, dst, uuid.New().String())
	require.Error(t, err)

	forked, listErr := svc.List(t.Context(), dst)
	require.NoError(t, listErr)
	require.Empty(t, forked, "a rejected fork still wrote into the destination")
}

// Forking at the very first message is the degenerate case: the branch starts
// from an empty transcript rather than from an error.
func TestForkSessionAtTheFirstMessageCopiesNothing(t *testing.T) {
	t.Parallel()

	svc, src, dst := forkFixture(t)

	first, err := svc.Create(t.Context(), src, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "a"}},
	})
	require.NoError(t, err)

	require.NoError(t, svc.ForkSession(t.Context(), src, dst, first.ID))

	forked, err := svc.List(t.Context(), dst)
	require.NoError(t, err)
	require.Empty(t, forked)
}
