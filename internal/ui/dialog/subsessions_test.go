package dialog

import (
	"image"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const (
	subSessionsTestW = 100
	subSessionsTestH = 30
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

// The panel has no separate command for stopping a background agent; typing
// what the user is after has to lead to the one list that does it.
func TestSubSessionsCommandIsFoundByTheWordsForStoppingAnAgent(t *testing.T) {
	t.Parallel()

	sty := styles.AngelaTeal()
	c := &Commands{
		com: &common.Common{
			Styles:    &sty,
			Workspace: &configWorkspace{cfg: &config.Config{}},
		},
		hasSession: true,
	}

	for _, item := range c.defaultCommands() {
		if item.ID() != "sub_sessions" {
			continue
		}
		for _, word := range []string{"stop", "background", "kill"} {
			require.Contains(t, item.Filter(), word)
		}
		return
	}
	t.Fatal("the sub-sessions command is missing")
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
	require.Contains(t, renderedRow(s, "bg-run"), "stopped", "the row shows the outcome at once")
	require.NotContains(t, renderedRow(s, "bg-run"), "Stop")
}

// renderedRow draws the row of one sub-session the way the list does.
func renderedRow(s *SubSessions, sessionID string) string {
	for _, it := range s.list.FilteredItems() {
		if it.(*SubSessionItem).ID() == sessionID {
			return ansi.Strip(it.Render(80))
		}
	}
	return ""
}

// drawSubSessions draws the dialog at a fixed size, which also records where
// the list landed for the mouse tests.
func drawSubSessions(s *SubSessions) uv.ScreenBuffer {
	scr := uv.NewScreenBuffer(subSessionsTestW, subSessionsTestH)
	s.Draw(scr, image.Rect(0, 0, subSessionsTestW, subSessionsTestH))
	return scr
}

// locateOnScreen returns the position of the first cell of text, so a test
// clicks where a user would see the words rather than where the code thinks
// they are.
func locateOnScreen(scr uv.ScreenBuffer, text string) (image.Point, bool) {
	for y := range subSessionsTestH {
		var line strings.Builder
		var columns []int
		for x := range subSessionsTestW {
			cell := scr.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				continue
			}
			for _, r := range cell.Content {
				line.WriteRune(r)
				columns = append(columns, x)
			}
		}
		if at := strings.Index(line.String(), text); at >= 0 {
			return image.Pt(columns[utf8.RuneCountInString(line.String()[:at])], y), true
		}
	}
	return image.Point{}, false
}

func findOnScreen(t *testing.T, scr uv.ScreenBuffer, text string) image.Point {
	t.Helper()
	at, ok := locateOnScreen(scr, text)
	require.True(t, ok, "%q is not on the screen", text)
	return at
}

func tab() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab} }

func enter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

func TestSubSessionItemShowsAStopButtonOnlyOnAStoppableRow(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())

	require.Contains(t, renderedRow(s, "bg-run"), "Stop")
	require.Contains(t, renderedRow(s, "bg-run"), "running")
	require.NotContains(t, renderedRow(s, "plain-run"), "Stop")
	require.NotContains(t, renderedRow(s, "bg-stopped"), "Stop")
}

// A row too narrow for both its text and the button keeps its text.
func TestSubSessionItemDropsTheStopButtonWhenThereIsNoRoomForIt(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	item := s.list.FilteredItems()[1].(*SubSessionItem)

	require.NotContains(t, ansi.Strip(item.Render(20)), "Stop")
	require.Contains(t, ansi.Strip(item.Render(80)), "Stop")
}

// Enter keeps entering the sub-session; tab moves the focus to the button
// first, and only then does enter end the agent.
func TestSubSessionsTabThenEnterStopsTheAgent(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	require.Equal(t, ActionEnterSubSession{SessionID: "bg-run"}, s.HandleMsg(enter()), "enter alone still enters")

	require.Nil(t, s.HandleMsg(tab()))
	action := s.HandleMsg(enter())

	require.Equal(t, ActionStopSubSession{SessionID: "bg-run"}, action)
	require.Contains(t, renderedRow(s, "bg-run"), "stopped")
}

func TestSubSessionsTabOnARowThatCannotBeStoppedChangesNothing(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	require.Equal(t, "plain-run", s.list.SelectedItem().(*SubSessionItem).ID())

	require.Nil(t, s.HandleMsg(tab()))

	require.Equal(t, ActionEnterSubSession{SessionID: "plain-run"}, s.HandleMsg(enter()))
	require.Empty(t, s.input.Value())
}

func TestSubSessionsTabTwiceReturnsTheFocusToTheRow(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	s.HandleMsg(tab())
	s.HandleMsg(tab())

	require.Equal(t, ActionEnterSubSession{SessionID: "bg-run"}, s.HandleMsg(enter()))
}

// The focus belongs to one row: moving away must not leave enter armed to
// stop an agent the user is no longer looking at.
func TestSubSessionsMovingTheSelectionReturnsTheFocusToTheRow(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	s.HandleMsg(tab())

	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})

	require.Equal(t, "bg-run", s.list.SelectedItem().(*SubSessionItem).ID())
	require.Equal(t, ActionEnterSubSession{SessionID: "bg-run"}, s.HandleMsg(enter()))
}

func TestSubSessionsTypingReturnsTheFocusToTheRow(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	s.HandleMsg(tab())

	s.HandleMsg(tea.KeyPressMsg{Code: 'b', Text: "b"})

	require.False(t, s.stopFocused)
}

