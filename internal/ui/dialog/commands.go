package dialog

import (
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
)

// CommandsID is the identifier for the commands dialog.
const CommandsID = "commands"

// CommandType represents the type of commands being displayed.
type CommandType uint

// String returns the string representation of the CommandType.
func (c CommandType) String() string { return []string{"System", "User", "MCP"}[c] }

const (
	SystemCommands CommandType = iota
	UserCommands
	MCPPrompts
)

// Commands represents a dialog that shows available commands.
type dockerMCPAvailabilityCheckedMsg struct {
	available bool
}

type Commands struct {
	com    *common.Common
	keyMap struct {
		Select,
		UpDown,
		Next,
		Previous,
		Tab,
		ShiftTab,
		Close key.Binding
	}

	sessionID  string
	hasSession bool
	hasTodos   bool
	hasQueue   bool
	// inBranch gates the abort command. Abandoning a branch is only
	// meaningful from inside one, and offering it elsewhere would name a
	// session the user is not looking at.
	inBranch bool
	// hasParent gates the go-to-parent command. It is broader than
	// inBranch: it also offers the jump from an ordinary sub-agent
	// transcript, not only a branch.
	hasParent bool
	selected  CommandType
	// active is the agent the session runs on, or nil when it is not
	// known yet. The model-dependent commands (thinking, variants, file
	// picker) are gated on it rather than on the global config, so the
	// menu offers what this session can actually do.
	active *workspace.ActiveAgent

	spinner spinner.Model
	loading bool

	help  help.Model
	input textinput.Model
	list  *list.FilterableList

	frame   *Frame
	metrics FrameMetrics

	windowWidth int

	customCommands []commands.CustomCommand
	mcpPrompts     []commands.MCPPrompt

	dockerMCPAvailable     *bool
	dockerMCPCheckInFlight bool
}

var _ Dialog = (*Commands)(nil)

// NewCommands creates a new commands dialog.
func NewCommands(com *common.Common, sessionID string, hasSession, hasTodos, hasQueue, inBranch, hasParent bool, active *workspace.ActiveAgent, customCommands []commands.CustomCommand, mcpPrompts []commands.MCPPrompt) (*Commands, error) {
	c := &Commands{
		com:            com,
		selected:       SystemCommands,
		sessionID:      sessionID,
		hasSession:     hasSession,
		hasTodos:       hasTodos,
		hasQueue:       hasQueue,
		inBranch:       inBranch,
		hasParent:      hasParent,
		active:         active,
		customCommands: customCommands,
		mcpPrompts:     mcpPrompts,
	}

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()

	c.help = help

	c.frame = NewFrame(com.Styles, FrameSpec{
		Title:     "Commands",
		MaxWidth:  defaultDialogMaxWidth,
		MaxHeight: defaultDialogHeight,
	})

	c.list = list.NewFilterableList()
	c.list.Focus()
	c.list.SetSelected(0)

	c.input = textinput.New()
	c.input.SetVirtualCursor(false)
	c.input.Placeholder = "Type to filter"
	c.input.SetStyles(com.Styles.TextInput)
	c.input.Focus()

	c.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	c.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	c.keyMap.Next = key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "next item"),
	)
	c.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	c.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch selection"),
	)
	c.keyMap.ShiftTab = key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "switch selection prev"),
	)
	closeKey := CloseKey
	closeKey.SetHelp("esc", "cancel")
	c.keyMap.Close = closeKey

	if available, known := config.DockerMCPAvailabilityCached(); known {
		c.dockerMCPAvailable = &available
	}

	// Set initial commands
	c.setCommandItems(c.selected)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = com.Styles.Dialog.Spinner
	c.spinner = s

	return c, nil
}

// ID implements Dialog.
func (c *Commands) ID() string {
	return CommandsID
}

