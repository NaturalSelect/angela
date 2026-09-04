package dialog

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// newTestCommands builds a fully wired Commands dialog the way the app
// does, backed by a configWorkspace so defaultCommands can read config.
func newTestCommands(t *testing.T, cfg *config.Config, sessionID string, hasSession bool, active *workspace.ActiveAgent, custom []commands.CustomCommand, prompts []commands.MCPPrompt) *Commands {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &configWorkspace{cfg: cfg},
	}
	c, err := NewCommands(com, sessionID, hasSession, false, false, false, false, active, custom, prompts)
	require.NoError(t, err)
	return c
}

// newCommandsForDefaults builds the menu the way the dialog does, with
// only the fields defaultCommands's session-independent gates read.
// Session/branch/parent gating is already covered by
// commands_branch_test.go.
func newCommandsForDefaults(t *testing.T, cfg *config.Config, hasSession bool, active *workspace.ActiveAgent, dockerAvailable *bool) *Commands {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	s := styles.CharmtonePantera()
	return &Commands{
		com: &common.Common{
			Styles:    &s,
			Workspace: &configWorkspace{cfg: cfg},
		},
		hasSession:         hasSession,
		active:             active,
		dockerMCPAvailable: dockerAvailable,
	}
}

// visibleCommandIDs collects the IDs of the CommandItems currently shown
// in the dialog's list, in display order.
func visibleCommandIDs(c *Commands) []string {
	var ids []string
	for _, it := range c.list.FilteredItems() {
		if ci, ok := it.(*CommandItem); ok && ci != nil {
			ids = append(ids, ci.ID())
		}
	}
	return ids
}

// findVisibleCommand returns the CommandItem with the given ID from the
// dialog's current list, or nil if not present.
func findVisibleCommand(c *Commands, id string) *CommandItem {
	for _, it := range c.list.FilteredItems() {
		if ci, ok := it.(*CommandItem); ok && ci != nil && ci.ID() == id {
			return ci
		}
	}
	return nil
}

func TestCommandType_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "System", SystemCommands.String())
	require.Equal(t, "User", UserCommands.String())
	require.Equal(t, "MCP", MCPPrompts.String())
}

// TestNewCommands_DefaultSystemCommands verifies the menu opens on the
// system list with the commands that need neither a session nor an
// active agent.
func TestNewCommands_DefaultSystemCommands(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	require.Equal(t, CommandsID, c.ID())
	require.Equal(t, SystemCommands, c.selected)

	ids := visibleCommandIDs(c)
	require.Contains(t, ids, "new_session")
	require.Contains(t, ids, "switch_session")
	require.Contains(t, ids, "switch_model")
	require.Contains(t, ids, "switch_agent")
	require.Contains(t, ids, "suspend")
	require.Contains(t, ids, "quit")
	require.NotContains(t, ids, "session_details", "no session is open yet")
}

// TestNewCommands_SessionGatedCommands verifies the commands that only
// make sense with an open session appear once one exists.
func TestNewCommands_SessionGatedCommands(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "sess-1", true, nil, nil, nil)
	ids := visibleCommandIDs(c)
	require.Contains(t, ids, "session_details")
	require.Contains(t, ids, "summarize")
	require.Contains(t, ids, "undo")
	require.Contains(t, ids, "scroll_to_bottom")
	require.Contains(t, ids, "toggle_compact")
}

