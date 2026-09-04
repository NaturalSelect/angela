package backend

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/undo"
	"github.com/stretchr/testify/require"
)

func TestBackendUndo_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	_, err := b.PreviewUndo(t.Context(), "nope", "s1")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)

	_, err = b.Undo(t.Context(), "nope", "s1", "m1")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}

func TestBackendUndo_NothingToUndo(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sess, err := b.CreateSession(t.Context(), ws.ID, "s")
	require.NoError(t, err)

	_, err = b.PreviewUndo(t.Context(), ws.ID, sess.ID)
	require.ErrorIs(t, err, undo.ErrNothingToUndo)
}

// TestBackendUndo_PreviewAndUndo drives a real undo.Service through the
// backend wrapper: it creates a genuine user turn, previews undoing it,
// and confirms the preview's cut point is exactly what Undo removes.
func TestBackendUndo_PreviewAndUndo(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sess, err := b.CreateSession(t.Context(), ws.ID, "s")
	require.NoError(t, err)

	msg, err := ws.Messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	})
	require.NoError(t, err)

	preview, err := b.PreviewUndo(t.Context(), ws.ID, sess.ID)
	require.NoError(t, err)
	require.Equal(t, msg.ID, preview.CutMessageID)
	require.Equal(t, "hello", preview.PoppedText)

	result, err := b.Undo(t.Context(), ws.ID, sess.ID, preview.CutMessageID)
	require.NoError(t, err)
	require.Equal(t, "hello", result.PoppedText)

	remaining, err := ws.Messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, remaining, "the undone turn must no longer be in the message list")
}
