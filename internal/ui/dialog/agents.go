package dialog

import (
	"errors"
	"sort"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

const (
	// AgentsID is the identifier for the agent selection dialog.
	AgentsID              = "agents"
	agentsDialogMaxWidth  = 60
	agentsDialogMinHeight = 8
	agentsDialogMaxHeight = 20
)

// Agents is a dialog for switching the session's primary agent. Only
// primary agents are listed: subagents are delegation targets reached
// through the agent tool, not something a session runs on.
type Agents struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

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
	a := &Agents{com: com}

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

	if err := a.setAgentItems(currentAgent); err != nil {
		return nil, err
	}

	return a, nil
}

// ID implements Dialog.
func (a *Agents) ID() string {
	return AgentsID
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
			return ActionSelectAgent{AgentID: agentItem.agentID}
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
	return InputCursor(a.com.Styles, a.input.Cursor())
}

// Draw implements [Dialog].
func (a *Agents) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := a.com.Styles
	width := max(0, min(agentsDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	a.input.SetWidth(dialogInputTextWidth(t, a.input, innerWidth))

	listTotalHeight := a.list.TotalHeight()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	desiredHeight := heightOffset + listTotalHeight
	maxAvailable := area.Dy() - t.Dialog.View.GetVerticalBorderSize()
	height := max(agentsDialogMinHeight, min(agentsDialogMaxHeight, desiredHeight, maxAvailable))

	listHeight, listTotalHeight, _ := sizeDialogList(t, a.list, innerWidth, height)

	rc := NewRenderContext(t, width)
	rc.Title = "Switch Agent"
	rc.AddPart(t.Dialog.InputPrompt.Render(a.input.View()))

	if a.list.Height() >= len(a.list.FilteredItems()) {
		a.list.ScrollToTop()
	} else {
		a.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(a.list.Height()).Render(a.list.Render())
	listView = joinScrollbar(t, listView, listHeight, listTotalHeight, listHeight, a.list.Offset())
	rc.AddPart(listView)
	rc.Help = renderDialogHelp(t, &a.help, a, innerWidth)

	view := rc.Render()

	cur := a.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
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

func (a *Agents) setAgentItems(currentAgent string) error {
	cfg := a.com.Config()
	if cfg == nil {
		return errors.New("configuration not available")
	}

	// An empty record means the session never switched, so it is on the
	// coder — the same fallback the coordinator applies per turn.
	if currentAgent == "" {
		currentAgent = config.AgentCoder
	}

	candidates := switchableAgents(cfg)
	if len(candidates) == 0 {
		return errors.New("no primary agents configured")
	}

	items := make([]list.FilterableItem, 0, len(candidates))
	selectedIndex := 0
	for i, agentCfg := range candidates {
		title := agentCfg.Name
		if title == "" {
			title = agentCfg.ID
		}
		items = append(items, &AgentItem{
			Versioned:   list.NewVersioned(),
			agentID:     agentCfg.ID,
			title:       title,
			description: agentCfg.Description,
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
	return nil
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
