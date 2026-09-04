package backend

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

func TestBackendSession_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	tests := []struct {
		name string
		call func(t *testing.T) error
	}{
		{"CreateSession", func(t *testing.T) error {
			_, err := b.CreateSession(t.Context(), "nope", "title")
			return err
		}},
		{"GetSession", func(t *testing.T) error {
			_, err := b.GetSession(t.Context(), "nope", "s1")
			return err
		}},
		{"ListSessions", func(t *testing.T) error {
			_, err := b.ListSessions(t.Context(), "nope")
			return err
		}},
		{"GetAgentSession", func(t *testing.T) error {
			_, err := b.GetAgentSession(t.Context(), "nope", "s1")
			return err
		}},
		{"ListSessionMessages", func(t *testing.T) error {
			_, err := b.ListSessionMessages(t.Context(), "nope", "s1")
			return err
		}},
		{"ListSessionHistory", func(t *testing.T) error {
			_, err := b.ListSessionHistory(t.Context(), "nope", "s1")
			return err
		}},
		{"SaveSession", func(t *testing.T) error {
			_, err := b.SaveSession(t.Context(), "nope", session.Session{})
			return err
		}},
		{"DeleteSession", func(t *testing.T) error {
			return b.DeleteSession(t.Context(), "nope", "s1")
		}},
		{"ListUserMessages", func(t *testing.T) error {
			_, err := b.ListUserMessages(t.Context(), "nope", "s1")
			return err
		}},
		{"ListAllUserMessages", func(t *testing.T) error {
			_, err := b.ListAllUserMessages(t.Context(), "nope")
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.call(t), ErrWorkspaceNotFound)
		})
	}
}

// TestBackendSession_CRUDFlow drives Create -> Get -> List -> Save ->
// Delete against a real session.Service wired through a real
// workspace, asserting each step's effect is actually observable
// through the next call rather than just checking for a nil error.
func TestBackendSession_CRUDFlow(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sess, err := b.CreateSession(t.Context(), ws.ID, "my session")
	require.NoError(t, err)
	require.NotEmpty(t, sess.ID)
	require.Equal(t, "my session", sess.Title)

	got, err := b.GetSession(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.Equal(t, sess.ID, got.ID)

	list, err := b.ListSessions(t.Context(), ws.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, sess.ID, list[0].ID)

	got.Title = "renamed"
	saved, err := b.SaveSession(t.Context(), ws.ID, got)
	require.NoError(t, err)
	require.Equal(t, "renamed", saved.Title)

	reGot, err := b.GetSession(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", reGot.Title)

	require.NoError(t, b.DeleteSession(t.Context(), ws.ID, sess.ID))
	_, err = b.GetSession(t.Context(), ws.ID, sess.ID)
	require.ErrorIs(t, err, session.ErrSessionNotFound)
}

// TestBackendSession_GetAgentSession covers both the session-not-found
// propagation and the happy path where a freshly created (unconfigured)
// workspace has no agent coordinator, so busy/branch report false.
func TestBackendSession_GetAgentSession(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sess, err := b.CreateSession(t.Context(), ws.ID, "s")
	require.NoError(t, err)

	agentSess, err := b.GetAgentSession(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.Equal(t, sess.ID, agentSess.Session.ID)
	require.Equal(t, "s", agentSess.Session.Title)
	require.False(t, agentSess.IsBusy, "fresh unconfigured workspace has no coordinator")
	require.False(t, agentSess.IsBranch)

	_, err = b.GetAgentSession(t.Context(), ws.ID, "missing-session")
	require.ErrorIs(t, err, session.ErrSessionNotFound)
}

// TestBackendSession_MessagesAndHistory drives real message.Service and
// history.Service calls, verifying the role filtering ListUserMessages
// and ListAllUserMessages perform on top of the raw ListSessionMessages
// feed.
func TestBackendSession_MessagesAndHistory(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sess, err := b.CreateSession(t.Context(), ws.ID, "s")
	require.NoError(t, err)

	_, err = b.ListSessionHistory(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)

	_, err = ws.Messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	})
	require.NoError(t, err)
	_, err = ws.Messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: "hi there"}},
	})
	require.NoError(t, err)

	all, err := b.ListSessionMessages(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.Len(t, all, 2)

	userOnly, err := b.ListUserMessages(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.Len(t, userOnly, 1)
	require.Equal(t, message.User, userOnly[0].Role)

	allUser, err := b.ListAllUserMessages(t.Context(), ws.ID)
	require.NoError(t, err)
	require.Len(t, allUser, 1)
	require.Equal(t, message.User, allUser[0].Role)
}
