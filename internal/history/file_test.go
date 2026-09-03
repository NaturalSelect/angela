package history

import (
	"context"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	ctx context.Context
	q   *db.Queries
	svc Service
}

func setupTest(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	q := db.New(conn)
	return &testEnv{
		ctx: t.Context(),
		q:   q,
		svc: NewService(q, conn),
	}
}

func (e *testEnv) createSession(t *testing.T, sessionID string) {
	t.Helper()
	_, err := e.q.CreateSession(e.ctx, db.CreateSessionParams{
		ID:    sessionID,
		Title: "Test Session",
	})
	require.NoError(t, err)
}

func TestService_Create(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	f, err := env.svc.Create(env.ctx, "session-1", "/a/b.go", "content-v0")
	require.NoError(t, err)
	require.Equal(t, "session-1", f.SessionID)
	require.Equal(t, "/a/b.go", f.Path)
	require.Equal(t, "content-v0", f.Content)
	require.Equal(t, int64(InitialVersion), f.Version)
	require.NotEmpty(t, f.ID)
}

func TestService_CreateVersion_FirstVersion(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	f, err := env.svc.CreateVersion(env.ctx, "session-1", "/a/b.go", "v0")
	require.NoError(t, err)
	require.Equal(t, int64(InitialVersion), f.Version)
}

func TestService_CreateVersion_IncrementsVersion(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	f1, err := env.svc.CreateVersion(env.ctx, "session-1", "/a/b.go", "v0")
	require.NoError(t, err)

	f2, err := env.svc.CreateVersion(env.ctx, "session-1", "/a/b.go", "v1")
	require.NoError(t, err)

	require.Equal(t, f1.Version+1, f2.Version)
	require.Equal(t, "v1", f2.Content)
}

func TestService_Get(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	created, err := env.svc.Create(env.ctx, "session-1", "/a/b.go", "content")
	require.NoError(t, err)

	got, err := env.svc.Get(env.ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created, got)
}

func TestService_Get_NotFound(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	_, err := env.svc.Get(env.ctx, "nonexistent")
	require.Error(t, err)
}

func TestService_GetByPathAndSession(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	created, err := env.svc.Create(env.ctx, "session-1", "/a/b.go", "content")
	require.NoError(t, err)

	got, err := env.svc.GetByPathAndSession(env.ctx, "/a/b.go", "session-1")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
}

func TestService_GetByPathAndSession_NotFound(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	_, err := env.svc.GetByPathAndSession(env.ctx, "/missing.go", "session-1")
	require.Error(t, err)
}

func TestService_ListBySession(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	_, err := env.svc.Create(env.ctx, "session-1", "/a.go", "a")
	require.NoError(t, err)
	_, err = env.svc.Create(env.ctx, "session-1", "/b.go", "b")
	require.NoError(t, err)

	files, err := env.svc.ListBySession(env.ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, files, 2)
}

func TestService_ListLatestSessionFiles(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	_, err := env.svc.CreateVersion(env.ctx, "session-1", "/a.go", "v0")
	require.NoError(t, err)
	_, err = env.svc.CreateVersion(env.ctx, "session-1", "/a.go", "v1")
	require.NoError(t, err)

	files, err := env.svc.ListLatestSessionFiles(env.ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "v1", files[0].Content)
}

func TestService_Delete(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	created, err := env.svc.Create(env.ctx, "session-1", "/a.go", "content")
	require.NoError(t, err)

	err = env.svc.Delete(env.ctx, created.ID)
	require.NoError(t, err)

	_, err = env.svc.Get(env.ctx, created.ID)
	require.Error(t, err)
}

func TestService_Delete_NotFound(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	err := env.svc.Delete(env.ctx, "nonexistent")
	require.Error(t, err)
}

func TestService_DeleteSessionFiles(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	_, err := env.svc.Create(env.ctx, "session-1", "/a.go", "a")
	require.NoError(t, err)
	_, err = env.svc.Create(env.ctx, "session-1", "/b.go", "b")
	require.NoError(t, err)

	err = env.svc.DeleteSessionFiles(env.ctx, "session-1")
	require.NoError(t, err)

	files, err := env.svc.ListBySession(env.ctx, "session-1")
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestService_PublishesEvents(t *testing.T) {
	t.Parallel()
	env := setupTest(t)
	env.createSession(t, "session-1")

	sub := env.svc.Subscribe(env.ctx)

	created, err := env.svc.Create(env.ctx, "session-1", "/a.go", "content")
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.Equal(t, pubsub.CreatedEvent, ev.Type)
		require.Equal(t, created.ID, ev.Payload.ID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for created event")
	}

	err = env.svc.Delete(env.ctx, created.ID)
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.Equal(t, pubsub.DeletedEvent, ev.Type)
		require.Equal(t, created.ID, ev.Payload.ID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deleted event")
	}
}