// HandleMsg implements [Dialog].
func (c *Commands) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case dockerMCPAvailabilityCheckedMsg:
		c.dockerMCPAvailable = &msg.available
		c.dockerMCPCheckInFlight = false
		if c.selected == SystemCommands {
			// Preserve the current selection across the rebuild to avoid reset
			var prevID string
			if item, ok := c.list.SelectedItem().(*CommandItem); ok && item != nil {
				prevID = item.id
			}
			c.setCommandItems(c.selected)
			if prevID != "" {
				for i, it := range c.list.FilteredItems() {
					if ci, ok := it.(*CommandItem); ok && ci != nil && ci.id == prevID {
						c.list.SetSelected(i)
						c.list.ScrollToSelected()
						break
					}
				}
			}
		}
		return nil
	case spinner.TickMsg:
		if c.loading {
			var cmd tea.Cmd
			c.spinner, cmd = c.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, c.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, c.keyMap.Previous):
			c.list.Focus()
			if c.list.IsSelectedFirst() {
				c.list.SelectLast()
			} else {
				c.list.SelectPrev()
			}
			c.list.ScrollToSelected()
		case key.Matches(msg, c.keyMap.Next):
			c.list.Focus()
			if c.list.IsSelectedLast() {
				c.list.SelectFirst()
			} else {
				c.list.SelectNext()
			}
			c.list.ScrollToSelected()
		case key.Matches(msg, c.keyMap.Select):
			if selectedItem := c.list.SelectedItem(); selectedItem != nil {
				if item, ok := selectedItem.(*CommandItem); ok && item != nil {
					return item.Action()
				}
			}
		case key.Matches(msg, c.keyMap.Tab):
			if len(c.customCommands) > 0 || len(c.mcpPrompts) > 0 {
				c.selected = c.nextCommandType()
				c.setCommandItems(c.selected)
			}
		case key.Matches(msg, c.keyMap.ShiftTab):
			if len(c.customCommands) > 0 || len(c.mcpPrompts) > 0 {
				c.selected = c.previousCommandType()
				c.setCommandItems(c.selected)
			}
		default:
			var cmd tea.Cmd
			for _, item := range c.list.FilteredItems() {
				if item, ok := item.(*CommandItem); ok && item != nil {
					if msg.String() == item.Shortcut() {
						return item.Action()
					}
				}
			}
			c.input, cmd = c.input.Update(msg)
			value := c.input.Value()
			c.list.SetFilter(value)
			c.list.ScrollToTop()
			c.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

func checkDockerMCPAvailabilityCmd() tea.Cmd {
	return func() tea.Msg {
		return dockerMCPAvailabilityCheckedMsg{available: config.RefreshDockerMCPAvailability()}
	}
}

func (c *Commands) InitialCmd() tea.Cmd {
	if c.dockerMCPAvailable != nil || c.dockerMCPCheckInFlight {
		return nil
	}
	c.dockerMCPCheckInFlight = true
	return checkDockerMCPAvailabilityCmd()
}

// Cursor returns the cursor position relative to the dialog.
func (c *Commands) Cursor() *tea.Cursor {
	return InputCursor(c.com.Styles, c.input.Cursor())
}

// commandsRadioView generates the command type selector radio buttons.
func commandsRadioView(sty *styles.Styles, selected CommandType, hasUserCmds bool, hasMCPPrompts bool) string {
	if !hasUserCmds && !hasMCPPrompts {
		return ""
	}

	selectedFn := func(t CommandType) string {
		if t == selected {
			return sty.Radio.On.Padding(0, 1).Render() + sty.Radio.Label.Render(t.String())
		}
		return sty.Radio.Off.Padding(0, 1).Render() + sty.Radio.Label.Render(t.String())
	}

	parts := []string{
		selectedFn(SystemCommands),
	}

	if hasUserCmds {
		parts = append(parts, selectedFn(UserCommands))
	}
	if hasMCPPrompts {
		parts = append(parts, selectedFn(MCPPrompts))
	}

	return strings.Join(parts, " ")
}

