package message

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func addMessage(t *testing.T, svc Service, sessionID string, role MessageRole, text string) string {
	t.Helper()
	created, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  role,
		Parts: []ContentPart{TextContent{Text: text}},
	})
	require.NoError(t, err)
	return created.ID
}

func TestListSinceStartsAtTheGivenMessage(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)
	var ids []string
	for _, text := range []string{"a", "b", "c", "d"} {
		ids = append(ids, addMessage(t, svc, sess, User, text))
	}

	got, err := svc.ListSince(t.Context(), sess, ids[2])
	require.NoError(t, err)
	require.Equal(t, []string{"c", "d"}, texts(got))

	got, err = svc.ListSince(t.Context(), sess, ids[3])
	require.NoError(t, err)
	require.Equal(t, []string{"d"}, texts(got))
}

func TestListSinceFallsBackToTheWholeSession(t *testing.T) {
	t.Parallel()

	svc, src, other := forkFixture(t)
	for _, text := range []string{"a", "b", "c"} {
		addMessage(t, svc, src, User, text)
	}
	foreign := addMessage(t, svc, other, User, "elsewhere")

	for name, since := range map[string]string{
		"empty":           "",
		"unknown":         uuid.New().String(),
		"other session's": foreign,
	} {
		got, err := svc.ListSince(t.Context(), src, since)
		require.NoError(t, err, name)
		require.Equal(t, []string{"a", "b", "c"}, texts(got), name)
	}
}

func TestListPageReturnsTheTailOldestFirst(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)
	for _, text := range []string{"a", "b", "c", "d", "e"} {
		addMessage(t, svc, sess, User, text)
	}

	page, err := svc.ListPage(t.Context(), sess, "", 3)
	require.NoError(t, err)
	require.Equal(t, []string{"c", "d", "e"}, texts(page.Messages))
	require.True(t, page.HasOlder)
}

func TestListPageWalksBackToTheStartOfTheSession(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)
	for _, text := range []string{"a", "b", "c", "d", "e"} {
		addMessage(t, svc, sess, User, text)
	}

	var pages [][]string
	cursor := ""
	for {
		page, err := svc.ListPage(t.Context(), sess, cursor, 2)
		require.NoError(t, err)
		pages = append(pages, texts(page.Messages))
		if !page.HasOlder {
			break
		}
		cursor = page.Messages[0].ID
	}

	require.Equal(t, [][]string{{"d", "e"}, {"b", "c"}, {"a"}}, pages)
}

func TestListPageEndingExactlyAtTheStartHasNothingOlder(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)
	for _, text := range []string{"a", "b", "c"} {
		addMessage(t, svc, sess, User, text)
	}

	page, err := svc.ListPage(t.Context(), sess, "", 3)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, texts(page.Messages))
	require.False(t, page.HasOlder)
}

// A tool result is stored after the assistant message that called it, so a
// page that began on one would show the call as interrupted. The results
// move to the older page, which then ends with their call.
func TestListPageNeverStartsOnAToolResult(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)
	addMessage(t, svc, sess, User, "u0")
	addMessage(t, svc, sess, Assistant, "a1")
	addMessage(t, svc, sess, Tool, "t1")
	addMessage(t, svc, sess, Tool, "t2")
	addMessage(t, svc, sess, User, "u1")
	addMessage(t, svc, sess, Assistant, "a2")

	newest, err := svc.ListPage(t.Context(), sess, "", 4)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "a2"}, texts(newest.Messages))
	require.True(t, newest.HasOlder)

	older, err := svc.ListPage(t.Context(), sess, newest.Messages[0].ID, 4)
	require.NoError(t, err)
	require.Equal(t, []string{"u0", "a1", "t1", "t2"}, texts(older.Messages))
	require.False(t, older.HasOlder)
}

func TestListPageKeepsAPageThatIsAllToolResults(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)
	addMessage(t, svc, sess, User, "u0")
	for _, text := range []string{"t1", "t2", "t3", "t4"} {
		addMessage(t, svc, sess, Tool, text)
	}

	page, err := svc.ListPage(t.Context(), sess, "", 3)
	require.NoError(t, err)
	require.Equal(t, []string{"t2", "t3", "t4"}, texts(page.Messages))
	require.True(t, page.HasOlder, "the page still needs a cursor to continue from")
}

func TestListPageOfAnEmptySession(t *testing.T) {
	t.Parallel()

	svc, sess := newTestService(t)

	page, err := svc.ListPage(t.Context(), sess, "", 5)
	require.NoError(t, err)
	require.Empty(t, page.Messages)
	require.False(t, page.HasOlder)
}

func TestListPageRejectsABadCursorOrLimit(t *testing.T) {
	t.Parallel()

	svc, src, other := forkFixture(t)
	addMessage(t, svc, src, User, "a")
	foreign := addMessage(t, svc, other, User, "elsewhere")

	_, err := svc.ListPage(t.Context(), src, uuid.New().String(), 5)
	require.Error(t, err, "an unknown cursor")

	_, err = svc.ListPage(t.Context(), src, foreign, 5)
	require.Error(t, err, "a cursor from another session")

	_, err = svc.ListPage(t.Context(), src, "", 0)
	require.Error(t, err, "a zero limit")
}
