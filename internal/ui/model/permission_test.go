package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newTestUIForPermissions builds a UI with a chat, dialog overlay, and
// common context sufficient to exercise handlePermissionNotification.
func newTestUIForPermissions() *UI {
	u := newTestUI()
	u.dialog = dialog.NewOverlay()
	return u
}

func TestHandlePermissionNotification_RemoteGrantClosesDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-1",
		ToolCallID: "tool-call-X",
		ToolName:   "bash",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))
	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-X",
		Granted:    true,
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"granted notification should close matching permissions dialog")
}

func TestHandlePermissionNotification_RemoteDenyClosesDialog(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-2",
		ToolCallID: "tool-call-Y",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-Y",
		Denied:     true,
	})

	require.False(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"denied notification should close matching permissions dialog")
}

func TestHandlePermissionNotification_InitialPendingDoesNotClose(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-3",
		ToolCallID: "tool-call-Z",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	// The initial notification published by permission.Request is
	// neither granted nor denied; it must not dismiss the dialog.
	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-Z",
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"initial pending notification must not close the dialog")
}

func TestHandlePermissionNotification_DifferentToolCallIDDoesNotClose(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	perm := permission.PermissionRequest{
		ID:         "perm-4",
		ToolCallID: "tool-call-A",
	}
	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, perm))

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: "tool-call-B",
		Granted:    true,
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"notification for unrelated tool call must not close the dialog")
}

// newTestUIForPermissionQueue builds on newTestUIForPermissions with a
// MockWorkspace whose Config() returns a real, non-nil config.
// Opening a queued request (via queuePermissionRequest or
// handlePermissionNotification advancing to nextPermissionRequest)
// chains into openPermissionsDialog, which reads Workspace.Config()
// to decide the diff mode, so a workspace that can answer it is
// needed once a second request actually gets queued and opened.
func newTestUIForPermissionQueue(t *testing.T) *UI {
	t.Helper()
	u := newTestUIForPermissions()
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{
		Options: &config.Options{TUI: &config.TUIOptions{}},
	}).AnyTimes()
	u.com.Workspace = ws
	return u
}

func TestQueuePermissionRequest_QueuesWhileDialogIsOpen(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissions()
	first := permission.PermissionRequest{ID: "perm-1", ToolCallID: "tool-call-1", ToolName: "bash"}
	second := permission.PermissionRequest{ID: "perm-2", ToolCallID: "tool-call-2", ToolName: "edit"}

	u.dialog.OpenDialogWithGrace(dialog.NewPermissions(u.com, first))
	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID))

	require.Nil(t, u.queuePermissionRequest(second))

	d := u.dialog.Dialog(dialog.PermissionsID)
	perm, ok := d.(*dialog.Permissions)
	require.True(t, ok)
	require.Equal(t, first.ToolCallID, perm.ToolCallID(),
		"queuing a second request must not replace the dialog already showing the first")

	require.Len(t, u.pendingPermissions, 1)
	require.Equal(t, second.ToolCallID, u.pendingPermissions[0].ToolCallID)
}

// TestActionPermissionResponse_OpensNextQueuedPermissionRequest mirrors
// update_routing_test.go's permissionClickDialog pattern: a fake
// dialog reporting dialog.PermissionsID turns a mouse click into the
// same dialog.ActionPermissionResponse a real "Allow" button would,
// isolating the routing fix from real button geometry (already
// covered by dialog.TestPermissions_MouseClickSelectsButton). It pins
// that answering the currently-open request reveals the one queued
// behind it instead of leaving the dialog empty.
func TestActionPermissionResponse_OpensNextQueuedPermissionRequest(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{
		Options: &config.Options{TUI: &config.TUIOptions{}},
	}).AnyTimes()

	first := permission.PermissionRequest{ID: "perm-1", ToolCallID: "tool-call-1", ToolName: "bash"}
	second := permission.PermissionRequest{ID: "perm-2", ToolCallID: "tool-call-2", ToolName: "edit"}
	ws.EXPECT().PermissionGrant(first).Return(true)

	m := newBusyUIWithWorkspace(ws)
	m.dialog = dialog.NewOverlay(permissionClickDialog{perm: first})
	require.Nil(t, m.queuePermissionRequest(second))
	require.Len(t, m.pendingPermissions, 1)

	m.Update(tea.MouseClickMsg{X: 5, Y: 5, Button: uv.MouseLeft})

	require.True(t, m.dialog.ContainsDialog(dialog.PermissionsID),
		"answering the first request must reveal the queued second one")
	d := m.dialog.Dialog(dialog.PermissionsID)
	perm, ok := d.(*dialog.Permissions)
	require.True(t, ok, "the next dialog must be a real permissions dialog for the queued request")
	require.Equal(t, second.ToolCallID, perm.ToolCallID())
	require.Empty(t, m.pendingPermissions)
}

// TestHandlePermissionNotification_RemoteResolutionOpensNextQueuedRequest
// covers the case where another client resolves the visible request
// remotely: the dialog it closes must hand off to whatever sub-agent
// request was queued behind it, exactly as answering it locally does.
func TestHandlePermissionNotification_RemoteResolutionOpensNextQueuedRequest(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissionQueue(t)
	first := permission.PermissionRequest{ID: "perm-1", ToolCallID: "tool-call-1"}
	second := permission.PermissionRequest{ID: "perm-2", ToolCallID: "tool-call-2"}

	require.Nil(t, u.queuePermissionRequest(first))
	require.Nil(t, u.queuePermissionRequest(second))
	require.Len(t, u.pendingPermissions, 1)

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: first.ToolCallID,
		Granted:    true,
	})

	require.True(t, u.dialog.ContainsDialog(dialog.PermissionsID),
		"a remote resolution of the visible request must advance to the queued one")
	d := u.dialog.Dialog(dialog.PermissionsID)
	perm, ok := d.(*dialog.Permissions)
	require.True(t, ok)
	require.Equal(t, second.ToolCallID, perm.ToolCallID())
	require.Empty(t, u.pendingPermissions)
}

// TestHandlePermissionNotification_RemoteResolutionPrunesQueuedSibling
// covers GrantPersistent sweeping a sibling sub-agent's request while
// it is still waiting in the queue, not yet displayed: the notification
// for it must drop it from pendingPermissions so it never resurfaces
// later as a stale, already-resolved prompt, without disturbing
// whichever request is currently on screen.
func TestHandlePermissionNotification_RemoteResolutionPrunesQueuedSibling(t *testing.T) {
	t.Parallel()

	u := newTestUIForPermissionQueue(t)
	shown := permission.PermissionRequest{ID: "perm-1", ToolCallID: "tool-call-1"}
	queued := permission.PermissionRequest{ID: "perm-2", ToolCallID: "tool-call-2"}

	require.Nil(t, u.queuePermissionRequest(shown))
	require.Nil(t, u.queuePermissionRequest(queued))
	require.Len(t, u.pendingPermissions, 1)

	u.handlePermissionNotification(permission.PermissionNotification{
		ToolCallID: queued.ToolCallID,
		Granted:    true,
	})

	require.Empty(t, u.pendingPermissions,
		"a sibling swept while still queued must be dropped, not shown later")
	d := u.dialog.Dialog(dialog.PermissionsID)
	perm, ok := d.(*dialog.Permissions)
	require.True(t, ok)
	require.Equal(t, shown.ToolCallID, perm.ToolCallID(),
		"the currently displayed request must be untouched by a notification for a different, queued one")
}
