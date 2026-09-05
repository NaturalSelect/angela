package model

import (
	"errors"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestOpenMCPServersDialog_BringsExistingToFront(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.MCPServersID}, idOnlyDialog{id: dialog.QuitID})

	cmd := m.openMCPServersDialog()
	require.Nil(t, cmd)
	require.Equal(t, dialog.MCPServersID, m.dialog.DialogLast().ID(),
		"reopening an already-open MCP servers dialog must bring it to front, not stack a duplicate")
}

func TestOpenMCPServersDialog_OpensNewDialogWhenNotPresent(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().MCPGetStates().Return(map[string]mcp.ClientInfo{}).AnyTimes()
	m := newDialogUI(t, ws)

	cmd := m.openMCPServersDialog()
	require.Nil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.MCPServersID))
	require.IsType(t, &dialog.MCPServers{}, m.dialog.DialogLast(),
		"a fresh open must construct the real MCPServers dialog")
}

func TestToggleMCPServer_DisablesConnectedServer(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{
		"docs": {Name: "docs", State: mcp.StateConnected},
	}

	afterDisable := map[string]mcp.ClientInfo{"docs": {Name: "docs", State: mcp.StateDisabled}}
	ws.EXPECT().MCPDisable("docs").Return(nil)
	ws.EXPECT().MCPGetStates().Return(afterDisable)

	cmd := m.toggleMCPServer("docs")
	require.NotNil(t, cmd)
	msg := cmd()
	changed, ok := msg.(mcpStateChangedMsg)
	require.True(t, ok, "expected mcpStateChangedMsg, got %T", msg)
	require.Equal(t, afterDisable, changed.states)
}

func TestToggleMCPServer_DisablesStartingServer(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{
		"docs": {Name: "docs", State: mcp.StateStarting},
	}

	ws.EXPECT().MCPDisable("docs").Return(nil)
	ws.EXPECT().MCPGetStates().Return(map[string]mcp.ClientInfo{})

	msg := m.toggleMCPServer("docs")()
	_, ok := msg.(mcpStateChangedMsg)
	require.True(t, ok, "a starting server must be disabled, not (re)enabled")
}

func TestToggleMCPServer_EnablesNonRunningServer(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{
		"docs": {Name: "docs", State: mcp.StateDisabled},
	}

	afterEnable := map[string]mcp.ClientInfo{"docs": {Name: "docs", State: mcp.StateStarting}}
	ws.EXPECT().MCPEnable(gomock.Any(), "docs").Return(nil)
	ws.EXPECT().MCPGetStates().Return(afterEnable)

	msg := m.toggleMCPServer("docs")()
	changed, ok := msg.(mcpStateChangedMsg)
	require.True(t, ok, "expected mcpStateChangedMsg, got %T", msg)
	require.Equal(t, afterEnable, changed.states)
}

func TestToggleMCPServer_UnknownServerIsEnabled(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{}

	ws.EXPECT().MCPEnable(gomock.Any(), "unknown").Return(nil)
	ws.EXPECT().MCPGetStates().Return(map[string]mcp.ClientInfo{})

	msg := m.toggleMCPServer("unknown")()
	_, ok := msg.(mcpStateChangedMsg)
	require.True(t, ok, "a server absent from mcpStates must be treated as not running and enabled")
}

func TestToggleMCPServer_ReportsErrorFromDisable(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{
		"docs": {Name: "docs", State: mcp.StateConnected},
	}

	wantErr := errors.New("disable boom")
	ws.EXPECT().MCPDisable("docs").Return(wantErr)

	msg := m.toggleMCPServer("docs")()
	infoMsg, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected util.InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeError, infoMsg.Type)
	require.Equal(t, wantErr.Error(), infoMsg.Msg)
}

func TestToggleMCPServer_ReportsErrorFromEnable(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.mcpStates = map[string]mcp.ClientInfo{
		"docs": {Name: "docs", State: mcp.StateDisabled},
	}

	wantErr := errors.New("enable boom")
	ws.EXPECT().MCPEnable(gomock.Any(), "docs").Return(wantErr)

	msg := m.toggleMCPServer("docs")()
	infoMsg, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected util.InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeError, infoMsg.Type)
	require.Equal(t, wantErr.Error(), infoMsg.Msg)
}
