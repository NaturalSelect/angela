package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
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
	m.attachments = attachments.New(attachments.NewRenderer(
		m.com.Styles.Attachments.Normal,
		m.com.Styles.Attachments.Deleting,
		m.com.Styles.Attachments.Image,
		m.com.Styles.Attachments.Text,
		m.com.Styles.Attachments.Skill,
		m.com.Styles.Attachments.Remove,
	), attachments.Keymap{})
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

// TestCaptureDraftClonesTextAndAttachments pins captureDraft's two jobs:
// reading the logical compose-box text, and cloning the attachment list
// rather than aliasing it — m.attachments.List() returns the live slice,
// which keeps changing after the snapshot is taken.
func TestCaptureDraftClonesTextAndAttachments(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.textarea.SetValue("what does this do?")
	m.attachments.Update(message.Attachment{FileName: "notes.txt"})

	d := m.captureDraft()
	require.Equal(t, "what does this do?", d.text)
	require.Len(t, d.attachments, 1)
	require.Equal(t, "notes.txt", d.attachments[0].FileName)

	m.attachments.Update(message.Attachment{FileName: "extra.txt"})
	require.Len(t, d.attachments, 1, "captureDraft must clone, not alias, the attachment list")
}

// TestCaptureDraftReinstatesTheBangPrefix pins that a draft captured while
// in bang mode round-trips through the same "!" convention used
// everywhere else a draft is stored (prompt history, external editor).
func TestCaptureDraftReinstatesTheBangPrefix(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.textarea.SetValue("ls -la")
	m.bangMode = true

	d := m.captureDraft()
	require.Equal(t, "!ls -la", d.text)
}

// TestApplyDraftRestoresTextAttachmentsAndBangMode pins applyDraft as the
// inverse of captureDraft: a leading "!" re-derives bang mode the same way
// syncBangModeFromTextarea does for prompt-history navigation.
func TestApplyDraftRestoresTextAttachmentsAndBangMode(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.attachments.Update(message.Attachment{FileName: "stale.txt"})

	m.applyDraft(editorDraft{
		text:        "!git status",
		attachments: []message.Attachment{{FileName: "notes.txt"}},
	})

	require.Equal(t, "git status", m.textarea.Value())
	require.True(t, m.bangMode)
	require.Len(t, m.attachments.List(), 1)
	require.Equal(t, "notes.txt", m.attachments.List()[0].FileName)
}

// TestApplyDraftZeroValueClearsTheBox pins that the zero-value editorDraft
// — what a session with nothing saved to return to gets — empties the box
// rather than leaving stale text or attachments behind.
func TestApplyDraftZeroValueClearsTheBox(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.textarea.SetValue("leftover")
	m.bangMode = true
	m.attachments.Update(message.Attachment{FileName: "leftover.txt"})

	m.applyDraft(editorDraft{})

	require.Empty(t, m.textarea.Value())
	require.False(t, m.bangMode)
	require.Empty(t, m.attachments.List())
}

// TestEnterSubSessionCmdCarriesTheCapturedDraft is the regression for the
// missing push/pop: entering a sub-agent's transcript (or a branch) used
// to leave whatever the user had typed, and any attached files, sitting
// in the box for a transcript never composed for them. This runs the real
// command enterSubSession returns, rather than a hand-built stand-in, to
// prove the wiring — not just captureDraft/applyDraft in isolation —
// actually saves the parent's draft into the pushed frame and clears the
// child's box.
func TestEnterSubSessionCmdCarriesTheCapturedDraft(t *testing.T) {
	t.Parallel()
	m, ws := newMockBusyUI(t)
	m.textarea.SetValue("what does this do?")
	m.attachments.Update(message.Attachment{FileName: "notes.txt"})

	item := agentItem(m, "msg-1", "call-1")
	childID := "msg-1$$call-1"
	ws.EXPECT().CreateAgentToolSessionID("msg-1", "call-1").Return(childID)
	ws.EXPECT().GetSession(gomock.Any(), childID).
		Return(session.Session{ID: childID, Title: "explore", ParentSessionID: "s1"}, nil)
	ws.EXPECT().ListSessionHistory(gomock.Any(), childID).Return(nil, nil)
	ws.EXPECT().FileTrackerListReadFiles(gomock.Any(), childID).Return(nil, nil)
	ws.EXPECT().AgentIsSessionBranch(childID).Return(false)
	ws.EXPECT().SetCurrentSession(gomock.Any(), childID).Return(nil)

	msgs := runCmds(m, m.enterSubSession(item))

	var loaded loadSessionMsg
	found := false
	for _, msg := range msgs {
		if lm, ok := msg.(loadSessionMsg); ok {
			loaded, found = lm, true
		}
	}
	require.True(t, found, "enterSubSession's command must produce a loadSessionMsg")
	require.NotNil(t, loaded.enterFrame)
	require.Equal(t, "what does this do?", loaded.enterFrame.draftText)
	require.Len(t, loaded.enterFrame.draftAttachments, 1)
	require.Equal(t, "notes.txt", loaded.enterFrame.draftAttachments[0].FileName)
	require.NotNil(t, loaded.draftAfter, "the child must start from a cleared box")
	require.Equal(t, editorDraft{}, *loaded.draftAfter)
}