// TestDefaultCommands_ActiveAgentGating pins which of the thinking
// toggle and the variant selector the menu offers, since they cover
// overlapping ground: a model that reasons without named levels gets
// the plain toggle, while named levels (reasoning or custom) get the
// selector instead.
func TestDefaultCommands_ActiveAgentGating(t *testing.T) {
	t.Parallel()

	t.Run("no active agent offers neither", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, false, nil, nil).defaultCommands())
		require.NotContains(t, ids, "toggle_thinking")
		require.NotContains(t, ids, "select_variant")
	})

	t.Run("reasoning without levels offers the thinking toggle", func(t *testing.T) {
		t.Parallel()
		active := &workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CanReason: true}}}
		items := newCommandsForDefaults(t, nil, false, active, nil).defaultCommands()
		ids := commandIDs(items)
		require.Contains(t, ids, "toggle_thinking")
		require.NotContains(t, ids, "select_variant")

		toggle := commandByID(items, "toggle_thinking")
		require.NotNil(t, toggle)
		require.Contains(t, toggle.Filter(), "Enable Thinking Mode")
	})

	t.Run("thinking already on flips the label to disable", func(t *testing.T) {
		t.Parallel()
		active := &workspace.ActiveAgent{
			CatwalkCfg: config.ProviderModel{Model: catwalk.Model{CanReason: true}},
			Think:      true,
		}
		items := newCommandsForDefaults(t, nil, false, active, nil).defaultCommands()
		toggle := commandByID(items, "toggle_thinking")
		require.NotNil(t, toggle)
		require.Contains(t, toggle.Filter(), "Disable Thinking Mode")
	})

	t.Run("reasoning levels offer the variant selector instead", func(t *testing.T) {
		t.Parallel()
		active := &workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{
			Model: catwalk.Model{CanReason: true, ReasoningLevels: []string{"low", "high"}},
		}}
		ids := commandIDs(newCommandsForDefaults(t, nil, false, active, nil).defaultCommands())
		require.NotContains(t, ids, "toggle_thinking")
		require.Contains(t, ids, "select_variant")
	})

	t.Run("custom variants without reasoning also offer the selector", func(t *testing.T) {
		t.Parallel()
		active := &workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{
			Model:    catwalk.Model{CanReason: false},
			Variants: map[string]config.SelectedModelOverride{"terse": {}},
		}}
		ids := commandIDs(newCommandsForDefaults(t, nil, false, active, nil).defaultCommands())
		require.NotContains(t, ids, "toggle_thinking")
		require.Contains(t, ids, "select_variant")
	})
}

// commandByID returns the item with the given ID, or nil.
func commandByID(items []*CommandItem, id string) *CommandItem {
	for _, it := range items {
		if it.ID() == id {
			return it
		}
	}
	return nil
}

// TestDefaultCommands_FilePickerGating verifies the file picker is only
// offered inside a session running a model that accepts images.
func TestDefaultCommands_FilePickerGating(t *testing.T) {
	t.Parallel()

	imageActive := &workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{SupportsImages: true}}}
	textActive := &workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{SupportsImages: false}}}

	t.Run("session on an image model offers the file picker", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, true, imageActive, nil).defaultCommands())
		require.Contains(t, ids, "file_picker")
	})

	t.Run("session on a text-only model hides it", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, true, textActive, nil).defaultCommands())
		require.NotContains(t, ids, "file_picker")
	})

	t.Run("no session hides it even on an image model", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, false, imageActive, nil).defaultCommands())
		require.NotContains(t, ids, "file_picker")
	})
}

// TestDefaultCommands_DockerMCPGating verifies the enable/disable
// Docker MCP commands are mutually exclusive and depend on both the
// configured state and the availability probe.
func TestDefaultCommands_DockerMCPGating(t *testing.T) {
	t.Parallel()

	trueVal, falseVal := true, false

	t.Run("already enabled shows only disable", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{MCP: config.MCPs{config.DockerMCPName: {}}}
		ids := commandIDs(newCommandsForDefaults(t, cfg, false, nil, nil).defaultCommands())
		require.Contains(t, ids, "disable_docker_mcp")
		require.NotContains(t, ids, "enable_docker_mcp")
	})

	t.Run("available and not enabled shows only enable", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, false, nil, &trueVal).defaultCommands())
		require.Contains(t, ids, "enable_docker_mcp")
		require.NotContains(t, ids, "disable_docker_mcp")
	})

	t.Run("known unavailable shows neither", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, false, nil, &falseVal).defaultCommands())
		require.NotContains(t, ids, "enable_docker_mcp")
		require.NotContains(t, ids, "disable_docker_mcp")
	})

	t.Run("unknown availability shows neither", func(t *testing.T) {
		t.Parallel()
		ids := commandIDs(newCommandsForDefaults(t, nil, false, nil, nil).defaultCommands())
		require.NotContains(t, ids, "enable_docker_mcp")
		require.NotContains(t, ids, "disable_docker_mcp")
	})
}

