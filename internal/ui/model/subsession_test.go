package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newSubSessionWorkspace answers only the ID derivation the navigation
// needs. Any other call fails the test (gomock's default for a method
// with no .EXPECT()), so a test that starts probing the workspace says so
// loudly instead of quietly passing.
func newSubSessionWorkspace(t *testing.T) *MockWorkspace {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().CreateAgentToolSessionID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(messageID, toolCallID string) string {
			return fmt.Sprintf("%s$$%s", messageID, toolCallID)
		}).AnyTimes()
	return ws
}

func newSubSessionUI(t *testing.T) *UI {
	t.Helper()
	m := newTestUI()
	m.com.Workspace = newSubSessionWorkspace(t)
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
	parent := sessionStackFrame{id: m.session.ID, title: m.session.Title}
	m.enterSubSession(item)
	// The cmd would produce a loadSessionMsg; simulate it.
	child := &session.Session{ID: childID, Title: childID, ParentSessionID: parent.id}
	m.Update(loadSessionMsg{session: child, enterFrame: &parent})
	m.session = child
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

// A mouse click on an Agent tool call drills into its sub-session the same
// way pressing enter (OpenSubSession) does: ui.go's DelayedClickMsg handling
// calls enterSubSession once Chat.HandleDelayedClick reports the click as
// handled on a selected agent tool item.
func TestDelayedClickOnAgentToolEntersSubSession(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	item := agentItem(m, "msg-1", "call-1")
	m.chat.SetMessages(item)
	m.updateLayoutAndSize()

	_, _ = m.chat.HandleMouseDown(0, 0)
	clickID := m.chat.pendingClickID

	_, cmd := m.Update(DelayedClickMsg{ClickID: clickID, ItemIdx: 0, X: 0, Y: 0})
	require.NotNil(t, cmd, "a click on an agent tool call must drill into its sub-session")
	require.False(t, m.inSubSession(), "the stack push is deferred until the load succeeds")

	// Simulate the load succeeding, as the cmd returned above would.
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "msg-1$$call-1", Title: "explore", ParentSessionID: "root"},
		enterFrame: &sessionStackFrame{id: "root", title: "Root task"},
	})
	require.True(t, m.inSubSession())
	require.Equal(t, "root", m.sessionStack[0].id)
}

// A click on an ordinary tool item still just expands it in place; it must
// not be mistaken for a drill-down since it has no sub-session behind it.
func TestDelayedClickOnPlainItemDoesNotEnterSubSession(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	item := &testExpandableItem{
		testMessageItem: testMessageItem{id: "a", text: "alpha"},
		clickHandled:    true,
	}
	m.chat.SetMessages(item)
	m.updateLayoutAndSize()

	_, _ = m.chat.HandleMouseDown(0, 0)
	clickID := m.chat.pendingClickID

	// tea.Batch always wraps its input in a non-nil cmd, even when
	// nothing was appended, so the meaningful check is that expansion
	// (not drill-down) is what ran and that no navigation took effect.
	m.Update(DelayedClickMsg{ClickID: clickID, ItemIdx: 0, X: 0, Y: 0})
	require.True(t, item.expanded, "non-agent items still expand on click")
	require.False(t, m.inSubSession())
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

// Esc is the cancel gesture; it must not navigate out of a
// sub-agent transcript.
func TestEscapeNoLongerLeavesASubSession(t *testing.T) {
	t.Parallel()

	m := newSubSessionUI(t)
	simulateEnter(m, "msg-1", "call-1")

	m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, m.inSubSession())
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

	full := ansi.Strip(m.header.renderTrail(trail, 200, 0))
	require.Equal(t, "Root task › explore the codebase › grep for callers", full)

	elided := ansi.Strip(m.header.renderTrail(trail, 40, 0))
	require.True(t, strings.HasPrefix(elided, "…"),
		"a trail too long to fit should elide the middle, got %q", elided)
	require.Contains(t, elided, "grep for callers", "the level in view must survive")

	tight := ansi.Strip(m.header.renderTrail(trail, 10, 0))
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
		got := m.header.renderTrail(trail, width, 0)
		require.LessOrEqual(t, ansi.StringWidth(got), width, "width %d overflowed", width)
	}
}
