package backend

import (
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
)

// GrantPermission grants, denies, or persistently grants a permission
// request. The returned bool reports whether this call resolved the
// pending request (true) or found it already resolved by a previous
// caller (false). A false return is not an error.
func (b *Backend) GrantPermission(workspaceID string, req proto.PermissionGrant) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}

	perm := permission.PermissionRequest{
		ID:          req.Permission.ID,
		SessionID:   req.Permission.SessionID,
		ToolCallID:  req.Permission.ToolCallID,
		ToolName:    req.Permission.ToolName,
		Description: req.Permission.Description,
		Action:      req.Permission.Action,
		Params:      req.Permission.Params,
		Path:        req.Permission.Path,
		DenyReason:  req.Permission.DenyReason,
	}

	switch req.Action {
	case proto.PermissionAllow:
		return ws.Permissions.Grant(perm), nil
	case proto.PermissionAllowForSession:
		return ws.Permissions.GrantPersistent(perm), nil
	case proto.PermissionDeny:
		return ws.Permissions.Deny(perm), nil
	default:
		return false, ErrInvalidPermissionAction
	}
}

// SetSessionUnattended records whether a session has anyone who could
// answer a permission prompt. A client driving a run headlessly marks
// its session so the gate refuses instead of blocking.
func (b *Backend) SetSessionUnattended(workspaceID, sessionID string, unattended bool) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	ws.Permissions.SetSessionUnattended(sessionID, unattended)
	return nil
}

// SetPermissionMode sets the workspace's permission mode.
func (b *Backend) SetPermissionMode(workspaceID, mode string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	parsed, ok := permission.ParsePermissionMode(mode)
	if !ok {
		return ErrInvalidPermissionMode
	}

	ws.Permissions.SetMode(parsed)
	return nil
}

// GetPermissionMode returns the workspace's current permission mode.
func (b *Backend) GetPermissionMode(workspaceID string) (string, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return "", err
	}

	return ws.Permissions.Mode().String(), nil
}