// TestDefaultCommands_TransparentBackgroundLabel verifies the toggle's
// label always names the action it performs, the opposite of the
// current setting.
func TestDefaultCommands_TransparentBackgroundLabel(t *testing.T) {
	t.Parallel()

	t.Run("unset config defaults to the disable label", func(t *testing.T) {
		t.Parallel()
		item := commandByID(newCommandsForDefaults(t, &config.Config{}, false, nil, nil).defaultCommands(), "toggle_transparent")
		require.NotNil(t, item)
		require.Contains(t, item.Filter(), "Disable Background Color")
	})

	t.Run("transparent already enabled flips to the enable label", func(t *testing.T) {
		t.Parallel()
		on := true
		cfg := &config.Config{Options: &config.Options{TUI: &config.TUIOptions{Transparent: &on}}}
		item := commandByID(newCommandsForDefaults(t, cfg, false, nil, nil).defaultCommands(), "toggle_transparent")
		require.NotNil(t, item)
		require.Contains(t, item.Filter(), "Enable Background Color")
	})
}

func TestCommands_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	require.Equal(t, ActionClose{}, c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestCommands_HandleMsg_Navigation verifies up/down wrap around the
// ends of the list.
func TestCommands_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	require.True(t, c.list.IsSelectedFirst())

	c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, c.list.IsSelectedLast(), "up from the first item must wrap to the last")

	c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, c.list.IsSelectedFirst(), "down from the last item must wrap to the first")
}

// TestCommands_HandleMsg_Select verifies enter dispatches the
// highlighted command's action.
func TestCommands_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	item, ok := c.list.SelectedItem().(*CommandItem)
	require.True(t, ok)

	action := c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, item.Action(), action)
}

// TestCommands_HandleMsg_ShortcutBypassesSelection verifies a key that
// matches a command's shortcut fires that command directly, regardless
// of which item is currently highlighted.
func TestCommands_HandleMsg_ShortcutBypassesSelection(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	c.list.SelectNext()

	action := c.HandleMsg(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	require.Equal(t, ActionNewSession{}, action)
}

// TestCommands_HandleMsg_TypingFiltersList verifies free text narrows
// the list through the shared fuzzy filter.
func TestCommands_HandleMsg_TypingFiltersList(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	var lastAction Action
	for _, r := range "quit" {
		lastAction = c.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, ok := lastAction.(ActionCmd)
	require.True(t, ok)

	ids := visibleCommandIDs(c)
	require.Equal(t, []string{"quit"}, ids)
}

// TestCommands_HandleMsg_TabCycling verifies tab is inert with only
// system commands, and cycles through user/MCP types once they exist.
func TestCommands_HandleMsg_TabCycling(t *testing.T) {
	t.Parallel()

	t.Run("without custom commands or MCP prompts, tab is a no-op", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equal(t, SystemCommands, c.selected)
		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		require.Equal(t, SystemCommands, c.selected)
	})

	t.Run("cycles through user and MCP command types", func(t *testing.T) {
		t.Parallel()
		custom := []commands.CustomCommand{{ID: "c1", Name: "Custom One", Content: "do it"}}
		prompts := []commands.MCPPrompt{{ID: "p1", Title: "Prompt One", PromptID: "prompt-1", ClientID: "client-1"}}
		c := newTestCommands(t, nil, "", false, nil, custom, prompts)

		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equal(t, UserCommands, c.selected)
		require.Contains(t, visibleCommandIDs(c), "custom_c1")

		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equal(t, MCPPrompts, c.selected)

		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equal(t, SystemCommands, c.selected, "tab wraps back to system commands")

		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		require.Equal(t, MCPPrompts, c.selected, "shift+tab wraps backward")
	})
}

// TestCommands_NextCommandType pins the forward cycle order across
// every combination of available command types.
func TestCommands_NextCommandType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		selected   CommandType
		hasCustom  bool
		hasPrompts bool
		want       CommandType
	}{
		{"system with nothing else wraps to itself", SystemCommands, false, false, SystemCommands},
		{"system advances to user when custom commands exist", SystemCommands, true, false, UserCommands},
		{"system skips to MCP when only prompts exist", SystemCommands, false, true, MCPPrompts},
		{"system prefers user over MCP when both exist", SystemCommands, true, true, UserCommands},
		{"user advances to MCP when prompts exist", UserCommands, true, true, MCPPrompts},
		{"user wraps to system without prompts", UserCommands, true, false, SystemCommands},
		{"MCP always wraps to system", MCPPrompts, true, true, SystemCommands},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &Commands{selected: tc.selected}
			if tc.hasCustom {
				c.customCommands = []commands.CustomCommand{{ID: "x"}}
			}
			if tc.hasPrompts {
				c.mcpPrompts = []commands.MCPPrompt{{ID: "y"}}
			}
			require.Equal(t, tc.want, c.nextCommandType())
		})
	}
}

