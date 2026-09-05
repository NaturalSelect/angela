package dialog

import (
	"sort"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	uv "github.com/charmbracelet/ultraviolet"
)

// MCPServersID is the identifier for the MCP server management dialog.
const MCPServersID = "mcp_servers"

// MCPServers lists every configured MCP server together with its live
// connection state and lets the user toggle a server on or off. Toggling
// is runtime-only: it never writes to config, so a server reverts to its
// configured enabled/disabled state the next time the app starts.
type MCPServers struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	frame   *Frame
	metrics FrameMetrics

	keyMap struct {
		Toggle   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*MCPServers)(nil)

// mcpServerItems builds the sorted list of server items from a snapshot of
// live MCP states.
func mcpServerItems(d *MCPServers, states map[string]mcptools.ClientInfo) []list.FilterableItem {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]list.FilterableItem, len(names))
	for idx, name := range names {
		items[idx] = NewMCPServerItem(d.com.Styles, name, states[name])
	}
	return items
}

// NewMCPServers creates a new MCPServers dialog.
func NewMCPServers(com *common.Common) *MCPServers {
	d := &MCPServers{com: com}
	d.frame = NewFrame(com.Styles, FrameSpec{
		Title:     "MCP Servers",
		MaxWidth:  defaultDialogMaxWidth,
		MaxHeight: defaultDialogHeight,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h

	d.list = list.NewFilterableList(mcpServerItems(d, com.Workspace.MCPGetStates())...)
	d.list.Focus()
	d.list.SetSelected(0)

	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter"
	d.input.SetStyles(com.Styles.TextInput)
	d.input.Focus()

	d.keyMap.Toggle = key.NewBinding(
		key.WithKeys("space"),
		key.WithHelp("space", "toggle"),
	)
	d.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	d.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	d.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "choose"),
	)
	d.keyMap.Close = CloseKey

	return d
}

// ID implements Dialog.
func (d *MCPServers) ID() string {
	return MCPServersID
}

// SetStates rebuilds the server list from a fresh state snapshot,
// preserving the current selection by server name where possible. Called
// whenever the app's MCP states change while this dialog is open.
func (d *MCPServers) SetStates(states map[string]mcptools.ClientInfo) {
	var selectedName string
	if item, ok := d.list.SelectedItem().(*MCPServerItem); ok && item != nil {
		selectedName = item.ID()
	}

	d.list.SetItems(mcpServerItems(d, states)...)

	if selectedName == "" {
		return
	}
	for idx, it := range d.list.FilteredItems() {
		if item, ok := it.(*MCPServerItem); ok && item != nil && item.ID() == selectedName {
			d.list.SetSelected(idx)
			d.list.ScrollToSelected()
			break
		}
	}
}

// HandleMsg implements Dialog.
func (d *MCPServers) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Previous):
			d.list.Focus()
			if d.list.IsSelectedFirst() {
				d.list.SelectLast()
			} else {
				d.list.SelectPrev()
			}
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Next):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
			} else {
				d.list.SelectNext()
			}
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Toggle):
			if item, ok := d.list.SelectedItem().(*MCPServerItem); ok && item != nil {
				return ActionToggleMCPServer{Name: item.ID()}
			}
		default:
			var cmd tea.Cmd
			d.input, cmd = d.input.Update(msg)
			value := d.input.Value()
			d.list.SetFilter(value)
			d.list.ScrollToTop()
			d.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (d *MCPServers) Cursor() *tea.Cursor {
	return InputCursor(d.com.Styles, d.input.Cursor())
}

// ShortHelp implements [help.KeyMap].
func (d *MCPServers) ShortHelp() []key.Binding {
	return []key.Binding{
		d.keyMap.UpDown,
		d.keyMap.Toggle,
		d.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (d *MCPServers) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{d.keyMap.UpDown, d.keyMap.Toggle, d.keyMap.Close},
	}
}

// Draw implements [Dialog].
func (d *MCPServers) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	d.metrics = d.frame.Measure(area)
	d.input.SetWidth(d.frame.InputTextWidth(d.input, d.metrics.ContentWidth))
	listHeight, listTotalHeight, _ := d.frame.SizeList(d.list, d.metrics)

	rc := d.frame.Context(d.metrics)
	rc.AddPart(t.Dialog.InputPrompt.Render(d.input.View()))

	listView := t.Dialog.List.Height(d.list.Height()).Render(d.list.Render())
	listView = d.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, d.list.Offset())
	rc.AddPart(listView)
	rc.Help = d.frame.RenderHelp(&d.help, d, d.metrics.ContentWidth)

	view := rc.Render()
	return d.frame.Draw(scr, area, view, d.Cursor())
}
