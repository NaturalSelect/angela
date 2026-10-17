package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestSubSessions(t *testing.T, entries []SubSessionEntry) *SubSessions {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s, Workspace: &configWorkspace{cfg: &config.Config{}}}
	return NewSubSessions(com, entries)
}

func subSessionIDs(s *SubSessions) []string {
	ids := make([]string, 0, len(s.list.FilteredItems()))
	for _, it := range s.list.FilteredItems() {
		ids = append(ids, it.(*SubSessionItem).ID())
	}
	return ids
}

func threeSubSessions() []SubSessionEntry {
	return []SubSessionEntry{
		{SessionID: "old-done", Agent: "explore", Task: "map the repo", State: SubSessionDone},
		{SessionID: "old-run", Agent: "explore", Task: "trace the login flow", State: SubSessionRunning},
		{SessionID: "new-done", Agent: "plan", Task: "design the API", State: SubSessionDone},
		{SessionID: "new-run", Agent: "plan", Task: "draft the schema", State: SubSessionWaiting},
		{SessionID: "failed", Agent: "explore", Task: "scan deps", State: SubSessionFailed},
	}
}

// Active sub-sessions are what the user most likely came for, so they come
// first; inside each group the newest leads.
func TestSubSessionsOrdersActiveFirstThenNewestFirst(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, threeSubSessions())

	require.Equal(t,
		[]string{"new-run", "old-run", "failed", "new-done", "old-done"},
		subSessionIDs(s),
	)
	require.Equal(t, "new-run", s.list.SelectedItem().(*SubSessionItem).ID())
}

func TestSubSessionsEnterReportsTheSelectedChild(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, threeSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, ActionEnterSubSession{SessionID: "old-run"}, action)
}

func TestSubSessionsEscapeCloses(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, threeSubSessions())

	require.IsType(t, ActionClose{}, s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestSubSessionsFilterMatchesAgentTaskAndState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query string
		want  []string
	}{
		{query: "schema", want: []string{"new-run"}},
		{query: "failed", want: []string{"failed"}},
		{query: "waiting", want: []string{"new-run"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()

			s := newTestSubSessions(t, threeSubSessions())
			for _, r := range tt.query {
				s.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
			}

			require.Equal(t, tt.want, subSessionIDs(s))
		})
	}
}

func TestSubSessionItemRendersStateAgentAndTask(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, []SubSessionEntry{
		{SessionID: "a", Agent: "plan", Task: "draft the schema", State: SubSessionWaiting},
	})

	out := ansi.Strip(s.list.FilteredItems()[0].Render(80))

	require.Contains(t, out, "◆")
	require.Contains(t, out, "Agent(plan)")
	require.Contains(t, out, "draft the schema")
	require.Contains(t, out, "waiting for you")
}

// A call whose input is still streaming has no name yet; the row must not
// print empty parentheses.
func TestSubSessionItemOmitsUnknownAgentName(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, []SubSessionEntry{
		{SessionID: "a", Task: "work", State: SubSessionRunning},
	})

	out := ansi.Strip(s.list.FilteredItems()[0].Render(80))

	require.NotContains(t, out, "()")
	require.Contains(t, out, "Agent")
}

func TestSubSessionsCommandIsOfferedOnlyWithASession(t *testing.T) {
	t.Parallel()

	sty := styles.AngelaTeal()
	for _, hasSession := range []bool{true, false} {
		c := &Commands{
			com: &common.Common{
				Styles:    &sty,
				Workspace: &configWorkspace{cfg: &config.Config{}},
			},
			hasSession: hasSession,
		}

		ids := commandIDs(c.defaultCommands())

		if hasSession {
			require.Contains(t, ids, "sub_sessions")
		} else {
			require.NotContains(t, ids, "sub_sessions")
		}
	}
}

// The command stays available while viewing a sub-agent transcript, so a
// nested sub-session can be reached without leaving the current level.
func TestSubSessionsCommandSurvivesViewingASubAgent(t *testing.T) {
	t.Parallel()

	sty := styles.AngelaTeal()
	c := &Commands{
		com: &common.Common{
			Styles:    &sty,
			Workspace: &configWorkspace{cfg: &config.Config{}},
		},
		hasSession: true,
		hasParent:  true,
	}

	require.Contains(t, commandIDs(c.defaultCommands()), "sub_sessions")
}

func backgroundSubSessions() []SubSessionEntry {
	return []SubSessionEntry{
		{SessionID: "bg-stopped", Agent: "explore", Task: "scan deps", State: SubSessionStopped},
		{SessionID: "bg-run", Agent: "explore", Task: "map the repo", State: SubSessionRunning, Stoppable: true},
		{SessionID: "plain-run", Agent: "explore", Task: "trace login", State: SubSessionRunning},
	}
}

func ctrlX() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}
}

// A stop is the user's own choice, so it reads differently from a failure
// and no longer asks for attention.
func TestSubSessionItemRendersAStoppedAgent(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, []SubSessionEntry{
		{SessionID: "a", Agent: "explore", Task: "scan deps", State: SubSessionStopped},
	})

	out := ansi.Strip(s.list.FilteredItems()[0].Render(80))

	require.Contains(t, out, "■")
	require.Contains(t, out, "stopped")
	require.NotContains(t, out, "✗")
	require.False(t, SubSessionStopped.active())
}

func TestSubSessionsListStoppedAgentsAfterTheActiveOnes(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())

	require.Equal(t, []string{"plain-run", "bg-run", "bg-stopped"}, subSessionIDs(s))
}

func TestSubSessionsFilterMatchesStopped(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	for _, r := range "stopped" {
		s.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	require.Equal(t, []string{"bg-stopped"}, subSessionIDs(s))
}

func TestSubSessionsCtrlXStopsTheSelectedBackgroundAgent(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "bg-run", s.list.SelectedItem().(*SubSessionItem).ID())

	action := s.HandleMsg(ctrlX())

	require.Equal(t, ActionStopSubSession{SessionID: "bg-run"}, action)
}

// Only a running background agent can be stopped; the key does nothing on
// any other row, and does not reach the filter box.
func TestSubSessionsCtrlXIgnoresRowsThatCannotBeStopped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		down int
	}{
		{"a running agent that is not in the background", 0},
		{"an agent that was already stopped", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newTestSubSessions(t, backgroundSubSessions())
			for range tt.down {
				s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
			}

			action := s.HandleMsg(ctrlX())

			require.Nil(t, action)
			require.Empty(t, s.input.Value())
			require.Len(t, subSessionIDs(s), 3, "the list must not be filtered")
		})
	}
}

func TestSubSessionsHelpOffersStopOnlyOnAStoppableRow(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())

	require.NotContains(t, helpKeys(s.ShortHelp()), "ctrl+x", "the first row is not stoppable")
	require.NotContains(t, helpKeys(s.FullHelp()[0]), "ctrl+x")

	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	require.Contains(t, helpKeys(s.ShortHelp()), "ctrl+x")
	require.Contains(t, helpKeys(s.FullHelp()[0]), "ctrl+x")
}
