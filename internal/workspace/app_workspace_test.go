package workspace

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newTestConfigStore builds a real config.ConfigStore rooted at a
// throwaway HOME/XDG tree, mirroring internal/config's own
// isolateReloadEnv test helper, so a test never reads or writes the
// developer's real config files. Callers must not use t.Parallel():
// it calls t.Setenv.
func newTestConfigStore(t *testing.T) *config.ConfigStore {
	t.Helper()
	return newTestConfigStoreInDir(t, t.TempDir())
}

// newTestConfigStoreInDir is newTestConfigStore for a caller that needs
// control over the working directory the store resolves paths against
// (e.g. project-initialization checks that read directory contents).
func newTestConfigStoreInDir(t *testing.T, workingDir string) *config.ConfigStore {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(isolated, ".local", "share"))
	t.Setenv("ANGELA_GLOBAL_CONFIG", filepath.Join(isolated, ".config", "angela"))

	store, err := config.Load(workingDir, t.TempDir(), false)
	require.NoError(t, err)
	return store
}

// awFixture bundles an AppWorkspace with gomock doubles for every
// service dependency AppWorkspace can reach through app.App, so tests
// can set expectations without a real database or LLM provider.
type awFixture struct {
	ws       *AppWorkspace
	app      *app.App
	sessions *MockSessionService
	messages *MockMessageService
	history  *MockHistoryService
	files    *MockFileTracker
	coord    *MockCoordinator
}

// newAWFixture builds an AppWorkspace over app.NewForTest with every
// mockable service wired to a gomock double. The config store is nil,
// which is safe here: none of the methods exercised through this
// fixture read AppWorkspace.store.
func newAWFixture(t *testing.T) *awFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)

	fx := &awFixture{
		app:      a,
		sessions: NewMockSessionService(ctrl),
		messages: NewMockMessageService(ctrl),
		history:  NewMockHistoryService(ctrl),
		files:    NewMockFileTracker(ctrl),
		coord:    NewMockCoordinator(ctrl),
	}
	a.Sessions = fx.sessions
	a.Messages = fx.messages
	a.History = fx.history
	a.FileTracker = fx.files
	a.AgentCoordinator = fx.coord
	fx.ws = NewAppWorkspace(a, nil)
	return fx
}

func TestAppWorkspace_CreateSession(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := session.Session{ID: "s1", Title: "hello"}
		fx.sessions.EXPECT().Create(gomock.Any(), "hello").Return(want, nil)

		got, err := fx.ws.CreateSession(t.Context(), "hello")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("insert failed")
		fx.sessions.EXPECT().Create(gomock.Any(), "hello").Return(session.Session{}, boom)

		_, err := fx.ws.CreateSession(t.Context(), "hello")
		require.ErrorIs(t, err, boom)
	})
}

// TestAppWorkspace_GetSession_NotFound pins that the session package's
// sentinel error survives the AppWorkspace passthrough unwrapped, since
// callers above this layer branch on errors.Is(err, session.ErrSessionNotFound).
func TestAppWorkspace_GetSession_NotFound(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.sessions.EXPECT().Get(gomock.Any(), "missing").Return(session.Session{}, session.ErrSessionNotFound)

	_, err := fx.ws.GetSession(t.Context(), "missing")
	require.ErrorIs(t, err, session.ErrSessionNotFound)
}

func TestAppWorkspace_GetSession_Success(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	want := session.Session{ID: "s1", Title: "hello"}
	fx.sessions.EXPECT().Get(gomock.Any(), "s1").Return(want, nil)

	got, err := fx.ws.GetSession(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestAppWorkspace_ListSessions(t *testing.T) {
	t.Parallel()

	t.Run("returns sessions", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := []session.Session{{ID: "s1"}, {ID: "s2"}}
		fx.sessions.EXPECT().List(gomock.Any()).Return(want, nil)

		got, err := fx.ws.ListSessions(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("empty list is not an error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.sessions.EXPECT().List(gomock.Any()).Return(nil, nil)

		got, err := fx.ws.ListSessions(t.Context())
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("query failed")
		fx.sessions.EXPECT().List(gomock.Any()).Return(nil, boom)

		_, err := fx.ws.ListSessions(t.Context())
		require.ErrorIs(t, err, boom)
	})
}

func TestAppWorkspace_SaveSession(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		in := session.Session{ID: "s1", Title: "old"}
		want := session.Session{ID: "s1", Title: "new"}
		fx.sessions.EXPECT().Save(gomock.Any(), in).Return(want, nil)

		got, err := fx.ws.SaveSession(t.Context(), in)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("save failed")
		fx.sessions.EXPECT().Save(gomock.Any(), gomock.Any()).Return(session.Session{}, boom)

		_, err := fx.ws.SaveSession(t.Context(), session.Session{ID: "s1"})
		require.ErrorIs(t, err, boom)
	})
}

func TestAppWorkspace_DeleteSession(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.sessions.EXPECT().Delete(gomock.Any(), "s1").Return(nil)

		require.NoError(t, fx.ws.DeleteSession(t.Context(), "s1"))
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("delete failed")
		fx.sessions.EXPECT().Delete(gomock.Any(), "s1").Return(boom)

		require.ErrorIs(t, fx.ws.DeleteSession(t.Context(), "s1"), boom)
	})
}

