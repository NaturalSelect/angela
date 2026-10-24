// NOTE: Angela's own port for ACP v1. It is stdlib-only and its structs
// carry no json tags, so the SDK binding owns the wire format.
package protocol

import (
	"context"
	"io"
)

// NOTE: The ctx passed to Prompt ends only when the connection closes.
// A session/cancel from the editor arrives through Cancel instead.
type Agent interface {
	Initialize(ctx context.Context, req InitializeRequest) (InitializeResponse, error)
	NewSession(ctx context.Context, req NewSessionRequest) (NewSessionResponse, error)
	LoadSession(ctx context.Context, req LoadSessionRequest) (LoadSessionResponse, error)
	ListSessions(ctx context.Context, req ListSessionsRequest) (ListSessionsResponse, error)
	DeleteSession(ctx context.Context, req DeleteSessionRequest) error
	Prompt(ctx context.Context, req PromptRequest) (PromptResponse, error)
	Cancel(ctx context.Context, req CancelRequest) error
	SetConfigOption(ctx context.Context, req SetConfigOptionRequest) (SetConfigOptionResponse, error)
}

type Client interface {
	SessionUpdate(ctx context.Context, n SessionNotification) error
	RequestPermission(ctx context.Context, req PermissionRequest) (PermissionResponse, error)
	Elicit(ctx context.Context, req ElicitationRequest) (ElicitationResponse, error)
}

// NOTE: newAgent is a factory because the Agent needs a Client, and the
// Client only exists once the connection does.
type Serve func(ctx context.Context, r io.Reader, w io.Writer, newAgent func(Client) Agent) error