// TestSubSessionNavigationPushesAndPopsTheDraft drives the pushed frame
// through Update, exercising the loadSessionMsg handler's draftAfter
// branch: drilling down clears the box, and popping back out restores
// exactly what was typed and attached before drilling down.
func TestSubSessionNavigationPushesAndPopsTheDraft(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.textarea.SetValue("what does this do?")
	m.attachments.Update(message.Attachment{FileName: "notes.txt"})

	draft := m.captureDraft()
	m.Update(loadSessionMsg{
		session: &session.Session{ID: "msg-1$$call-1", Title: "explore", ParentSessionID: "root"},
		enterFrame: &sessionStackFrame{
			id: "root", title: "Root task",
			draftText: draft.text, draftAttachments: draft.attachments,
		},
		draftAfter: &editorDraft{},
	})
	m.session = &session.Session{ID: "msg-1$$call-1", Title: "explore", ParentSessionID: "root"}

	require.Empty(t, m.textarea.Value(), "the child sub-session must not inherit the parent's draft")
	require.Empty(t, m.attachments.List(), "the child sub-session must not inherit the parent's attachments")

	parent := m.sessionStack[len(m.sessionStack)-1]
	restore := editorDraft{text: parent.draftText, attachments: parent.draftAttachments}
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "root", Title: "Root task"},
		leaveLevel: true,
		draftAfter: &restore,
	})

	require.Equal(t, "what does this do?", m.textarea.Value(),
		"leaving must restore exactly what was typed before drilling down")
	require.Len(t, m.attachments.List(), 1)
	require.Equal(t, "notes.txt", m.attachments.List()[0].FileName)
}

// TestGoToBreadcrumbLevelRestoresThatLevelsDraft pins the multi-level
// jump: the draft restored is the one captured when the view first
// drilled down from that level, not the level immediately below it.
func TestGoToBreadcrumbLevelRestoresThatLevelsDraft(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)

	m.textarea.SetValue("root draft")
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "level-1", Title: "level 1", ParentSessionID: "root"},
		enterFrame: &sessionStackFrame{id: "root", title: "Root task", draftText: "root draft"},
		draftAfter: &editorDraft{},
	})
	m.session = &session.Session{ID: "level-1", Title: "level 1", ParentSessionID: "root"}

	m.textarea.SetValue("level 1 draft")
	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "level-2", Title: "level 2", ParentSessionID: "level-1"},
		enterFrame: &sessionStackFrame{id: "level-1", title: "level 1", draftText: "level 1 draft"},
		draftAfter: &editorDraft{},
	})
	m.session = &session.Session{ID: "level-2", Title: "level 2", ParentSessionID: "level-1"}
	require.Len(t, m.sessionStack, 2)

	wantFrame := m.sessionStack[0]
	require.NotNil(t, m.goToBreadcrumbLevel(0))
	m.Update(loadSessionMsg{
		session:         &session.Session{ID: "root", Title: "Root task"},
		truncateStackTo: &[]int{0}[0],
		draftAfter:      &editorDraft{text: wantFrame.draftText, attachments: wantFrame.draftAttachments},
	})

	require.Equal(t, "root draft", m.textarea.Value(),
		"jumping to the root must restore the draft captured there, not level 1's")
	require.Empty(t, m.sessionStack)
}

// TestSwitchingSessionsClearsTheDraft is the switcher counterpart to
// TestSwitchingSessionsClearsTheStack: a switcher pick has no saved level
// to restore, so whatever was typed for the old session must not leak
// into the one just switched to.
func TestSwitchingSessionsClearsTheDraft(t *testing.T) {
	t.Parallel()
	m := newSubSessionUI(t)
	m.dialog = dialog.NewOverlay(passThroughDialog{})
	m.textarea.SetValue("typed for the root session")
	m.attachments.Update(message.Attachment{FileName: "root-notes.txt"})

	m.handleDialogMsg(dialog.ActionSelectSession{Session: session.Session{ID: "other"}})
	require.Equal(t, "typed for the root session", m.textarea.Value(),
		"the box must not clear before the switch actually lands")

	m.Update(loadSessionMsg{
		session:    &session.Session{ID: "other", Title: "Other"},
		clearStack: true,
		draftAfter: &editorDraft{},
	})

	require.Empty(t, m.textarea.Value())
	require.Empty(t, m.attachments.List())
}
