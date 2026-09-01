package model

import (
	"errors"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newMcpConfigUI builds a UI whose Config() carries the given MCP entries,
// so mcpInfo's "for _, mcp := range m.com.Config().MCP.Sorted()" loop has
// something to iterate. newKeyRoutingUI's fixed Config has no MCP entries
// and cannot be overridden after the fact (a second .AnyTimes() on the same
// method never gets reached), so these tests build their own mock.
func newMcpConfigUI(t *testing.T, mcps config.MCPs) *UI {
	t.Helper()
	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{MCP: mcps}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	return newBusyUIWithWorkspace(ws)
}

func TestMcpInfo_NoConfiguredMCPsShowsNone(t *testing.T) {
	t.Parallel()

	m, _ := newKeyRoutingUI(t)
	m.mcpStates = map[string]mcp.ClientInfo{}

	out := m.mcpInfo(60, 5, false)

	require.Contains(t, out, "MCPs")
	require.Contains(t, out, "None")
}

func TestMcpInfo_RendersConnectedMCPWithCounts(t *testing.T) {
	t.Parallel()

	m := newMcpConfigUI(t, config.MCPs{"docs": {Type: config.MCPStdio}})
	m.mcpStates = map[string]mcp.ClientInfo{
		"docs": {
			Name:   "docs",
			State:  mcp.StateConnected,
			Counts: mcp.Counts{Tools: 3, Prompts: 1, Resources: 2},
		},
	}

	out := m.mcpInfo(60, 5, true)

	require.Contains(t, out, "docs")
	require.Contains(t, out, "3 tools")
	require.Contains(t, out, "1 prompts")
	require.Contains(t, out, "2 resources")
}

func TestMcpInfo_DockerMCPShowsFriendlyName(t *testing.T) {
	t.Parallel()

	m := newMcpConfigUI(t, config.MCPs{config.DockerMCPName: {Type: config.MCPStdio}})
	m.mcpStates = map[string]mcp.ClientInfo{
		config.DockerMCPName: {Name: config.DockerMCPName, State: mcp.StateConnected},
	}

	out := m.mcpInfo(60, 5, false)

	require.Contains(t, out, "Docker MCP")
}

func TestMcpList_ZeroMaxItemsReturnsEmpty(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	out := mcpList(&sty, []mcp.ClientInfo{{Name: "a", State: mcp.StateConnected}}, 40, 0)

	require.Empty(t, out)
}

func TestMcpList_TruncatesWhenExceedingMaxItems(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	clients := []mcp.ClientInfo{
		{Name: "a", State: mcp.StateConnected},
		{Name: "b", State: mcp.StateConnected},
		{Name: "c", State: mcp.StateConnected},
	}

	out := mcpList(&sty, clients, 40, 2)

	require.Contains(t, out, "…and 1 more")
}

func TestMcpList_EachStateRendersItsOwnDescription(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	tests := []struct {
		name    string
		client  mcp.ClientInfo
		wantSub string
	}{
		{"starting", mcp.ClientInfo{Name: "a", State: mcp.StateStarting}, "starting..."},
		{"error without detail", mcp.ClientInfo{Name: "a", State: mcp.StateError}, "error"},
		{"error with detail", mcp.ClientInfo{Name: "a", State: mcp.StateError, Error: errors.New("boom")}, "error: boom"},
		{"needs auth", mcp.ClientInfo{Name: "a", State: mcp.StateNeedsAuth}, "needs authentication"},
		{"disabled", mcp.ClientInfo{Name: "a", State: mcp.StateDisabled}, "disabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := mcpList(&sty, []mcp.ClientInfo{tc.client}, 40, 5)
			require.Contains(t, out, tc.wantSub)
		})
	}
}

func TestMcpCounts_FormatsAllPresentCounts(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	out := mcpCounts(&sty, mcp.Counts{Tools: 2, Prompts: 0, Resources: 4})

	require.Contains(t, out, "2 tools")
	require.NotContains(t, out, "prompts")
	require.Contains(t, out, "4 resources")
}

func TestMcpCounts_AllZeroIsEmpty(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	out := mcpCounts(&sty, mcp.Counts{})

	require.Empty(t, out)
}
