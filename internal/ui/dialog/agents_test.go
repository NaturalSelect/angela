package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

// agentsWorkspace is the least workspace the agents dialog needs: only
// the config it reads to build the switchable-agent list.
type agentsWorkspace struct {
	workspace.Workspace

	cfg *config.Config
}

func (w *agentsWorkspace) Config() *config.Config { return w.cfg }

func hiddenAgent() *bool {
	h := true
	return &h
}

// agentsTestConfig builds a config with a mix of primary, hidden-primary,
// and subagent entries, so the switchable-agents filter has something to
// actually filter.
func agentsTestConfig() *config.Config {
	return &config.Config{
		Agents: map[string]config.Agent{
			config.AgentCoder: {ID: config.AgentCoder, Name: "Coder", Mode: config.AgentModePrimary},
			"reviewer":        {ID: "reviewer", Name: "Reviewer", Description: "Reviews code", Mode: config.AgentModePrimary},
			"ghost":           {ID: "ghost", Name: "Ghost", Mode: config.AgentModePrimary, Hidden: hiddenAgent()},
			"task":            {ID: "task", Name: "Task", Mode: config.AgentModeSubagent},
		},
	}
}

func newTestAgents(t *testing.T, currentAgent string) *Agents {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s, Workspace: &agentsWorkspace{cfg: agentsTestConfig()}}
	a, err := NewAgents(com, currentAgent)
	require.NoError(t, err)
	return a
}

// TestNewAgents_RequiresConfig pins the guard against a workspace that
// cannot answer Config() yet.
func TestNewAgents_RequiresConfig(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s, Workspace: &agentsWorkspace{cfg: nil}}
	_, err := NewAgents(com, "")
	require.ErrorContains(t, err, "configuration not available")
}

// TestNewAgents_RequiresAPrimaryAgent pins the guard against a config
// that resolved to no switchable agents at all.
func TestNewAgents_RequiresAPrimaryAgent(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s, Workspace: &agentsWorkspace{cfg: &config.Config{}}}
	_, err := NewAgents(com, "")
	require.ErrorContains(t, err, "no primary agents configured")
}

// TestSwitchableAgents_FiltersHiddenAndSubagents pins the exact
// membership and ordering the dialog list is built from: only visible
// primary agents, sorted by ID.
func TestSwitchableAgents_FiltersHiddenAndSubagents(t *testing.T) {
	t.Parallel()

	agents := switchableAgents(agentsTestConfig())

	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	require.Equal(t, []string{config.AgentCoder, "reviewer"}, ids,
		"hidden and subagent entries must be excluded, and the rest sorted by ID")
}

// TestNewAgents_SelectsCurrentAgent verifies the list opens with the
// session's current agent highlighted, and falls back to the coder when
// the session never switched.
func TestNewAgents_SelectsCurrentAgent(t *testing.T) {
	t.Parallel()

	t.Run("explicit current agent", func(t *testing.T) {
		t.Parallel()
		a := newTestAgents(t, "reviewer")
		item, ok := a.list.SelectedItem().(*AgentItem)
		require.True(t, ok)
		require.Equal(t, "reviewer", item.agentID)
	})

	t.Run("empty current agent falls back to coder", func(t *testing.T) {
		t.Parallel()
		a := newTestAgents(t, "")
		item, ok := a.list.SelectedItem().(*AgentItem)
		require.True(t, ok)
		require.Equal(t, config.AgentCoder, item.agentID)
	})
}

// TestAgents_ID verifies the dialog identifies itself for the overlay
// stack.
func TestAgents_ID(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, "")
	require.Equal(t, AgentsID, a.ID())
}

// TestAgents_HandleMsg_Close verifies the close key closes the dialog
// regardless of selection state.
func TestAgents_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, "")
	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, ActionClose{}, action)
}

// TestAgents_HandleMsg_Navigation verifies up/down wrap around the ends
// of the list rather than stopping there.
func TestAgents_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, config.AgentCoder)
	first, ok := a.list.SelectedItem().(*AgentItem)
	require.True(t, ok)
	require.Equal(t, config.AgentCoder, first.agentID)

	// Up from the first item wraps to the last.
	a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	last, ok := a.list.SelectedItem().(*AgentItem)
	require.True(t, ok)
	require.Equal(t, "reviewer", last.agentID)

	// Down from the last item wraps back to the first.
	a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	backToFirst, ok := a.list.SelectedItem().(*AgentItem)
	require.True(t, ok)
	require.Equal(t, config.AgentCoder, backToFirst.agentID)
}

