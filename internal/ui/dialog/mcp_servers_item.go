package dialog

import (
	"fmt"
	"strings"

	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

// MCPServerItem wraps an [mcptools.ClientInfo] to implement the [ListItem]
// interface.
type MCPServerItem struct {
	*list.Versioned
	name    string
	info    mcptools.ClientInfo
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

var _ ListItem = &MCPServerItem{}

// NewMCPServerItem creates a new MCPServerItem.
func NewMCPServerItem(t *styles.Styles, name string, info mcptools.ClientInfo) *MCPServerItem {
	return &MCPServerItem{
		Versioned: list.NewVersioned(),
		name:      name,
		info:      info,
		t:         t,
		cache:     make(map[int]string),
	}
}

// Finished implements list.Item. A state change always rebuilds the list
// with fresh items (see MCPServers.SetStates) rather than mutating one in
// place, so an existing item is render-stable outside of explicit
// SetFocused / SetMatch.
func (i *MCPServerItem) Finished() bool {
	return true
}

// Filter implements ListItem.
func (i *MCPServerItem) Filter() string {
	return i.name
}

// ID implements ListItem.
func (i *MCPServerItem) ID() string {
	return i.name
}

// State returns the server's current connection state.
func (i *MCPServerItem) State() mcptools.State {
	return i.info.State
}

// SetFocused implements ListItem.
func (i *MCPServerItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch implements ListItem.
func (i *MCPServerItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// statusText describes the server's current state and, once connected,
// what it offers.
func (i *MCPServerItem) statusText() string {
	switch i.info.State {
	case mcptools.StateStarting:
		return "starting..."
	case mcptools.StateConnected:
		counts := i.info.Counts
		var parts []string
		if counts.Tools > 0 {
			parts = append(parts, fmt.Sprintf("%d tools", counts.Tools))
		}
		if counts.Prompts > 0 {
			parts = append(parts, fmt.Sprintf("%d prompts", counts.Prompts))
		}
		if counts.Resources > 0 {
			parts = append(parts, fmt.Sprintf("%d resources", counts.Resources))
		}
		if len(parts) == 0 {
			return "connected"
		}
		return "connected, " + strings.Join(parts, " ")
	case mcptools.StateError:
		if i.info.Error != nil {
			return "error: " + i.info.Error.Error()
		}
		return "error"
	case mcptools.StateNeedsAuth:
		return "needs authentication"
	case mcptools.StateDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// Render implements ListItem.
func (i *MCPServerItem) Render(width int) string {
	title := i.name
	if i.name == config.DockerMCPName {
		title = "Docker MCP"
	}
	itemStyles := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(itemStyles, title, i.statusText(), i.focused, width, i.cache, &i.m)
}