// TestCommands_PreviousCommandType pins the backward cycle order
// across every combination of available command types.
func TestCommands_PreviousCommandType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		selected   CommandType
		hasCustom  bool
		hasPrompts bool
		want       CommandType
	}{
		{"system with nothing else wraps to itself", SystemCommands, false, false, SystemCommands},
		{"system steps back to MCP when prompts exist", SystemCommands, false, true, MCPPrompts},
		{"system steps back to user when only custom commands exist", SystemCommands, true, false, UserCommands},
		{"system prefers MCP over user when both exist", SystemCommands, true, true, MCPPrompts},
		{"user always steps back to system", UserCommands, true, true, SystemCommands},
		{"MCP steps back to user when custom commands exist", MCPPrompts, true, true, UserCommands},
		{"MCP steps back to system without custom commands", MCPPrompts, false, true, SystemCommands},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &Commands{selected: tc.selected}
			if tc.hasCustom {
				c.customCommands = []commands.CustomCommand{{ID: "x"}}
			}
			if tc.hasPrompts {
				c.mcpPrompts = []commands.MCPPrompt{{ID: "y"}}
			}
			require.Equal(t, tc.want, c.previousCommandType())
		})
	}
}

// TestCommands_HandleMsg_DockerMCPAvailabilityChecked verifies the
// availability result is recorded, the in-flight flag clears, and the
// highlighted command survives the resulting rebuild while viewing
// system commands, but a different view is left untouched.
func TestCommands_HandleMsg_DockerMCPAvailabilityChecked(t *testing.T) {
	t.Parallel()

	t.Run("preserves the selection while on system commands", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		for i, id := range visibleCommandIDs(c) {
			if id == "suspend" {
				c.list.SetSelected(i)
				break
			}
		}
		selected, ok := c.list.SelectedItem().(*CommandItem)
		require.True(t, ok)
		require.Equal(t, "suspend", selected.ID())

		action := c.HandleMsg(dockerMCPAvailabilityCheckedMsg{available: true})
		require.Nil(t, action)
		require.NotNil(t, c.dockerMCPAvailable)
		require.True(t, *c.dockerMCPAvailable)
		require.False(t, c.dockerMCPCheckInFlight)

		after, ok := c.list.SelectedItem().(*CommandItem)
		require.True(t, ok)
		require.Equal(t, "suspend", after.ID(), "the highlighted command must survive the rebuild")
	})

	t.Run("updates availability without rebuilding a non-system view", func(t *testing.T) {
		t.Parallel()
		custom := []commands.CustomCommand{{ID: "c1", Name: "Custom One"}}
		c := newTestCommands(t, nil, "", false, nil, custom, nil)
		c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equal(t, UserCommands, c.selected)

		c.HandleMsg(dockerMCPAvailabilityCheckedMsg{available: false})
		require.NotNil(t, c.dockerMCPAvailable)
		require.False(t, *c.dockerMCPAvailable)
		require.Equal(t, UserCommands, c.selected, "a non-system view must not be reset")
	})
}

