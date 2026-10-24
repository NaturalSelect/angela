// NOTE: Package acp adapts Angela to the Agent Client Protocol. It only
// translates protocol messages, on top of the Workspace abstraction.
package acp

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// NOTE: Events gives each caller its own event stream without needing a
// tea.Program, which the ACP front end does not have. It stays off
// workspace.Workspace so the TUI-facing interface and its mock are unchanged.
type Workspace interface {
	workspace.Workspace
	Events(ctx context.Context) <-chan pubsub.Event[tea.Msg]
}

var _ Workspace = (*workspace.AppWorkspace)(nil)
