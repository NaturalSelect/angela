// NOTE: Package caelis binds the protocol port to the caelis-labs ACP SDK. It
// is the only package allowed to import that SDK.
package caelis

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
)

// NOTE: The SDK's reader goroutine can block on a stdin read that cannot be
// interrupted, so waiting for it must be bounded.
const closeTimeout = 3 * time.Second

var _ protocol.Serve = Serve

// NOTE: Serve takes ownership of r and w, because the SDK closes them when
// the connection ends. It returns nil when the peer closes the connection or
// ctx ends.
func Serve(ctx context.Context, r io.Reader, w io.Writer, newAgent func(protocol.Client) protocol.Agent) error {
	a := &sdkAgent{ready: make(chan struct{})}
	conn, err := acp.NewAgentSideConnectionWithOptions(a, w, r, acp.ConnectionOptions{})
	if err != nil {
		return err
	}

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-conn.Done():
			cancel()
		case <-connCtx.Done():
		}
	}()

	a.connCtx = connCtx
	a.inner = newAgent(sdkClient{conn: conn})
	close(a.ready)

	select {
	case <-conn.Done():
	case <-ctx.Done():
		_ = conn.Close()
	}

	waitCtx, stop := context.WithTimeout(context.Background(), closeTimeout)
	defer stop()
	err = conn.Wait(waitCtx)
	if waitCtx.Err() != nil || ctx.Err() != nil ||
		errors.Is(err, acp.ErrPeerClosed) || errors.Is(err, acp.ErrConnectionClosed) {
		return nil
	}
	return err
}

// NOTE: The optional SDK interfaces sdkAgent implements must match the
// methods of protocol.Agent, or the SDK would advertise methods the adapter
// cannot serve.
type sdkAgent struct {
	// NOTE: ready is closed once inner is set. The SDK starts reading
	// requests before the agent exists, so every method waits on it first.
	ready   chan struct{}
	connCtx context.Context
	inner   protocol.Agent
}

var (
	_ acp.Agent               = (*sdkAgent)(nil)
	_ acp.AgentLoader         = (*sdkAgent)(nil)
	_ acp.AgentSessionResumer = (*sdkAgent)(nil)
	_ acp.AgentSessionLister  = (*sdkAgent)(nil)
	_ acp.AgentSessionDeleter = (*sdkAgent)(nil)
	_ acp.AgentSessionCloser  = (*sdkAgent)(nil)
	_ acp.AgentSessionConfig  = (*sdkAgent)(nil)
)

func (a *sdkAgent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	<-a.ready
	resp, err := a.inner.Initialize(ctx, toInitialize(p))
	if err != nil {
		return acp.InitializeResponse{}, toRequestError(err)
	}
	return fromInitializeResponse(resp), nil
}

func (a *sdkAgent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	<-a.ready
	resp, err := a.inner.NewSession(ctx, toNewSession(p))
	if err != nil {
		return acp.NewSessionResponse{}, toRequestError(err)
	}
	if after := resp.AfterSent; after != nil {
		err := acp.AfterResponse(ctx, func(ctx context.Context) error {
			after(ctx)
			return nil
		})
		if err != nil {
			slog.Warn("Failed to schedule work after session/new", "error", err)
		}
	}
	return fromNewSession(resp), nil
}

func (a *sdkAgent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	<-a.ready
	resp, err := a.inner.LoadSession(ctx, toLoadSession(p))
	if err != nil {
		return acp.LoadSessionResponse{}, toRequestError(err)
	}
	return fromLoadSession(resp), nil
}

func (a *sdkAgent) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	<-a.ready
	resp, err := a.inner.ResumeSession(ctx, toResumeSession(p))
	if err != nil {
		return acp.ResumeSessionResponse{}, toRequestError(err)
	}
	return fromResumeSession(resp), nil
}

func (a *sdkAgent) ListSessions(ctx context.Context, p acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	<-a.ready
	resp, err := a.inner.ListSessions(ctx, toListSessions(p))
	if err != nil {
		return acp.ListSessionsResponse{}, toRequestError(err)
	}
	return fromListSessions(resp), nil
}

func (a *sdkAgent) DeleteSession(ctx context.Context, p acp.DeleteSessionRequest) (acp.DeleteSessionResponse, error) {
	<-a.ready
	if err := a.inner.DeleteSession(ctx, toDeleteSession(p)); err != nil {
		return acp.DeleteSessionResponse{}, toRequestError(err)
	}
	return acp.DeleteSessionResponse{}, nil
}

func (a *sdkAgent) CloseSession(ctx context.Context, p acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	<-a.ready
	if err := a.inner.CloseSession(ctx, toCloseSession(p)); err != nil {
		return acp.CloseSessionResponse{}, toRequestError(err)
	}
	return acp.CloseSessionResponse{}, nil
}

func (a *sdkAgent) SetSessionConfigOption(ctx context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	<-a.ready
	req, err := toSetConfigOption(p)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, toRequestError(err)
	}
	resp, err := a.inner.SetConfigOption(ctx, req)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, toRequestError(err)
	}
	return fromSetConfigOption(resp), nil
}

// NOTE: The SDK cancels the request ctx on session/cancel, on
// $/cancel_request and when a new prompt arrives for the same session. The
// adapter must see none of that, so it gets the connection ctx instead:
// a cancelled turn then returns a normal "cancelled" stop reason, and a
// second prompt queues instead of cutting the first one short.
func (a *sdkAgent) Prompt(_ context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	<-a.ready
	req, err := toPrompt(p)
	if err != nil {
		return acp.PromptResponse{}, toRequestError(err)
	}
	resp, err := a.inner.Prompt(a.connCtx, req)
	if err != nil {
		return acp.PromptResponse{}, toRequestError(err)
	}
	return fromPromptResponse(resp), nil
}

func (a *sdkAgent) Cancel(ctx context.Context, n acp.CancelNotification) error {
	<-a.ready
	return a.inner.Cancel(ctx, toCancel(n))
}