// Stopping one agent leaves the list open so the next can be stopped too.
func TestSubSessionsStayUsableAfterAStop(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, []SubSessionEntry{
		{SessionID: "a", Agent: "explore", Task: "one", State: SubSessionRunning, Stoppable: true},
		{SessionID: "b", Agent: "explore", Task: "two", State: SubSessionRunning, Stoppable: true},
	})

	require.Equal(t, ActionStopSubSession{SessionID: "b"}, s.HandleMsg(ctrlX()))
	require.Nil(t, s.HandleMsg(ctrlX()), "a stopped row cannot be stopped again")

	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, ActionStopSubSession{SessionID: "a"}, s.HandleMsg(ctrlX()))
}

func TestSubSessionsClickingStopStopsTheAgent(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	scr := drawSubSessions(s)
	at := findOnScreen(t, scr, "Stop")

	action := s.HandleMsg(tea.MouseClickMsg{X: at.X, Y: at.Y, Button: uv.MouseLeft})

	require.Equal(t, ActionStopSubSession{SessionID: "bg-run"}, action)
	require.Contains(t, renderedRow(s, "bg-run"), "stopped")
}

// Every cell of the button counts, not only its first letter.
func TestSubSessionsClickingEitherEdgeOfTheStopButtonStopsTheAgent(t *testing.T) {
	t.Parallel()

	for _, dx := range []int{-1, 4} {
		s := newTestSubSessions(t, backgroundSubSessions())
		at := findOnScreen(t, drawSubSessions(s), "Stop")

		action := s.HandleMsg(tea.MouseClickMsg{X: at.X + dx, Y: at.Y, Button: uv.MouseLeft})

		require.Equal(t, ActionStopSubSession{SessionID: "bg-run"}, action, "dx=%d", dx)
	}
}

func TestSubSessionsClickingElsewhereOnTheRowOnlySelectsIt(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	scr := drawSubSessions(s)
	stop := findOnScreen(t, scr, "Stop")
	title := findOnScreen(t, scr, "map the repo")
	require.Equal(t, stop.Y, title.Y)

	action := s.HandleMsg(tea.MouseClickMsg{X: title.X, Y: title.Y, Button: uv.MouseLeft})

	require.Nil(t, action)
	require.Equal(t, "bg-run", s.list.SelectedItem().(*SubSessionItem).ID())
	require.Contains(t, renderedRow(s, "bg-run"), "running", "nothing was stopped")
}

func TestSubSessionsClickingAnotherMouseButtonOrOutsideTheListDoesNothing(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	at := findOnScreen(t, drawSubSessions(s), "Stop")

	require.Nil(t, s.HandleMsg(tea.MouseClickMsg{X: at.X, Y: at.Y, Button: uv.MouseRight}))
	require.Nil(t, s.HandleMsg(tea.MouseClickMsg{X: at.X, Y: 0, Button: uv.MouseLeft}), "above the list")
	require.Nil(t, s.HandleMsg(tea.MouseClickMsg{X: 0, Y: at.Y, Button: uv.MouseLeft}), "left of the list")
	require.Contains(t, renderedRow(s, "bg-run"), "running")
}

func TestSubSessionsHoveringStopChangesHowItIsDrawn(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	before := drawSubSessions(s)
	at := findOnScreen(t, before, "Stop")

	s.HandleMsg(tea.MouseMotionMsg{X: at.X, Y: at.Y})
	after := drawSubSessions(s)

	changed := false
	for x := at.X; x < at.X+4; x++ {
		b, a := before.CellAt(x, at.Y), after.CellAt(x, at.Y)
		if b != nil && a != nil && b.Style != a.Style {
			changed = true
			break
		}
	}
	require.True(t, changed, "hovering the button should change some cell's style")

	s.HandleMsg(tea.MouseMotionMsg{X: 0, Y: 0})
	restored := drawSubSessions(s)
	require.Equal(t, before.CellAt(at.X, at.Y).Style, restored.CellAt(at.X, at.Y).Style, "moving away undoes it")
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
	require.NotContains(t, helpKeys(s.ShortHelp()), "tab")
	require.NotContains(t, helpKeys(s.FullHelp()[0]), "ctrl+x")

	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	require.Contains(t, helpKeys(s.ShortHelp()), "ctrl+x")
	require.Contains(t, helpKeys(s.ShortHelp()), "tab")
	require.Contains(t, helpKeys(s.FullHelp()[0]), "ctrl+x")
	require.Contains(t, helpKeys(s.FullHelp()[0]), "tab")
}

// Enter ends the agent while the button has the focus, and the footer must
// say so before the user presses it.
func TestSubSessionsHelpRelabelsEnterWhileTheStopButtonHasTheFocus(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "enter sub-session", s.ShortHelp()[0].Help().Desc)

	s.HandleMsg(tab())

	require.Equal(t, "stop agent", s.ShortHelp()[0].Help().Desc)
}

// The footer must still fit the dialog with every stop hint on it, or the
// close hint is the first thing cut.
func TestSubSessionsHelpKeepsTheCloseHintOnAStoppableRow(t *testing.T) {
	t.Parallel()

	s := newTestSubSessions(t, backgroundSubSessions())
	s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	scr := drawSubSessions(s)

	_, ok := locateOnScreen(scr, "esc")
	require.True(t, ok, "the close hint is still in the footer")
}
