package backend

import (
	"context"

	"github.com/NaturalSelect/angela/internal/undo"
)

// PreviewUndo reports what undoing the given session's last turn
// would do, without doing it.
func (b *Backend) PreviewUndo(ctx context.Context, workspaceID, sessionID string) (undo.Preview, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return undo.Preview{}, err
	}

	return ws.Undo.Preview(ctx, sessionID)
}

// Undo reverts the turn identified by cutMessageID, as returned by a
// prior PreviewUndo for the same session.
func (b *Backend) Undo(ctx context.Context, workspaceID, sessionID, cutMessageID string) (undo.Result, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return undo.Result{}, err
	}

	return ws.Undo.Undo(ctx, sessionID, cutMessageID)
}
