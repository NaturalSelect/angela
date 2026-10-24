// NOTE: Package acp adapts Angela to the Agent Client Protocol. It only
// translates protocol messages, on top of the Workspace abstraction.
package acp

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// NOTE: Events is not part of workspace.Workspace because ClientWorkspace
// cannot give each caller its own event stream. Fold it into the shared
// interface once ClientWorkspace is removed.
type Workspace interface {
	workspace.Workspace
	Events(ctx context.Context) <-chan pubsub.Event[tea.Msg]
}

var _ Workspace = (*workspace.AppWorkspace)(nil)
