package dialog

import (
	"errors"
	"image"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
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

// TestMCPServers_SetStatesWithNoPriorSelectionSkipsReselect verifies that
// when nothing was selected before a state refresh (e.g. an empty list),
// SetStates returns early instead of attempting to find and restore a
// selection that never existed.
func TestMCPServers_SetStatesWithNoPriorSelectionSkipsReselect(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{})
	require.Equal(t, -1, d.list.Selected())

	d.SetStates(map[string]mcptools.ClientInfo{
		"github": {Name: "github", State: mcptools.StateConnected},
		"fetch":  {Name: "fetch", State: mcptools.StateDisabled},
	})

	require.Equal(t, -1, d.list.Selected(), "nothing was selected before, so SetStates must not force a selection")
}

// TestMCPServers_NavigatePreviousStepsAndWraps verifies that the Previous
// key steps to the prior item, and wraps to the last item when the first
// one is currently selected.
func TestMCPServers_NavigatePreviousStepsAndWraps(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"a": {Name: "a", State: mcptools.StateConnected},
		"b": {Name: "b", State: mcptools.StateConnected},
		"c": {Name: "c", State: mcptools.StateConnected},
	})
	d.list.SetSelected(1) // "b"

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Nil(t, action)
	item, ok := d.list.SelectedItem().(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "a", item.ID(), "previous from the middle should step to the previous item")

	// Wrap around: previous from the first item selects the last.
	action = d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Nil(t, action)
	item, ok = d.list.SelectedItem().(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "c", item.ID(), "previous from the first item should wrap to the last")
}

// TestMCPServers_NavigateNextStepsAndWraps verifies that the Next key
// steps to the following item, and wraps to the first item when the
// last one is currently selected.
func TestMCPServers_NavigateNextStepsAndWraps(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"a": {Name: "a", State: mcptools.StateConnected},
		"b": {Name: "b", State: mcptools.StateConnected},
		"c": {Name: "c", State: mcptools.StateConnected},
	})
	d.list.SetSelected(1) // "b"

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, action)
	item, ok := d.list.SelectedItem().(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "c", item.ID(), "next from the middle should step to the next item")

	// Wrap around: next from the last item selects the first.
	action = d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, action)
	item, ok = d.list.SelectedItem().(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "a", item.ID(), "next from the last item should wrap to the first")
}

// TestMCPServers_ToggleNoOpWhenNothingSelected verifies that toggling on
// an empty list (nothing selected) does not produce an action.
func TestMCPServers_ToggleNoOpWhenNothingSelected(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{})
	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Nil(t, action)
}

// TestMCPServers_TypingIntoFilterNarrowsListAndResetsSelection verifies
// that keys not matching a binding fall through to the filter box,
// narrowing the visible list and resetting the selection to the top.
func TestMCPServers_TypingIntoFilterNarrowsListAndResetsSelection(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"alpha":  {Name: "alpha", State: mcptools.StateConnected},
		"widget": {Name: "widget", State: mcptools.StateConnected},
	})
	d.list.SetSelected(1) // "widget"

	action := d.HandleMsg(keyMsg('w'))
	_, ok := action.(ActionCmd)
	require.True(t, ok, "typing should feed the filter box and return an ActionCmd")
	require.Equal(t, "w", d.input.Value())

	items := d.list.FilteredItems()
	require.Len(t, items, 1, "only 'widget' should match the 'w' filter")
	item, ok := items[0].(*MCPServerItem)
	require.True(t, ok)
	require.Equal(t, "widget", item.ID())
	require.Equal(t, 0, d.list.Selected(), "filtering must reset the selection to the top")
}

// TestMCPServers_Cursor verifies that the dialog reports a cursor for
// its always-focused filter input.
func TestMCPServers_Cursor(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"github": {Name: "github", State: mcptools.StateConnected},
	})
	cur := d.Cursor()
	require.NotNil(t, cur, "the filter input is focused, so the dialog should own the cursor")
}

// TestMCPServers_HelpBindings verifies the short and full help bindings
// exposed by the dialog.
func TestMCPServers_HelpBindings(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, nil)

	require.Equal(t, []key.Binding{d.keyMap.UpDown, d.keyMap.Toggle, d.keyMap.Close}, d.ShortHelp())
	require.Equal(t, [][]key.Binding{{d.keyMap.UpDown, d.keyMap.Toggle, d.keyMap.Close}}, d.FullHelp())
}

// TestMCPServers_Draw verifies that drawing the dialog renders its
// title and list contents without error.
func TestMCPServers_Draw(t *testing.T) {
	t.Parallel()

	d := newTestMCPServers(t, map[string]mcptools.ClientInfo{
		"github": {Name: "github", State: mcptools.StateConnected, Counts: mcptools.Counts{Tools: 3}},
	})

	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, cur, "the focused filter input should own the cursor")

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "MCP Servers")
	require.Contains(t, view, "github")
}