// Draw implements [Dialog].
func (c *Commands) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := c.com.Styles
	c.metrics = c.frame.Measure(area)
	if area.Dx() != c.windowWidth && c.selected == SystemCommands {
		c.windowWidth = area.Dx()
		// since some items in the list depend on width (e.g. toggle sidebar command),
		// we need to reset the command items when width changes
		c.setCommandItems(c.selected)
	}

	c.input.SetWidth(c.frame.InputTextWidth(c.input, c.metrics.ContentWidth))

	// This dialog renders no scrollbar, so the list keeps the full content
	// width rather than reserving a column for one.
	c.list.SetSize(c.metrics.ContentWidth, max(0, c.metrics.Height-c.frame.ListHeightOffset()))

	// Hide the shortcut hints uniformly when the widest would crowd names.
	applyInfoColumnVisibility(c.list.FilteredItems(), c.metrics.ContentWidth, commandInfoMaxPercent)

	c.frame.SetTitle("Commands", commandsRadioView(t, c.selected, len(c.customCommands) > 0, len(c.mcpPrompts) > 0))

	helpLine := c.frame.RenderHelp(&c.help, c, c.metrics.ContentWidth)
	if c.loading {
		helpLine = t.Dialog.HelpView.Width(c.metrics.ContentWidth).Render(c.spinner.View() + " Generating Prompt...")
	}

	inputView := t.Dialog.InputPrompt.Render(c.input.View())
	listView := t.Dialog.List.Height(c.list.Height()).Render(c.list.Render())

	view := c.frame.Render(c.metrics, []string{inputView, listView}, helpLine)

	cur := c.Cursor()
	return c.frame.Draw(scr, area, view, cur)
}

// ShortHelp implements [help.KeyMap].
// ShortHelp implements [help.KeyMap].
func (c *Commands) ShortHelp() []key.Binding {
	bindings := []key.Binding{}
	// Tab only does anything when there is a second or third tab to
	// cycle to (see the same guard in HandleMsg's Tab/ShiftTab cases),
	// so advertising it otherwise offers a key that does nothing.
	if len(c.customCommands) > 0 || len(c.mcpPrompts) > 0 {
		bindings = append(bindings, c.keyMap.Tab)
	}
	return append(bindings, c.keyMap.UpDown, c.keyMap.Select, c.keyMap.Close)
}

// FullHelp implements [help.KeyMap].
func (c *Commands) FullHelp() [][]key.Binding {
	row := []key.Binding{c.keyMap.Select, c.keyMap.Next, c.keyMap.Previous}
	if len(c.customCommands) > 0 || len(c.mcpPrompts) > 0 {
		row = append(row, c.keyMap.Tab)
	}
	return [][]key.Binding{
		row,
		{c.keyMap.Close},
	}
}

// nextCommandType returns the next command type in the cycle.
func (c *Commands) nextCommandType() CommandType {
	switch c.selected {
	case SystemCommands:
		if len(c.customCommands) > 0 {
			return UserCommands
		}
		if len(c.mcpPrompts) > 0 {
			return MCPPrompts
		}
		fallthrough
	case UserCommands:
		if len(c.mcpPrompts) > 0 {
			return MCPPrompts
		}
		fallthrough
	case MCPPrompts:
		return SystemCommands
	default:
		return SystemCommands
	}
}

// previousCommandType returns the previous command type in the cycle.
func (c *Commands) previousCommandType() CommandType {
	switch c.selected {
	case SystemCommands:
		if len(c.mcpPrompts) > 0 {
			return MCPPrompts
		}
		if len(c.customCommands) > 0 {
			return UserCommands
		}
		return SystemCommands
	case UserCommands:
		return SystemCommands
	case MCPPrompts:
		if len(c.customCommands) > 0 {
			return UserCommands
		}
		return SystemCommands
	default:
		return SystemCommands
	}
}

