package caelis

import (
	"context"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
)

type sdkClient struct {
	conn *acp.AgentSideConnection
}

var _ protocol.Client = sdkClient{}

func (c sdkClient) SessionUpdate(ctx context.Context, n protocol.SessionNotification) error {
	notification, err := fromSessionNotification(n)
	if err != nil {
		return err
	}
	return c.conn.SessionUpdate(ctx, notification)
}

func (c sdkClient) RequestPermission(ctx context.Context, r protocol.PermissionRequest) (protocol.PermissionResponse, error) {
	req, err := fromPermissionRequest(r)
	if err != nil {
		return protocol.PermissionResponse{}, err
	}
	resp, err := c.conn.RequestPermission(ctx, req)
	if err != nil {
		return protocol.PermissionResponse{}, err
	}
	return toPermissionResponse(resp), nil
}

func (c sdkClient) Elicit(ctx context.Context, r protocol.ElicitationRequest) (protocol.ElicitationResponse, error) {
	resp, err := c.conn.CreateElicitation(ctx, fromElicitationRequest(r))
	if err != nil {
		return protocol.ElicitationResponse{}, err
	}
	return toElicitationResponse(resp), nil
}
