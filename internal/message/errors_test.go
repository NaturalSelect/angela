package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/stretchr/testify/require"
)

// erroringQuerier wraps a [db.Querier] and forces selected methods to
// return a canned error instead of touching the database, so callers
// can exercise a [Service] method's error-propagation branches without
// corrupting the underlying SQLite file.
type erroringQuerier struct {
	db.Querier

	createMessageErr         error
	updateMessageErr         error
	deleteMessageErr         error
	listMessagesBySessionErr error
	listUserMessagesErr      error
	listAllUserMessagesErr   error

	// corruptCreatedParts makes CreateMessage succeed but return a row
	// whose parts column cannot be decoded, simulating data corruption
	// discovered immediately after the insert.
	corruptCreatedParts bool
}

func (e *erroringQuerier) CreateMessage(ctx context.Context, arg db.CreateMessageParams) (db.Message, error) {
	if e.createMessageErr != nil {
		return db.Message{}, e.createMessageErr
	}
	msg, err := e.Querier.CreateMessage(ctx, arg)
	if err != nil {
		return msg, err
	}
	if e.corruptCreatedParts {
		msg.Parts = "not valid json"
	}
	return msg, nil
}

func (e *erroringQuerier) UpdateMessage(ctx context.Context, arg db.UpdateMessageParams) error {
	if e.updateMessageErr != nil {
		return e.updateMessageErr
	}
	return e.Querier.UpdateMessage(ctx, arg)
}

func (e *erroringQuerier) DeleteMessage(ctx context.Context, id string) error {
	if e.deleteMessageErr != nil {
		return e.deleteMessageErr
	}
	return e.Querier.DeleteMessage(ctx, id)
}

func (e *erroringQuerier) ListMessagesBySession(ctx context.Context, sessionID string) ([]db.Message, error) {
	if e.listMessagesBySessionErr != nil {
		return nil, e.listMessagesBySessionErr
	}
	return e.Querier.ListMessagesBySession(ctx, sessionID)
}

func (e *erroringQuerier) ListUserMessagesBySession(ctx context.Context, sessionID string) ([]db.Message, error) {
	if e.listUserMessagesErr != nil {
		return nil, e.listUserMessagesErr
	}
	return e.Querier.ListUserMessagesBySession(ctx, sessionID)
}

func (e *erroringQuerier) ListAllUserMessages(ctx context.Context) ([]db.Message, error) {
	if e.listAllUserMessagesErr != nil {
		return nil, e.listAllUserMessagesErr
	}
	return e.Querier.ListAllUserMessages(ctx)
}

var errQuerierBoom = errors.New("querier: boom")

func TestDelete_ReturnsErrorWhenMessageNotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService(t)
	err := svc.Delete(t.Context(), "does-not-exist")
	require.Error(t, err)
}

func TestDelete_PropagatesDeleteMessageError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	normal := NewService(q)
	msg, err := normal.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
	require.NoError(t, err)

	erroring := NewService(&erroringQuerier{Querier: q, deleteMessageErr: errQuerierBoom})
	err = erroring.Delete(t.Context(), msg.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestCreate_PropagatesMarshalError(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	_, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{fakeContentPart{}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown part type")
}

func TestCreate_PropagatesCreateMessageError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, createMessageErr: errQuerierBoom})
	_, err = erroring.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestCreate_PropagatesFromDBItemError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, corruptCreatedParts: true})
	_, err = erroring.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
	require.Error(t, err)
}

