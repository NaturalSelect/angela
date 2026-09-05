package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/sandbox"
)

// IsInSandbox reports whether the workspace's process is currently
// confined by a sandbox.
func (c *Client) IsInSandbox(ctx context.Context, id string) (bool, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/sandbox", id), nil, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get sandbox status: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to get sandbox status: status code %d", rsp.StatusCode)
	}
	var result proto.SandboxStatusResponse
	if err := json.NewDecoder(rsp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode sandbox status: %w", err)
	}
	return result.InSandbox, nil
}

// EnterSandbox restricts the workspace's process according to cfg.
func (c *Client) EnterSandbox(ctx context.Context, id string, cfg sandbox.Config) error {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/sandbox/enter", id), nil, jsonBody(proto.EnterSandboxRequest{
		ReadWrite:    cfg.ReadWrite,
		ReadOnly:     cfg.ReadOnly,
		AllowNetwork: cfg.AllowNetwork,
	}), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return fmt.Errorf("failed to enter sandbox: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to enter sandbox: status code %d", rsp.StatusCode)
	}
	return nil
}
