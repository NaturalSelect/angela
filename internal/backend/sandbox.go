package backend

import (
	"context"
	"fmt"

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

// EnterSandbox always fails with sandbox.ErrNotSupported: a Backend
// hosts multiple workspaces in one OS process, and Landlock
// confinement is irreversible and process-wide (see
// internal/sandbox). Restricting one workspace here would also
// restrict every other workspace the daemon serves, so this can
// never be supported here regardless of cfg.
func (b *Backend) EnterSandbox(ctx context.Context, workspaceID string, cfg sandbox.Config) error {
	if _, err := b.GetWorkspace(workspaceID); err != nil {
		return err
	}
	return fmt.Errorf("sandbox is not supported for a workspace served by a daemon, since it would restrict every workspace the daemon hosts: %w", sandbox.ErrNotSupported)
}
