package dialog

import (
	"errors"
	"sort"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

const (
	// AgentsID is the identifier for the agent selection dialog.
	AgentsID = "agents"
	// AgentModelAgentsID is the identifier for the agent picker in the
	// "Switch Agent Model" flow, which lists every configured agent
	// rather than only the switchable primary ones.
	AgentModelAgentsID    = "agent-model-agents"
	agentsDialogMaxWidth  = 60
	agentsDialogMinHeight = 8
	agentsDialogMaxHeight = 20
)

// Agents is a dialog for picking an agent: either the session's
// primary agent (NewAgents), or the override target of a "Switch
// Agent Model" pick (NewAgentModelTarget). onSelect decides which
// action a pick reports, so both flows share one list, filter and
// navigation implementation.
type Agents struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	frame   *Frame
	metrics FrameMetrics

	id       string
	onSelect func(agentID string) Action

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// AgentItem represents one switchable agent.
type AgentItem struct {
	*list.Versioned
	agentID     string
	title       string
	description string
	isCurrent   bool
	t           *styles.Styles
	m           fuzzy.Match
	cache       map[int]string
	focused     bool
}

// Finished implements list.Item. Agent items are render-stable outside
// of explicit SetFocused / SetMatch.
func (a *AgentItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Agents)(nil)
	_ ListItem = (*AgentItem)(nil)
)

// NewAgents creates the agent selection dialog. currentAgent is the
// agent the session runs on today, so it can be marked and preselected.
func NewAgents(com *common.Common, currentAgent string) (*Agents, error) {
	cfg := com.Config()
	if cfg == nil {
		return nil, errors.New("configuration not available")
	}
	candidates := switchableAgents(cfg)
	if len(candidates) == 0 {
		return nil, errors.New("no primary agents configured")
	}
	return newAgents(com, AgentsID, "Switch Agent", currentAgent, candidates,
		func(agentID string) Action { return ActionSelectAgent{AgentID: agentID} })
}

// NewAgentModelTarget creates the agent picker for the "Switch Agent
// Model" command. Every configured agent is a valid override target,
// hidden ones included: an override applies wherever InstantiateAgent
// resolves that agent, whether that is a session's primary agent or
// one of Angela's own internal calls, so restricting the list the way
// NewAgents does would make those agents unreachable for this command
// specifically. Hidden agents keep an "internal" label so the
// distinction isn't lost.
func NewAgentModelTarget(com *common.Common, currentAgent string) (*Agents, error) {
	cfg := com.Config()
	if cfg == nil {
		return nil, errors.New("configuration not available")
	}
	candidates := allAgents(cfg)
	if len(candidates) == 0 {
		return nil, errors.New("no agents configured")
	}
	return newAgents(com, AgentModelAgentsID, "Switch Agent Model", currentAgent, candidates,
		func(agentID string) Action { return ActionSelectAgentModelTarget{AgentID: agentID} })
}

// newAgents builds the dialog plumbing shared by NewAgents and
// NewAgentModelTarget: only the identifier, title, candidate list and
// the action a pick reports differ between the two.
func newAgents(com *common.Common, id, title, currentAgent string, candidates []config.Agent, onSelect func(agentID string) Action) (*Agents, error) {
	a := &Agents{com: com, id: id, onSelect: onSelect}

	a.frame = NewFrame(com.Styles, FrameSpec{
		Title:     title,
		MaxWidth:  agentsDialogMaxWidth,
		MinHeight: agentsDialogMinHeight,
		MaxHeight: agentsDialogMaxHeight,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	a.help = h

	a.list = list.NewFilterableList()
	a.list.Focus()

	a.input = textinput.New()
	a.input.SetVirtualCursor(false)
	a.input.Placeholder = "Type to filter"
	a.input.SetStyles(com.Styles.TextInput)
	a.input.Focus()

	a.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	a.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	a.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	a.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	a.keyMap.Close = CloseKey

	a.setAgentItems(candidates, currentAgent)

	return a, nil
}

// ID implements Dialog.
func (a *Agents) ID() string {
	return a.id
}

// HandleMsg implements [Dialog].
func (a *Agents) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, a.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, a.keyMap.Previous):
			a.list.Focus()
			if a.list.IsSelectedFirst() {
				a.list.SelectLast()
				a.list.ScrollToBottom()
				break
			}
			a.list.SelectPrev()
			a.list.ScrollToSelected()
		case key.Matches(msg, a.keyMap.Next):
			a.list.Focus()
			if a.list.IsSelectedLast() {
				a.list.SelectFirst()
				a.list.ScrollToTop()
				break
			}
			a.list.SelectNext()
			a.list.ScrollToSelected()
		case key.Matches(msg, a.keyMap.Select):
			selectedItem := a.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			agentItem, ok := selectedItem.(*AgentItem)
			if !ok {
				break
			}
			return a.onSelect(agentItem.agentID)
		default:
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(msg)
			a.list.SetFilter(a.input.Value())
			a.list.ScrollToTop()
			a.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (a *Agents) Cursor() *tea.Cursor {
	cur := a.input.Cursor()
	if cur != nil {
		// textinput.Cursor() offsets X by rune count, not display width;
		// correct for double-width runes (CJK).
		value := []rune(a.input.Value())
		n := a.input.Position()
		cur.X += lipgloss.Width(string(value[:n])) - n
	}
	return InputCursor(a.com.Styles, cur)
}

// Draw implements [Dialog].
func (a *Agents) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := a.com.Styles
	a.metrics = a.frame.FitHeight(area, a.frame.ListHeightOffset()+a.list.TotalHeight())

	a.input.SetWidth(a.frame.InputTextWidth(a.input, a.metrics.ContentWidth))

	listHeight, listTotalHeight, _ := a.frame.SizeList(a.list, a.metrics)

	if a.list.Height() >= len(a.list.FilteredItems()) {
		a.list.ScrollToTop()
	} else {
		a.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(a.list.Height()).Render(a.list.Render())
	listView = a.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, a.list.Offset())

	view := a.frame.Render(a.metrics,
		[]string{t.Dialog.InputPrompt.Render(a.input.View()), listView},
		a.frame.RenderHelp(&a.help, a, a.metrics.ContentWidth),
	)

	cur := a.Cursor()
	return a.frame.Draw(scr, area, view, cur)
}