// TestCommands_HandleMsg_SpinnerTick verifies the spinner only
// animates while the dialog is loading.
func TestCommands_HandleMsg_SpinnerTick(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	require.Nil(t, c.HandleMsg(spinner.TickMsg{}), "ticks are ignored while not loading")

	c.StartLoading()
	action := c.HandleMsg(spinner.TickMsg{})
	_, ok := action.(ActionCmd)
	require.True(t, ok, "a loading dialog must keep animating")
}

// TestCommands_InitialCmd verifies the Docker MCP probe fires exactly
// once: not when the result is already known, not when a check is
// already in flight, and it marks the flight flag when it does start.
func TestCommands_InitialCmd(t *testing.T) {
	t.Parallel()

	t.Run("known availability skips the check", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		known := true
		c.dockerMCPAvailable = &known
		require.Nil(t, c.InitialCmd())
	})

	t.Run("a check already in flight is not duplicated", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		c.dockerMCPAvailable = nil
		c.dockerMCPCheckInFlight = true
		require.Nil(t, c.InitialCmd())
	})

	t.Run("unknown availability starts a check", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		c.dockerMCPAvailable = nil
		c.dockerMCPCheckInFlight = false
		require.NotNil(t, c.InitialCmd())
		require.True(t, c.dockerMCPCheckInFlight)
	})
}

// TestCommands_SetCustomCommands verifies the stored commands refresh
// the visible list only when the user command type is showing.
func TestCommands_SetCustomCommands(t *testing.T) {
	t.Parallel()

	custom := []commands.CustomCommand{{ID: "c1", Name: "Custom One"}}

	t.Run("refreshes the list when user commands are showing", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		c.selected = UserCommands
		c.SetCustomCommands(custom)
		require.Equal(t, custom, c.customCommands)
		require.Contains(t, visibleCommandIDs(c), "custom_c1")
	})

	t.Run("stores the commands without touching a different view", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		before := visibleCommandIDs(c)
		c.SetCustomCommands(custom)
		require.Equal(t, custom, c.customCommands)
		require.Equal(t, before, visibleCommandIDs(c))
	})
}

// TestCommands_SetMCPPrompts verifies the stored prompts refresh the
// visible list only when the MCP command type is showing.
func TestCommands_SetMCPPrompts(t *testing.T) {
	t.Parallel()

	prompts := []commands.MCPPrompt{{ID: "p1", Title: "Prompt One", PromptID: "prompt-1"}}

	t.Run("refreshes the list when MCP prompts are showing", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		c.selected = MCPPrompts
		c.SetMCPPrompts(prompts)
		require.Equal(t, prompts, c.mcpPrompts)
		require.Len(t, visibleCommandIDs(c), 1)
	})

	t.Run("stores the prompts without touching a different view", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		before := visibleCommandIDs(c)
		c.SetMCPPrompts(prompts)
		require.Equal(t, prompts, c.mcpPrompts)
		require.Equal(t, before, visibleCommandIDs(c))
	})
}

// TestCommands_StartStopLoading verifies the spinner starts once,
// ignores a repeated start, and stops cleanly.
func TestCommands_StartStopLoading(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	require.False(t, c.loading)

	cmd := c.StartLoading()
	require.NotNil(t, cmd)
	require.True(t, c.loading)

	require.Nil(t, c.StartLoading(), "starting while already loading must not restart the spinner")

	c.StopLoading()
	require.False(t, c.loading)
}

