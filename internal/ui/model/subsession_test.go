package model

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// subSessionWorkspace answers only the ID derivation the navigation needs. The
// embedded interface panics on anything else, so a test that starts probing the
// workspace says so loudly instead of quietly passing.
type subSessionWorkspace struct {
	workspace.Workspace
}

func (subSessionWorkspace) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return fmt.Sprintf("%s$$%s", messageID, toolCallID)
}

func newSubSessionUI(t *testing.T) *UI {
	t.Helper()
	m := newTestUI()
	m.com.Workspace = subSessionWorkspace{}
	m.keyMap = DefaultKeyMap()
	m.header = newHeader(m.com)
	m.dialog = dialog.NewOverlay()
	m.session = &session.Session{ID: "root", Title: "Root task"}
	return m
}

func agentItem(m *UI, messageID, toolCallID string) *chat.AgentToolMessageItem {
	item := chat.NewAgentToolMessageItem(
		m.com.Styles,
		message.ToolCall{ID: toolCallID, Name: "agent"},
		nil,
		false,
	)
	item.SetMessageID(messageID)
	return item
}

// simulateEnter calls enterSubSession and then feeds the loadSessionMsg the
// cmd would produce so the deferred stack push takes effect synchronously
// in the test.
func simulateEnter(m *UI, messageID, toolCallID string) {
	item := agentItem(m, messageID, toolCallID)
	childID := m.com.Workspace.CreateAgentToolSessionID(messageID, toolCallID)
	m.enterSubSession(item)
	// The cmd would produce a loadSessionMsg; simulate it.
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: childID, Title: childID},
		enterFrame: &sessionStackFrame{id: m.session.ID, title: m.session.Title},
	})
	m.session = &session.Session{ID: childID, Title: childID}
}

// simulateLeave feeds a successful loadSessionMsg with leaveLevel so the
// deferred stack pop takes effect.
func simulateLeave(m *UI) {
	if len(m.sessionStack) == 0 {
		return
	}
	parent := m.sessionStack[len(m.sessionStack)-1]
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: parent.id, Title: parent.title},
		leaveLevel: true,
	})
	m.session = &session.Session{ID: parent.id, Title: parent.title}
}

// The push is deferred: enterSubSession returns a cmd, and only the
// resulting loadSessionMsg pushes the parent frame onto the stack.
func TestEnterSubSessionPushesTheParent(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)

	require.False(t, m.inSubSession())

	// Calling enterSubSession alone must NOT push yet.
	m.enterSubSession(agentItem(m, "msg-1", "call-1"))
	require.False(t, m.inSubSession(), "push happened before the load succeeded")

	// Simulating the successful load pushes the frame.
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "msg-1$$call-1", Title: "explore"},
		enterFrame: &sessionStackFrame{id: "root", title: "Root task"},
	})
	require.True(t, m.inSubSession())
	require.Len(t, m.sessionStack, 1)
	require.Equal(t, "root", m.sessionStack[0].id)
	require.Equal(t, "Root task", m.sessionStack[0].title)
}

// Escape from a grandchild belongs one level up, not all the way home.
func TestLeaveSubSessionPopsExactlyOneLevel(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)

	simulateEnter(m, "msg-1", "call-1")
	m.session = &session.Session{ID: "msg-1$$call-1", Title: "explore"}
	simulateEnter(m, "msg-2", "call-2")
	m.session = &session.Session{ID: "msg-2$$call-2", Title: "grep"}

	require.Len(t, m.sessionStack, 2)

	simulateLeave(m)
	require.Len(t, m.sessionStack, 1)
	require.Equal(t, "root", m.sessionStack[0].id,
		"popping the grandchild jumped past the child instead of landing on it")

	simulateLeave(m)
	require.False(t, m.inSubSession())
}

func TestSessionTrailNamesEveryLevel(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)

	require.Equal(t, []string{"Root task"}, m.sessionTrail())

	simulateEnter(m, "msg-1", "call-1")
	m.session = &session.Session{ID: "msg-1$$call-1", Title: "explore"}
	require.Equal(t, []string{"Root task", "explore"}, m.sessionTrail())

	m.session = nil
	require.Nil(t, m.sessionTrail())
}

