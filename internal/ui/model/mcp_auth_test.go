package model

import (
	"context"
	"errors"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestIsAuthTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"context canceled", context.Canceled, true},
		{"context deadline exceeded", context.DeadlineExceeded, true},
		{"message wraps context canceled", errors.New("authorization cancelled: context canceled"), true},
		{"message wraps deadline exceeded", errors.New("oauth: context deadline exceeded"), true},
		{"authorization cancelled message", errors.New("authorization cancelled"), true},
		{"unrelated error", errors.New("connection refused"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isAuthTimeout(tc.err))
		})
	}
}

func TestAuthenticateMCP_Success(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().MCPAuthenticate(gomock.Any(), "docs").Return(nil)

	msg := m.authenticateMCP(context.Background(), "docs")()

	require.Equal(t, dialog.ActionMCPAuthComplete{Name: "docs"}, msg)
}

func TestAuthenticateMCP_TimeoutErrorIsRewrapped(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().MCPAuthenticate(gomock.Any(), "docs").Return(context.Canceled)

	msg := m.authenticateMCP(context.Background(), "docs")()

	errored, ok := msg.(dialog.ActionMCPAuthErrored)
	require.True(t, ok)
	require.Equal(t, "docs", errored.Name)
	require.EqualError(t, errored.Error, "authentication timed out")
}

func TestAuthenticateMCP_OtherErrorPassesThrough(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	wantErr := errors.New("boom")
	ws.EXPECT().MCPAuthenticate(gomock.Any(), "docs").Return(wantErr)

	msg := m.authenticateMCP(context.Background(), "docs")()

	require.Equal(t, dialog.ActionMCPAuthErrored{Name: "docs", Error: wantErr}, msg)
}

func TestOpenMCPAuthDialog_NoPendingIsNoOp(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	ws.EXPECT().MCPPendingAuth().Return(nil)

	cmd := m.openMCPAuthDialog()

	require.Nil(t, cmd)
	require.False(t, m.dialog.ContainsDialog(dialog.MCPAuthID))
}

func TestOpenMCPAuthDialog_OpensThenBringsToFront(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	pending := []mcp.PendingAuthServer{{Name: "docs", URL: "https://example.com"}}
	ws.EXPECT().MCPPendingAuth().Return(pending).Times(2)

	cmd := m.openMCPAuthDialog()
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.MCPAuthID))

	// A second call finds the dialog already open and brings it to front
	// instead of stacking a duplicate.
	cmd = m.openMCPAuthDialog()
	require.Nil(t, cmd)
}

func TestCheckPendingMCPAuth_ReturnsCurrentStates(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	states := map[string]mcp.ClientInfo{"docs": {Name: "docs", State: mcp.StateConnected}}
	ws.EXPECT().MCPGetStates().Return(states)

	msg := m.checkPendingMCPAuth()()

	changed, ok := msg.(mcpStateChangedMsg)
	require.True(t, ok)
	require.Equal(t, states, changed.states)
}
