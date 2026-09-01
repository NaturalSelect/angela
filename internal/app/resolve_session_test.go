package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newSessionServiceMock returns a MockSessionService seeded with sessions,
// wired only for the methods resolveSession reaches. Create calls are
// recorded into the returned slice pointer, mirroring the old
// mockSessionService.created field.
func newSessionServiceMock(t *testing.T, sessions []session.Session) (*MockSessionService, *[]session.Session) {
	t.Helper()
	m := NewMockSessionService(gomock.NewController(t))
	created := &[]session.Session{}
	m.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, title string) (session.Session, error) {
		s := session.Session{ID: "new-session-id", Title: title}
		*created = append(*created, s)
		return s, nil
	}).AnyTimes()
	m.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id string) (session.Session, error) {
		for _, s := range sessions {
			if s.ID == id {
				return s, nil
			}
		}
		return session.Session{}, sql.ErrNoRows
	}).AnyTimes()
	m.EXPECT().GetLast(gomock.Any()).DoAndReturn(func(context.Context) (session.Session, error) {
		if len(sessions) > 0 {
			return sessions[0], nil
		}
		return session.Session{}, sql.ErrNoRows
	}).AnyTimes()
	m.EXPECT().IsAgentToolSession(gomock.Any()).DoAndReturn(func(sessionID string) bool {
		parts := strings.Split(sessionID, "$$")
		return len(parts) == 2
	}).AnyTimes()
	return m, created
}

func newTestApp(sessions session.Service) *App {
	return &App{Sessions: sessions}
}

func TestResolveSession_NewSession(t *testing.T) {
	mock, created := newSessionServiceMock(t, nil)
	app := newTestApp(mock)

	sess, err := app.resolveSession(t.Context(), "", false)
	require.NoError(t, err)
	require.Equal(t, "new-session-id", sess.ID)
	require.Len(t, *created, 1)
}

func TestResolveSession_ContinueByID(t *testing.T) {
	mock, created := newSessionServiceMock(t, []session.Session{
		{ID: "existing-id", Title: "Old session"},
	})
	app := newTestApp(mock)

	sess, err := app.resolveSession(t.Context(), "existing-id", false)
	require.NoError(t, err)
	require.Equal(t, "existing-id", sess.ID)
	require.Equal(t, "Old session", sess.Title)
	require.Empty(t, *created)
}

func TestResolveSession_ContinueByID_NotFound(t *testing.T) {
	mock, _ := newSessionServiceMock(t, nil)
	app := newTestApp(mock)

	_, err := app.resolveSession(t.Context(), "nonexistent", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found")
}

func TestResolveSession_ContinueByID_ChildSession(t *testing.T) {
	mock, _ := newSessionServiceMock(t, []session.Session{
		{ID: "child-id", ParentSessionID: "parent-id", Title: "Child session"},
	})
	app := newTestApp(mock)

	_, err := app.resolveSession(t.Context(), "child-id", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot continue a child session")
}

func TestResolveSession_ContinueByID_AgentToolSession(t *testing.T) {
	mock, _ := newSessionServiceMock(t, nil)
	app := newTestApp(mock)

	_, err := app.resolveSession(t.Context(), "msg123$$tool456", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot continue an agent tool session")
}

func TestResolveSession_Last(t *testing.T) {
	mock, created := newSessionServiceMock(t, []session.Session{
		{ID: "most-recent", Title: "Latest session"},
		{ID: "older", Title: "Older session"},
	})
	app := newTestApp(mock)

	sess, err := app.resolveSession(t.Context(), "", true)
	require.NoError(t, err)
	require.Equal(t, "most-recent", sess.ID)
	require.Empty(t, *created)
}

func TestResolveSession_Last_NoSessions(t *testing.T) {
	mock, _ := newSessionServiceMock(t, nil)
	app := newTestApp(mock)

	_, err := app.resolveSession(t.Context(), "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no sessions found")
}
