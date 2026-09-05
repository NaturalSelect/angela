package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

// mcpStatesWorkspace is the least workspace the MCP servers dialog needs:
// a fixed snapshot of live MCP client states.
type mcpStatesWorkspace struct {
	workspace.Workspace
	states map[string]mcptools.ClientInfo
}

func (w *mcpStatesWorkspace) MCPGetStates() map[string]mcptools.ClientInfo {
	return w.states
}

func newTestMCPServers(t *testing.T, states map[string]mcptools.ClientInfo) *MCPServers {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &mcpStatesWorkspace{states: states},
	}
	return NewMCPServers(com)
}

func TestMCPServers_ListsConfiguredServersSorted(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"github": {Name: "github", State: mcptools.StateConnected, Counts: mcptools.Counts{Tools: 3}},
		"fetch":  {Name: "fetch", State: mcptools.StateDisabled},
	})

	require.Equal(t, MCPServersID, d.ID())
	items := d.list.FilteredItems()
	require.Len(t, items, 2)

	first, ok := items[0].(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "fetch", first.ID())

	second, ok := items[1].(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "github", second.ID())
}

func TestMCPServers_ToggleEmitsActionForSelectedServer(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"fetch":  {Name: "fetch", State: mcptools.StateDisabled},
		"github": {Name: "github", State: mcptools.StateConnected},
	})
	d.list.SetSelected(1) // github, second alphabetically

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	toggle, ok := action.(ActionToggleMCPServer)
	require.True(t, ok, "expected ActionToggleMCPServer, got %T", action)
	require.Equal(t, "github", toggle.Name)
}

func TestMCPServers_EscapeCloses(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"github": {Name: "github", State: mcptools.StateConnected},
	})

	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestMCPServers_SetStatesPreservesSelection verifies that refreshing the
// list after a toggle (as the UI does on every mcpStateChangedMsg) keeps
// the same server selected even though the item backing it is rebuilt
// from scratch.
func TestMCPServers_SetStatesPreservesSelection(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"fetch":  {Name: "fetch", State: mcptools.StateDisabled},
		"github": {Name: "github", State: mcptools.StateConnected},
	})
	d.list.SetSelected(1) // github

	d.SetStates(map[string]mcptools.ClientInfo{
		"fetch":  {Name: "fetch", State: mcptools.StateDisabled},
		"github": {Name: "github", State: mcptools.StateDisabled},
	})

	item, ok := d.list.SelectedItem().(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "github", item.ID())
	require.Equal(t, mcptools.StateDisabled, item.State())
}
