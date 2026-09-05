package model

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// openMCPServersDialog opens the MCP server management dialog. If the
// dialog is already open, it brings it to the front instead.
func (m *UI) openMCPServersDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.MCPServersID) {
		m.dialog.BringToFront(dialog.MCPServersID)
		return nil
	}

	m.dialog.OpenDialog(dialog.NewMCPServers(m.com))
	return nil
}

// toggleMCPServer starts or stops a configured MCP server at runtime
// without persisting the change to config, so it reverts to its
// configured state on the next restart. The direction follows the
// server's last known state: a connected or starting server is stopped;
// anything else (disabled, errored, needing auth) is (re)started.
func (m *UI) toggleMCPServer(name string) tea.Cmd {
	state, ok := m.mcpStates[name]
	running := ok && (state.State == mcp.StateConnected || state.State == mcp.StateStarting)

	return func() tea.Msg {
		var err error
		if running {
			err = m.com.Workspace.MCPDisable(name)
		} else {
			err = m.com.Workspace.MCPEnable(context.Background(), name)
		}
		if err != nil {
			return util.ReportError(err)()
		}
		return mcpStateChangedMsg{states: m.com.Workspace.MCPGetStates()}
	}
}
