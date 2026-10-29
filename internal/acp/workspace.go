// NOTE: Package acp adapts Angela to the Agent Client Protocol. It only
// translates protocol messages, on top of the Workspace abstraction.
package acp

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// NOTE: The methods below stay off workspace.Workspace so the TUI-facing
// interface and its mock are unchanged. Events gives each caller its own
// event stream without needing a tea.Program, which the ACP front end does
// not have. The accepted-run pair lets a session/cancel that arrives before
// a run registers be recorded against it instead of being dropped.
type Workspace interface {
	workspace.Workspace
	Events(ctx context.Context) <-chan pubsub.Event[tea.Msg]
	// ListCustomCommands reads the user's and the project's command files.
	ListCustomCommands(ctx context.Context) ([]commands.CustomCommand, error)
	// AgentBeginAccepted reserves a run on the session. The caller must
	// close the handle, and may get nil when the session has no executor.
	AgentBeginAccepted(ctx context.Context, sessionID string) *agent.AcceptedRun
	// AgentRunAccepted runs a prompt under a reservation taken by
	// AgentBeginAccepted.
	AgentRunAccepted(ctx context.Context, accept *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) error
	// AttachMCP registers servers for this process only and starts
	// connecting them. It does not wait: the returned channel closes once
	// every attempt has settled, and the caller decides how long to wait on
	// it. ctx is the lifetime of their sessions.
	AttachMCP(ctx context.Context, servers map[string]config.MCPConfig) <-chan struct{}
}

var _ Workspace = (*workspace.AppWorkspace)(nil)