// setCommandItems sets the command items based on the specified command type.
func (c *Commands) setCommandItems(commandType CommandType) {
	c.selected = commandType

	commandItems := []list.FilterableItem{}
	switch c.selected {
	case SystemCommands:
		for _, cmd := range c.defaultCommands() {
			commandItems = append(commandItems, cmd)
		}
	case UserCommands:
		for _, cmd := range c.customCommands {
			var action Action
			if cmd.Skill != nil {
				action = ActionAttachSkill{ID: cmd.Skill.SkillFilePath, Name: cmd.Skill.Name}
			} else {
				action = ActionRunCustomCommand{
					Content:   cmd.Content,
					Arguments: cmd.Arguments,
					Skill:     cmd.Skill,
				}
			}
			item := NewCommandItem(c.com.Styles, "custom_"+cmd.ID, cmd.Name, "", action)
			if cmd.Skill != nil {
				item = item.WithDescription(cmd.Skill.Description)
			}
			commandItems = append(commandItems, item)
		}
	case MCPPrompts:
		for _, cmd := range c.mcpPrompts {
			action := ActionRunMCPPrompt{
				Title:       cmd.Title,
				Description: cmd.Description,
				PromptID:    cmd.PromptID,
				ClientID:    cmd.ClientID,
				Arguments:   cmd.Arguments,
			}
			commandItems = append(commandItems, NewCommandItem(c.com.Styles, toolnames.MCPPrefix+cmd.ID, cmd.PromptID, "", action))
		}
	}

	c.list.SetItems(commandItems...)
	c.list.SetFilter("")
	c.list.ScrollToTop()
	c.list.SetSelected(0)
	c.input.SetValue("")
}