// TestMCPServerItem_Filter verifies that Filter returns the server name
// used for fuzzy matching.
func TestMCPServerItem_Filter(t *testing.T) {
	t.Parallel()
	s := styles.CharmtonePantera()
	item := NewMCPServerItem(&s, "github", mcptools.ClientInfo{State: mcptools.StateConnected})
	require.Equal(t, "github", item.Filter())
}

// TestMCPServerItem_SetFocused verifies that SetFocused only bumps the
// version and drops the cache when the focus state actually changes.
func TestMCPServerItem_SetFocused(t *testing.T) {
	t.Parallel()
	s := styles.CharmtonePantera()
	item := NewMCPServerItem(&s, "github", mcptools.ClientInfo{State: mcptools.StateConnected})
	initial := item.Version()

	item.SetFocused(false) // already unfocused: no-op.
	require.Equal(t, initial, item.Version())

	item.SetFocused(true)
	require.True(t, item.focused)
	require.Greater(t, item.Version(), initial, "focusing must bump the version so the list cache invalidates")
	require.Nil(t, item.cache, "focusing must drop the cached render")
}

// TestMCPServerItem_SetMatch verifies that SetMatch only bumps the
// version and drops the cache when the match actually changes.
func TestMCPServerItem_SetMatch(t *testing.T) {
	t.Parallel()
	s := styles.CharmtonePantera()
	item := NewMCPServerItem(&s, "github", mcptools.ClientInfo{State: mcptools.StateConnected})
	initial := item.Version()

	item.SetMatch(fuzzy.Match{}) // same as the zero-value default: no-op.
	require.Equal(t, initial, item.Version())

	m := fuzzy.Match{Str: "github", Index: 0, MatchedIndexes: []int{0, 1}}
	item.SetMatch(m)
	require.Greater(t, item.Version(), initial, "a changed match must bump the version so the list cache invalidates")
	require.Nil(t, item.cache)
	require.Equal(t, m, item.m)
}

// TestMCPServerItem_StatusText verifies the status text for every
// mcptools.State value, including the unknown-state fallback.
func TestMCPServerItem_StatusText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info mcptools.ClientInfo
		want string
	}{
		{"starting", mcptools.ClientInfo{State: mcptools.StateStarting}, "starting..."},
		{"connected no counts", mcptools.ClientInfo{State: mcptools.StateConnected}, "connected"},
		{
			"connected all counts",
			mcptools.ClientInfo{State: mcptools.StateConnected, Counts: mcptools.Counts{Tools: 2, Prompts: 1, Resources: 3}},
			"connected, 2 tools 1 prompts 3 resources",
		},
		{"error with cause", mcptools.ClientInfo{State: mcptools.StateError, Error: errors.New("boom")}, "error: boom"},
		{"error without cause", mcptools.ClientInfo{State: mcptools.StateError}, "error"},
		{"needs auth", mcptools.ClientInfo{State: mcptools.StateNeedsAuth}, "needs authentication"},
		{"disabled", mcptools.ClientInfo{State: mcptools.StateDisabled}, "disabled"},
		{"unknown state", mcptools.ClientInfo{State: mcptools.State(99)}, "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := styles.CharmtonePantera()
			item := NewMCPServerItem(&s, "svc", tc.info)
			require.Equal(t, tc.want, item.statusText())
		})
	}
}

// TestMCPServerItem_RenderUsesDockerFriendlyName verifies that the
// built-in Docker MCP server renders under a friendly display name
// rather than its internal config key.
func TestMCPServerItem_RenderUsesDockerFriendlyName(t *testing.T) {
	t.Parallel()
	s := styles.CharmtonePantera()
	item := NewMCPServerItem(&s, config.DockerMCPName, mcptools.ClientInfo{State: mcptools.StateConnected})

	rendered := ansi.Strip(item.Render(40))
	require.Contains(t, rendered, "Docker MCP")
}

// TestMCPServerItem_RenderUsesRawNameForNonDockerServer verifies that
// an ordinary server renders under its own name and status text.
func TestMCPServerItem_RenderUsesRawNameForNonDockerServer(t *testing.T) {
	t.Parallel()
	s := styles.CharmtonePantera()
	item := NewMCPServerItem(&s, "github", mcptools.ClientInfo{State: mcptools.StateConnected, Counts: mcptools.Counts{Tools: 2}})

	rendered := ansi.Strip(item.Render(60))
	require.Contains(t, rendered, "github")
	require.Contains(t, rendered, "connected, 2 tools")
}