// TestCommands_Draw covers the visible content across the default
// list, the radio view once alternate types exist, and the loading
// state.
func TestCommands_Draw(t *testing.T) {
	t.Parallel()

	t.Run("system commands", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		scr := uv.NewScreenBuffer(60, 24)
		c.Draw(scr, uv.Rect(0, 0, 60, 24))
		content := ansi.Strip(scr.Render())
		require.Contains(t, content, "Commands")
		require.Contains(t, content, "New Session")
	})

	t.Run("radio view appears once alternate command types exist", func(t *testing.T) {
		t.Parallel()
		custom := []commands.CustomCommand{{ID: "c1", Name: "Custom One"}}
		c := newTestCommands(t, nil, "", false, nil, custom, nil)
		scr := uv.NewScreenBuffer(60, 24)
		c.Draw(scr, uv.Rect(0, 0, 60, 24))
		content := ansi.Strip(scr.Render())
		require.Contains(t, content, "System")
		require.Contains(t, content, "User")
	})

	t.Run("loading state shows the generating prompt hint", func(t *testing.T) {
		t.Parallel()
		c := newTestCommands(t, nil, "", false, nil, nil, nil)
		c.StartLoading()
		scr := uv.NewScreenBuffer(60, 24)
		c.Draw(scr, uv.Rect(0, 0, 60, 24))
		content := ansi.Strip(scr.Render())
		require.Contains(t, content, "Generating Prompt...")
	})
}

// TestCommandsRadioView verifies the radio row is hidden with only
// system commands, and otherwise names each available type.
func TestCommandsRadioView(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()

	require.Empty(t, commandsRadioView(&s, SystemCommands, false, false))

	withUser := ansi.Strip(commandsRadioView(&s, SystemCommands, true, false))
	require.Contains(t, withUser, "System")
	require.Contains(t, withUser, "User")
	require.NotContains(t, withUser, "MCP")

	withBoth := ansi.Strip(commandsRadioView(&s, MCPPrompts, true, true))
	require.Contains(t, withBoth, "System")
	require.Contains(t, withBoth, "User")
	require.Contains(t, withBoth, "MCP")
}

// TestCommands_Help verifies the short help exposes the primary
// bindings and the full help includes tab cycling.
func TestCommands_Help(t *testing.T) {
	t.Parallel()

	c := newTestCommands(t, nil, "", false, nil, nil, nil)
	require.Len(t, c.ShortHelp(), 4)

	var flat []string
	for _, row := range c.FullHelp() {
		for _, b := range row {
			flat = append(flat, b.Help().Key)
		}
	}
	require.Contains(t, flat, c.keyMap.Select.Help().Key)
	require.Contains(t, flat, c.keyMap.Close.Help().Key)
	require.Contains(t, flat, c.keyMap.Tab.Help().Key)
}

// TestCommands_SetCommandItems_MCPPrompts verifies MCP prompt items
// dispatch ActionRunMCPPrompt with the prompt's identifying fields.
func TestCommands_SetCommandItems_MCPPrompts(t *testing.T) {
	t.Parallel()

	prompts := []commands.MCPPrompt{{
		ID:          "p1",
		Title:       "Prompt One",
		Description: "does a thing",
		PromptID:    "prompt-1",
		ClientID:    "client-1",
	}}
	c := newTestCommands(t, nil, "", false, nil, nil, prompts)
	c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, MCPPrompts, c.selected)

	items := c.list.FilteredItems()
	require.Len(t, items, 1)
	item, ok := items[0].(*CommandItem)
	require.True(t, ok)

	action, ok := item.Action().(ActionRunMCPPrompt)
	require.True(t, ok)
	require.Equal(t, "prompt-1", action.PromptID)
	require.Equal(t, "client-1", action.ClientID)
}

// TestCommands_SetCommandItems_CustomCommand verifies a plain custom
// command dispatches ActionRunCustomCommand with its content.
func TestCommands_SetCommandItems_CustomCommand(t *testing.T) {
	t.Parallel()

	custom := []commands.CustomCommand{{ID: "c1", Name: "Custom One", Content: "do the thing"}}
	c := newTestCommands(t, nil, "", false, nil, custom, nil)
	c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})

	item := findVisibleCommand(c, "custom_c1")
	require.NotNil(t, item)

	action, ok := item.Action().(ActionRunCustomCommand)
	require.True(t, ok)
	require.Equal(t, "do the thing", action.Content)
}