func TestForkSession_PropagatesListMessagesBySessionError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	src := newTestSession(t, q)
	dst := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, listMessagesBySessionErr: errQuerierBoom})
	_, err = erroring.ForkSession(t.Context(), src.ID, dst.ID, "", "whatever")
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestForkSession_PropagatesCreateMessageError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	src := newTestSession(t, q)
	dst := newTestSession(t, q)

	normal := NewService(q)
	_, err = normal.Create(t.Context(), src.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "a"}}})
	require.NoError(t, err)
	cut, err := normal.Create(t.Context(), src.ID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	erroring := NewService(&erroringQuerier{Querier: q, createMessageErr: errQuerierBoom})
	_, err = erroring.ForkSession(t.Context(), src.ID, dst.ID, "", cut.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestDeleteSessionMessages_PropagatesListError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, listMessagesBySessionErr: errQuerierBoom})
	err = erroring.DeleteSessionMessages(t.Context(), sess.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestDeleteSessionMessages_PropagatesDeleteError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	normal := NewService(q)
	_, err = normal.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "a"}}})
	require.NoError(t, err)

	erroring := NewService(&erroringQuerier{Querier: q, deleteMessageErr: errQuerierBoom})
	err = erroring.DeleteSessionMessages(t.Context(), sess.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestDeleteFrom_PropagatesListError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, listMessagesBySessionErr: errQuerierBoom})
	_, err = erroring.DeleteFrom(t.Context(), sess.ID, "whatever")
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestDeleteFrom_PropagatesDeleteError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	normal := NewService(q)
	first, err := normal.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "a"}}})
	require.NoError(t, err)

	erroring := NewService(&erroringQuerier{Querier: q, deleteMessageErr: errQuerierBoom})
	_, err = erroring.DeleteFrom(t.Context(), sess.ID, first.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestList_PropagatesListMessagesBySessionError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, listMessagesBySessionErr: errQuerierBoom})
	_, err = erroring.List(t.Context(), sess.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestListUserMessages_PropagatesQuerierError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := NewService(&erroringQuerier{Querier: q, listUserMessagesErr: errQuerierBoom})
	_, err = erroring.ListUserMessages(t.Context(), sess.ID)
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestListUserMessages_PropagatesCorruptedRowError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)
	svc := NewService(q)

	created, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
	require.NoError(t, err)

	_, err = conn.ExecContext(t.Context(), `UPDATE messages SET parts = 'not valid json' WHERE id = ?`, created.ID)
	require.NoError(t, err)

	_, err = svc.ListUserMessages(t.Context(), sess.ID)
	require.Error(t, err)
}

func TestListAllUserMessages_PropagatesQuerierError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)

	erroring := NewService(&erroringQuerier{Querier: q, listAllUserMessagesErr: errQuerierBoom})
	_, err = erroring.ListAllUserMessages(t.Context())
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestListAllUserMessages_PropagatesCorruptedRowError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)
	svc := NewService(q)

	created, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
	require.NoError(t, err)

	_, err = conn.ExecContext(t.Context(), `UPDATE messages SET parts = 'not valid json' WHERE id = ?`, created.ID)
	require.NoError(t, err)

	_, err = svc.ListAllUserMessages(t.Context())
	require.Error(t, err)
}

// TestList_PropagatesCorruptedRowError covers the defensive path where a
// message row's JSON can't be decoded (e.g. hand-edited or corrupted on
// disk): List must surface the decode error instead of panicking or
// silently dropping the row.
func TestList_PropagatesCorruptedRowError(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)
	svc := NewService(q)

	created, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: User, Parts: []ContentPart{TextContent{Text: "x"}}})
	require.NoError(t, err)

	_, err = conn.ExecContext(t.Context(), `UPDATE messages SET parts = 'not valid json' WHERE id = ?`, created.ID)
	require.NoError(t, err)

	_, err = svc.List(t.Context(), sess.ID)
	require.Error(t, err)

	_, err = svc.Get(t.Context(), created.ID)
	require.Error(t, err)
}

func TestFlush_ReturnsNilForAnIDThatWasNeverUpdated(t *testing.T) {
	t.Parallel()

	svc, _ := newTestService(t)
	require.NoError(t, svc.Flush(t.Context(), "never-updated"))
}