// defaultCommands returns the list of default system commands.
func (c *Commands) defaultCommands() []*CommandItem {
	commands := []*CommandItem{
		NewCommandItem(c.com.Styles, "new_session", "New Session", "ctrl+n", ActionNewSession{}).WithAliases("clear"),
		NewCommandItem(c.com.Styles, "switch_session", "Sessions", "ctrl+s", ActionOpenDialog{SessionsID}),
		NewCommandItem(c.com.Styles, "switch_model", "Switch Model", "ctrl+l", ActionOpenDialog{ModelsID}),
		NewCommandItem(c.com.Styles, "switch_agent", "Switch Agent", "", ActionOpenDialog{AgentsID}).WithAliases("agent"),
		NewCommandItem(c.com.Styles, "manage_mcp", "Manage MCP Servers", "", ActionOpenDialog{MCPServersID}).WithAliases("mcp", "mcps"),
		NewCommandItem(c.com.Styles, "suspend", "Suspend", "ctrl+z", ActionSuspend{}),
	}

	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "session_details", "Session Details", "ctrl+d", ActionToggleDetails{}))
	}

	// Only show compact command if there's an active session
	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "summarize", "Summarize Session", "", ActionSummarize{SessionID: c.sessionID}).WithAliases("compact"))
	}

	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "undo", "Undo Last Turn", "", ActionUndo{SessionID: c.sessionID}).WithAliases("revert"))
	}

	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "scroll_to_bottom", "Scroll to Bottom", "ctrl+down", ActionScrollToBottom{}).WithAliases("bottom"))
	}

	if c.hasParent {
		commands = append(commands, NewCommandItem(c.com.Styles, "go_to_parent", "Go to Parent", "ctrl+up", ActionGoToParent{}).WithAliases("parent"))
	}

	if c.inBranch {
		commands = append(commands, NewCommandItem(c.com.Styles, "abort_branch", "Abort Branch", "", ActionAbortBranch{SessionID: c.sessionID}).WithAliases("abort"))
	}

	if c.active != nil {
		// A model that reasons without naming levels has one knob
		// and no presets to seed, so it keeps its own toggle.
		if c.active.CatwalkCfg.CanReason && len(c.active.CatwalkCfg.ReasoningLevels) == 0 {
			status := "Enable"
			if c.active.Think {
				status = "Disable"
			}
			commands = append(commands, NewCommandItem(c.com.Styles, "toggle_thinking", status+" Thinking Mode", "", ActionToggleThinking{}))
		}

		// Everything else picks a preset, reasoning levels included:
		// they seed variants of the same name.
		if len(c.active.CatwalkCfg.VariantNames()) > 0 {
			commands = append(commands, NewCommandItem(c.com.Styles, "select_variant", "Select Variant", "ctrl+e", ActionOpenDialog{
				DialogID: VariantsID,
			}))
		}
	}
	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "toggle_compact", "Toggle Dense Mode", "", ActionToggleCompactMode{}))
	}
	if c.hasSession && c.active != nil && c.active.CatwalkCfg.SupportsImages {
		commands = append(commands, NewCommandItem(c.com.Styles, "file_picker", "Open File Picker", "ctrl+f", ActionOpenDialog{
			DialogID: FilePickerID,
		}))
	}

	// Add external editor command if $EDITOR is available.
	//
	// TODO: Use [tea.EnvMsg] to get environment variable instead of os.Getenv;
	// because os.Getenv does IO is breaks the TEA paradigm and is generally an
	// antipattern.
	if os.Getenv("EDITOR") != "" {
		commands = append(commands, NewCommandItem(c.com.Styles, "open_external_editor", "Open External Editor", "ctrl+o", ActionExternalEditor{}))
	}

	// The remaining toggles are genuinely global options rather than
	// anything the session's agent owns.
	cfg := c.com.Config()

	// Add Docker MCP command if available and not already enabled.
	if !cfg.IsDockerMCPEnabled() && c.dockerMCPAvailable != nil && *c.dockerMCPAvailable {
		commands = append(commands, NewCommandItem(c.com.Styles, "enable_docker_mcp", "Enable Docker MCP Catalog", "", ActionEnableDockerMCP{}))
	}

	// Add disable Docker MCP command if it's currently enabled
	if cfg.IsDockerMCPEnabled() {
		commands = append(commands, NewCommandItem(c.com.Styles, "disable_docker_mcp", "Disable Docker MCP Catalog", "", ActionDisableDockerMCP{}))
	}

	// Sandbox restriction is irreversible for the life of the process, so
	// once it is active there is nothing left for the command to do.
	if !c.com.Workspace.IsInSandbox() {
		commands = append(commands, NewCommandItem(c.com.Styles, "sandbox", "Enter Sandbox", "", ActionOpenDialog{DialogID: SandboxID}))
	}

	// Add a command for selecting notification style via picker dialog.
	notificationLabel := "Notification Style"
	commands = append(commands, NewCommandItem(c.com.Styles, "select_notifications", notificationLabel, "", ActionOpenDialog{DialogID: NotificationsID}))

	commands = append(
		commands,
		NewCommandItem(c.com.Styles, "cycle_permission_mode", "Cycle Permission Mode", "shift+tab", ActionCyclePermissionMode{}),
		NewCommandItem(c.com.Styles, "toggle_help", "Toggle Help", "ctrl+g", ActionToggleHelp{}),
		NewCommandItem(c.com.Styles, "init", "Initialize Project", "", ActionInitializeProject{}),
	)

	// Add transparent background toggle.
	transparentLabel := "Disable Background Color"
	if cfg != nil && cfg.Options != nil && cfg.Options.TUI.Transparent != nil && *cfg.Options.TUI.Transparent {
		transparentLabel = "Enable Background Color"
	}
	commands = append(commands, NewCommandItem(c.com.Styles, "toggle_transparent", transparentLabel, "", ActionToggleTransparentBackground{}))

	commands = append(
		commands,
		NewCommandItem(c.com.Styles, "quit", "Quit", "ctrl+c", tea.QuitMsg{}).WithAliases("exit"),
	)

	return commands
}

// SetCustomCommands sets the custom commands and refreshes the view if user commands are currently displayed.
func (c *Commands) SetCustomCommands(customCommands []commands.CustomCommand) {
	c.customCommands = customCommands
	if c.selected == UserCommands {
		c.setCommandItems(c.selected)
	}
}

// SetMCPPrompts sets the MCP prompts and refreshes the view if MCP prompts are currently displayed.
func (c *Commands) SetMCPPrompts(mcpPrompts []commands.MCPPrompt) {
	c.mcpPrompts = mcpPrompts
	if c.selected == MCPPrompts {
		c.setCommandItems(c.selected)
	}
}

// StartLoading implements [LoadingDialog].
func (c *Commands) StartLoading() tea.Cmd {
	if c.loading {
		return nil
	}
	c.loading = true
	return c.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (c *Commands) StopLoading() {
	c.loading = false
}