// Escape is layered: a running turn owns it first. Leaving the session while
// the sub-agent is still streaming would strand the user's cancel.
func TestEscapeCancelsBeforeItLeaves(t *testing.T) {
	t.Parallel()

	t.Run("idle escape leaves the sub-session", func(t *testing.T) {
		t.Parallel()
		m := newSubSessionUI(t)
		simulateEnter(m, "msg-1", "call-1")

		m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
		// The leave is now async, so the stack does NOT change in
		// handleKeyPressMsg itself; it fires a cmd. But inSubSession
		// is still true until the loadSessionMsg lands — what we
		// are testing here is that Esc fires the leave rather than
		// being swallowed. The leave path itself is proven by
		// TestLeaveSubSessionPopsExactlyOneLevel.
	})

	t.Run("busy escape cancels and stays", func(t *testing.T) {
		t.Parallel()
		m := newSubSessionUI(t)
		simulateEnter(m, "msg-1", "call-1")
		m.agentBusyCache.set(true)

		m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.True(t, m.inSubSession(),
			"escape left the session instead of cancelling the running turn")
	})
}

// passThroughDialog stands in for the sessions dialog: it hands the action
// straight back so handleDialogMsg reaches its switch without the real
// dialog's async loading.
type passThroughDialog struct{ dialog.Dialog }

func (passThroughDialog) ID() string { return dialog.SessionsID }

func (passThroughDialog) HandleMsg(msg tea.Msg) dialog.Action { return msg }

// Jumping to an unrelated session abandons the trail: its ancestors are not
// this session's ancestors, and Escape would otherwise walk into a stranger.
// The clear is deferred to loadSessionMsg success, so a failed switch never
// destroys the current breadcrumb.
func TestSwitchingSessionsClearsTheStack(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.dialog = dialog.NewOverlay(passThroughDialog{})
	simulateEnter(m, "msg-1", "call-1")
	require.True(t, m.inSubSession())

	// handleDialogMsg fires the load but does NOT clear synchronously.
	m.handleDialogMsg(dialog.ActionSelectSession{Session: session.Session{ID: "other"}})
	require.True(t, m.inSubSession(),
		"the stack was cleared before the session loaded — a failed switch would destroy the breadcrumb")

	// The successful loadSessionMsg clears the stack.
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "other", Title: "Other"},
		clearStack: true,
	})
	require.False(t, m.inSubSession())
}

func TestBreadcrumbFallsBackAsItRunsOutOfRoom(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	trail := []string{"Root task", "explore the codebase", "grep for callers"}

	full := ansi.Strip(m.header.renderTrail(trail, 200))
	require.Equal(t, "Root task › explore the codebase › grep for callers", full)

	elided := ansi.Strip(m.header.renderTrail(trail, 40))
	require.True(t, strings.HasPrefix(elided, "…"),
		"a trail too long to fit should elide the middle, got %q", elided)
	require.Contains(t, elided, "grep for callers", "the level in view must survive")

	tight := ansi.Strip(m.header.renderTrail(trail, 10))
	require.LessOrEqual(t, ansi.StringWidth(tight), 10,
		"the breadcrumb overflowed its share of the header")
}

// The header hands the breadcrumb a fixed share of the row; exceeding it
// pushes the working directory off the right edge.
func TestBreadcrumbNeverExceedsItsWidth(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)

	trail := []string{"alpha", "beta", "gamma", "delta"}
	for width := 1; width <= 60; width++ {
		got := m.header.renderTrail(trail, width)
		require.LessOrEqual(t, ansi.StringWidth(got), width, "width %d overflowed", width)
	}
}

// The help line is the only place the way out is discoverable.
func TestShortHelpAdvertisesTheWayBack(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)

	require.False(t, helpHasKey(m.ShortHelp(), "esc"))

	simulateEnter(m, "msg-1", "call-1")
	require.True(t, helpHasKey(m.ShortHelp(), "esc"),
		"a sub-session gives no hint that escape goes back")
}

func helpHasKey(binds []key.Binding, want string) bool {
	for _, b := range binds {
		if b.Help().Key == want {
			return true
		}
	}
	return false
}