// ShortHelp implements [help.KeyMap].
func (a *Agents) ShortHelp() []key.Binding {
	return []key.Binding{
		a.keyMap.UpDown,
		a.keyMap.Select,
		a.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (a *Agents) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		a.keyMap.Select,
		a.keyMap.Next,
		a.keyMap.Previous,
		a.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

func (a *Agents) setAgentItems(candidates []config.Agent, currentAgent string) {
	// An empty record means the session never switched, so it is on the
	// coder — the same fallback the coordinator applies per turn.
	if currentAgent == "" {
		currentAgent = config.AgentCoder
	}

	items := make([]list.FilterableItem, 0, len(candidates))
	selectedIndex := 0
	for i, agentCfg := range candidates {
		title := agentCfg.Name
		if title == "" {
			title = agentCfg.ID
		}
		description := agentCfg.Description
		if agentCfg.IsHidden() {
			description = "internal · " + description
		}
		items = append(items, &AgentItem{
			Versioned:   list.NewVersioned(),
			agentID:     agentCfg.ID,
			title:       title,
			description: description,
			isCurrent:   agentCfg.ID == currentAgent,
			t:           a.com.Styles,
		})
		if agentCfg.ID == currentAgent {
			selectedIndex = i
		}
	}

	a.list.SetItems(items...)
	a.list.SetSelected(selectedIndex)
	a.list.ScrollToSelected()
}

// switchableAgents returns the agents a session can run on, sorted by ID
// so the list is stable across openings. Hidden agents back Angela's own
// internal calls and subagents are delegation targets; neither drives a
// session.
func switchableAgents(cfg *config.Config) []config.Agent {
	var agents []config.Agent
	for _, agentCfg := range cfg.Agents {
		if agentCfg.Mode != config.AgentModePrimary || agentCfg.IsHidden() {
			continue
		}
		agents = append(agents, agentCfg)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

// allAgents returns every configured agent, sorted by ID so the list
// is stable across openings. It backs the "Switch Agent Model" picker,
// which — unlike switchableAgents — has to reach hidden agents too.
// Disabled agents never reach cfg.Agents: ResolveAgents drops them at
// load time.
func allAgents(cfg *config.Config) []config.Agent {
	agents := make([]config.Agent, 0, len(cfg.Agents))
	for _, agentCfg := range cfg.Agents {
		agents = append(agents, agentCfg)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

// Filter returns the filter value for the agent item.
func (a *AgentItem) Filter() string {
	return a.title + " " + a.description
}

// ID returns the unique identifier for the agent.
func (a *AgentItem) ID() string {
	return a.agentID
}

// SetFocused sets the focus state of the agent item.
func (a *AgentItem) SetFocused(focused bool) {
	if a.focused == focused {
		return
	}
	a.cache = nil
	a.focused = focused
	if a.Versioned != nil {
		a.Bump()
	}
}

// SetMatch sets the fuzzy match for the agent item.
func (a *AgentItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(a.m, m) {
		return
	}
	a.cache = nil
	a.m = m
	if a.Versioned != nil {
		a.Bump()
	}
}

// Render returns the string representation of the agent item.
func (a *AgentItem) Render(width int) string {
	info := a.description
	if a.isCurrent {
		info = "current"
	}
	itemStyles := ListItemStyles{
		ItemBlurred:     a.t.Dialog.NormalItem,
		ItemFocused:     a.t.Dialog.SelectedItem,
		InfoTextBlurred: a.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: a.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(itemStyles, a.title, info, a.focused, width, a.cache, &a.m)
}
