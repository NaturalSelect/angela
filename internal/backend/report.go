package backend

import (
	"context"

	"github.com/NaturalSelect/angela/internal/agent/tools"
)

// ImportReport stores a report read from disk in the given session so
// the agent's LoadReport tool can return it.
func (b *Backend) ImportReport(ctx context.Context, workspaceID, sessionID string, report tools.Report) (tools.ImportResult, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return tools.ImportResult{}, err
	}

	return tools.ImportReport(ctx, ws.Sessions, ws.Messages, sessionID, report)
}