func TestFlushAll_ReturnsFirstErrorButAttemptsEveryPendingID(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	erroring := &erroringQuerier{Querier: q, updateMessageErr: errQuerierBoom}
	svc := NewService(erroring, WithDebounce(time.Hour))

	msg1, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)
	msg1.AppendContent("x")
	require.NoError(t, svc.Update(t.Context(), msg1))

	msg2, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)
	msg2.AppendContent("y")
	require.NoError(t, svc.Update(t.Context(), msg2))

	err = svc.FlushAll(t.Context())
	require.ErrorIs(t, err, errQuerierBoom)
}

func TestUpdate_PropagatesWriteMarshalError(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t, WithDebounce(0))
	msg, err := svc.Create(t.Context(), sessionID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	msg.Parts = append(msg.Parts, fakeContentPart{})
	err = svc.Update(t.Context(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown part type")
}

// TestFlushOne_TimerFiredFlushBacksOffWhenAnotherIsInFlight pins the
// contract documented on [service.flushOne]: a timer-fired flush
// (syncCaller=false) that finds another flush already running for the
// same ID must return immediately rather than blocking, since the
// in-flight flusher will pick up the trailing dirty state itself.
//
// We drive both flushOne calls directly instead of waiting on a real
// timer so the race is deterministic: the first call is parked inside
// UpdateMessage via a release channel, and only once we know it holds
// flushing=true do we issue the second, timer-shaped call.
func TestFlushOne_TimerFiredFlushBacksOffWhenAnotherIsInFlight(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	slow := &slowUpdateQuerier{
		Querier: q,
		release: make(chan struct{}),
		started: make(chan struct{}),
	}
	svc := NewService(slow, WithDebounce(time.Hour)).(*service)

	msg, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)
	msg.AppendContent("payload")
	require.NoError(t, svc.Update(t.Context(), msg))

	flushDone := make(chan error, 1)
	go func() { flushDone <- svc.flushOne(t.Context(), msg.ID, true) }()

	select {
	case <-slow.started:
	case <-time.After(time.Second):
		t.Fatal("in-flight flush never reached UpdateMessage")
	}

	timerFlushDone := make(chan error, 1)
	go func() { timerFlushDone <- svc.flushOne(t.Context(), msg.ID, false) }()

	select {
	case err := <-timerFlushDone:
		require.NoError(t, err, "a timer-fired flush must back off cleanly when another flush is in-flight")
	case <-time.After(time.Second):
		t.Fatal("timer-fired flushOne should return immediately when another flush is in-flight")
	}

	close(slow.release)
	select {
	case err := <-flushDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("in-flight flush did not complete after release")
	}
}

// TestFlushOne_SyncCallerRetriesWhenDirtiedDuringWrite pins the comment
// on [service.flushOne]: "If a delta arrived during the SQL write and we
// are a sync caller, the user expects that delta to land too." A delta
// that lands mid-write must not be lost, and a synchronous caller must
// not return until it is durably persisted.
func TestFlushOne_SyncCallerRetriesWhenDirtiedDuringWrite(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	q := db.New(conn)
	sess := newTestSession(t, q)

	slow := &slowUpdateQuerier{
		Querier: q,
		release: make(chan struct{}),
		started: make(chan struct{}),
	}
	svc := NewService(slow, WithDebounce(time.Hour)).(*service)

	msg, err := svc.Create(t.Context(), sess.ID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)
	msg.AppendContent("first")
	require.NoError(t, svc.Update(t.Context(), msg))

	flushDone := make(chan error, 1)
	go func() { flushDone <- svc.Flush(t.Context(), msg.ID) }()

	select {
	case <-slow.started:
	case <-time.After(time.Second):
		t.Fatal("flush never reached UpdateMessage")
	}

	// A new delta arrives while the first write is still in flight.
	msg.AppendContent("-second")
	require.NoError(t, svc.Update(t.Context(), msg))

	close(slow.release)

	select {
	case err := <-flushDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Flush did not return after the in-flight write completed")
	}

	got, err := svc.Get(t.Context(), msg.ID)
	require.NoError(t, err)
	require.Equal(t, "first-second", got.Content().Text, "the delta that arrived during the in-flight write must not be lost")
}
