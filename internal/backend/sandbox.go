package backend

import (
	"context"

	"github.com/NaturalSelect/angela/internal/sandbox"
)

// IsInSandbox reports whether the workspace's process is currently
// confined by a sandbox.
func (b *Backend) IsInSandbox(workspaceID string) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}
	return ws.Sandbox.IsInSandbox(), nil
}

// EnterSandbox restricts the workspace's process according to cfg.
func (b *Backend) EnterSandbox(ctx context.Context, workspaceID string, cfg sandbox.Config) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	return ws.Sandbox.EnterSandbox(cfg)
}