// TestAgents_HandleMsg_Select verifies pressing enter dispatches the
// currently highlighted agent, not whatever agent the session runs on
// today.
func TestAgents_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, config.AgentCoder)
	a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	action := a.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionSelectAgent)
	require.True(t, ok)
	require.Equal(t, "reviewer", resp.AgentID)
}

// TestAgents_HandleMsg_TypingFilters verifies free text narrows the list
// via the shared fuzzy filter and resets the selection to the top match.
func TestAgents_HandleMsg_TypingFilters(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, config.AgentCoder)
	for _, r := range "review" {
		action := a.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
		_, ok := action.(ActionCmd)
		require.True(t, ok, "typing must report an ActionCmd for the input blink")
	}

	require.Len(t, a.list.FilteredItems(), 1)
	item, ok := a.list.SelectedItem().(*AgentItem)
	require.True(t, ok)
	require.Equal(t, "reviewer", item.agentID)
}

// TestAgents_Draw verifies the dialog renders its title and the visible
// agent names, marking the current agent distinctly.
func TestAgents_Draw(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, config.AgentCoder)
	scr := uv.NewScreenBuffer(60, 20)
	a.Draw(scr, uv.Rect(0, 0, 60, 20))

	content := ansi.Strip(scr.Render())
	require.Contains(t, content, "Switch Agent")
	require.Contains(t, content, "Coder")
	require.Contains(t, content, "Reviewer")
	require.Contains(t, content, "current")
}

// TestAgents_Help verifies the short and full help bindings include the
// bindings a user needs to operate the list.
func TestAgents_Help(t *testing.T) {
	t.Parallel()

	a := newTestAgents(t, "")

	short := a.ShortHelp()
	require.Len(t, short, 3)

	full := a.FullHelp()
	var flat []string
	for _, row := range full {
		for _, b := range row {
			flat = append(flat, b.Help().Key)
		}
	}
	require.Contains(t, flat, a.keyMap.Select.Help().Key)
	require.Contains(t, flat, a.keyMap.Close.Help().Key)
}

// TestAgentItem_FilterAndID verifies the filter text combines the title
// and description, and ID exposes the underlying agent ID.
func TestAgentItem_FilterAndID(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	item := &AgentItem{
		agentID:     "reviewer",
		title:       "Reviewer",
		description: "Reviews code",
		t:           &s,
	}
	require.Equal(t, "Reviewer Reviews code", item.Filter())
	require.Equal(t, "reviewer", item.ID())
}

// TestAgentItem_RenderShowsCurrentInsteadOfDescription verifies the
// current agent's row substitutes "current" for its description, so the
// active choice is unambiguous at a glance.
func TestAgentItem_RenderShowsCurrentInsteadOfDescription(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	current := &AgentItem{agentID: "coder", title: "Coder", description: "Writes code", isCurrent: true, t: &s}
	require.Contains(t, ansi.Strip(current.Render(40)), "current")
	require.NotContains(t, ansi.Strip(current.Render(40)), "Writes code")

	other := &AgentItem{agentID: "reviewer", title: "Reviewer", description: "Reviews code", t: &s}
	require.Contains(t, ansi.Strip(other.Render(40)), "Reviews code")
}

// TestAgentItem_SetFocusedAndSetMatchToleratesNoVersioned verifies the
// nil-guard: an item built without a Versioned helper (never bumped) is
// still safe to mutate.
func TestAgentItem_SetFocusedAndSetMatchToleratesNoVersioned(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	item := &AgentItem{agentID: "coder", title: "Coder", t: &s}

	require.NotPanics(t, func() { item.SetFocused(true) })
	require.NotPanics(t, func() { item.SetMatch(fuzzy.Match{Str: "x"}) })
}

// TestAgentItem_SetFocusedAndSetMatchDedupe pins the version-bump
// convention: a mutator only invalidates the render cache when the
// value actually changes.
func TestAgentItem_SetFocusedAndSetMatchDedupe(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	item := &AgentItem{agentID: "coder", title: "Coder", t: &s, Versioned: list.NewVersioned()}

	before := item.Version()
	item.SetFocused(true)
	require.Greater(t, item.Version(), before)
	afterFocus := item.Version()
	item.SetFocused(true)
	require.Equal(t, afterFocus, item.Version(), "an unchanged focus state must not bump")

	m := fuzzy.Match{Str: "coder", Index: 0, MatchedIndexes: []int{0, 1}}
	item.SetMatch(m)
	require.Greater(t, item.Version(), afterFocus)
	afterMatch := item.Version()
	item.SetMatch(m)
	require.Equal(t, afterMatch, item.Version(), "an identical match must not bump")
}