func TestAppWorkspace_CreateAgentToolSessionID(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.sessions.EXPECT().CreateAgentToolSessionID("msg-1", "call-1").Return("msg-1$$call-1")

	require.Equal(t, "msg-1$$call-1", fx.ws.CreateAgentToolSessionID("msg-1", "call-1"))
}

func TestAppWorkspace_ParseAgentToolSessionID(t *testing.T) {
	t.Parallel()

	t.Run("recognized agent tool session", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.sessions.EXPECT().ParseAgentToolSessionID("msg-1$$call-1").Return("msg-1", "call-1", true)

		msgID, callID, ok := fx.ws.ParseAgentToolSessionID("msg-1$$call-1")
		require.True(t, ok)
		require.Equal(t, "msg-1", msgID)
		require.Equal(t, "call-1", callID)
	})

	t.Run("not an agent tool session", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.sessions.EXPECT().ParseAgentToolSessionID("plain-id").Return("", "", false)

		_, _, ok := fx.ws.ParseAgentToolSessionID("plain-id")
		require.False(t, ok)
	})
}

// TestAppWorkspace_SetCurrentSession pins that reporting the current
// session never errors, including when clearing it with an empty ID
// (landing screen) and when herdr is not attached (nil client).
func TestAppWorkspace_SetCurrentSession(t *testing.T) {
	t.Parallel()

	for _, sessionID := range []string{"sess-1", ""} {
		t.Run("sessionID="+sessionID, func(t *testing.T) {
			t.Parallel()
			fx := newAWFixture(t)
			require.NoError(t, fx.ws.SetCurrentSession(t.Context(), sessionID))
		})
	}
}

// TestAppWorkspace_ListMessages_FlushError verifies that a FlushAll
// failure short-circuits before List runs: List debounces streaming
// updates in memory, so a caller must never read stale state after a
// failed flush. The mock has no List expectation, so an unwanted call
// would fail the test on its own.
func TestAppWorkspace_ListMessages_FlushError(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	boom := errors.New("flush failed")
	fx.messages.EXPECT().FlushAll(gomock.Any()).Return(boom)

	got, err := fx.ws.ListMessages(t.Context(), "sess-1")
	require.ErrorIs(t, err, boom)
	require.Nil(t, got)
}

func TestAppWorkspace_ListMessages_Success(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	want := []message.Message{{ID: "m1"}, {ID: "m2"}}
	gomock.InOrder(
		fx.messages.EXPECT().FlushAll(gomock.Any()).Return(nil),
		fx.messages.EXPECT().List(gomock.Any(), "sess-1").Return(want, nil),
	)

	got, err := fx.ws.ListMessages(t.Context(), "sess-1")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestAppWorkspace_ListUserMessages(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := []message.Message{{ID: "m1"}}
		fx.messages.EXPECT().ListUserMessages(gomock.Any(), "sess-1").Return(want, nil)

		got, err := fx.ws.ListUserMessages(t.Context(), "sess-1")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("query failed")
		fx.messages.EXPECT().ListUserMessages(gomock.Any(), "sess-1").Return(nil, boom)

		_, err := fx.ws.ListUserMessages(t.Context(), "sess-1")
		require.ErrorIs(t, err, boom)
	})
}

func TestAppWorkspace_ListAllUserMessages(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := []message.Message{{ID: "m1"}, {ID: "m2"}}
		fx.messages.EXPECT().ListAllUserMessages(gomock.Any()).Return(want, nil)

		got, err := fx.ws.ListAllUserMessages(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("query failed")
		fx.messages.EXPECT().ListAllUserMessages(gomock.Any()).Return(nil, boom)

		_, err := fx.ws.ListAllUserMessages(t.Context())
		require.ErrorIs(t, err, boom)
	})
}
