package backend

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestBackendPermission_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	tests := []struct {
		name string
		call func(t *testing.T) error
	}{
		{"GrantPermission", func(t *testing.T) error {
			_, err := b.GrantPermission("nope", proto.PermissionGrant{Action: proto.PermissionAllow})
			return err
		}},
		{"SetSessionUnattended", func(t *testing.T) error {
			return b.SetSessionUnattended("nope", "s1", true)
		}},
		{"SetPermissionMode", func(t *testing.T) error {
			return b.SetPermissionMode("nope", "yolo")
		}},
		{"GetPermissionMode", func(t *testing.T) error {
			_, err := b.GetPermissionMode("nope")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.call(t), ErrWorkspaceNotFound)
		})
	}
}

// TestBackendPermission_GrantPermission drives every proto.PermissionAction
// branch against a real permission.Service. None of these IDs have a
// pending request registered, so a real, non-tautological "false" comes
// back from the service for the three valid actions; the fourth case
// checks the backend's own validation for an action the service never
// sees.
func TestBackendPermission_GrantPermission(t *testing.T) {
	tests := []struct {
		name    string
		action  proto.PermissionAction
		wantErr error
	}{
		{name: "allow", action: proto.PermissionAllow},
		{name: "allow_for_session", action: proto.PermissionAllowForSession},
		{name: "deny", action: proto.PermissionDeny},
		{name: "invalid_action", action: proto.PermissionAction("bogus"), wantErr: ErrInvalidPermissionAction},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, ws, _ := newPublishingWorkspace(t)

			resolved, err := b.GrantPermission(ws.ID, proto.PermissionGrant{
				Action: tc.action,
				Permission: proto.PermissionRequest{
					ID:        "perm-1",
					SessionID: "s1",
					ToolName:  "bash",
				},
			})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.False(t, resolved, "no pending request was ever registered for this ID")
		})
	}
}

// TestBackendPermission_ModeAndUnattended exercises the real
// permission.Service so the mode string returned by GetPermissionMode
// reflects what SetPermissionMode actually persisted, and
// SetSessionUnattended's effect is confirmed through the service's own
// SessionUnattended getter rather than assumed.
func TestBackendPermission_ModeAndUnattended(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	require.NoError(t, b.SetSessionUnattended(ws.ID, "s1", true))
	require.True(t, ws.Permissions.SessionUnattended("s1"))

	mode, err := b.GetPermissionMode(ws.ID)
	require.NoError(t, err)
	require.Equal(t, permission.ModeManual.String(), mode, "fresh workspace defaults to manual mode")

	require.NoError(t, b.SetPermissionMode(ws.ID, "yolo"))
	mode, err = b.GetPermissionMode(ws.ID)
	require.NoError(t, err)
	require.Equal(t, permission.ModeYolo.String(), mode)

	err = b.SetPermissionMode(ws.ID, "not-a-real-mode")
	require.ErrorIs(t, err, ErrInvalidPermissionMode)
}
