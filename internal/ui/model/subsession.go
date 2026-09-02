package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/chat"
)

// sessionStackFrame remembers a level the view drilled down from. The title is
// captured on the way in because popping reloads the parent asynchronously, and
// the breadcrumb has to name the level before that load lands.
type sessionStackFrame struct {
	id    string
	title string
}

// loadSessionOpt carries deferred stack operations for sub-session
// navigation. The push/pop only takes effect inside the loadSessionMsg
// handler — never before the async load — so a failed load leaves
// the stack untouched.
type loadSessionOpt struct {
	enterFrame *sessionStackFrame
	leaveLevel bool
	// clearStack replaces the session stack with nil on success. Used
	// when switching to an unrelated session: the new session's
	// ancestors are not the old stack, so keeping it would let Escape
	// reload a stranger.
	clearStack bool
	// truncateStackTo trims sessionStack to this length on success. Used
	// when the user jumps straight to an ancestor via the breadcrumb
	// instead of leaving one level at a time.
	truncateStackTo *int
}

// inSubSession reports whether the view is below the session the user opened.
func (m *UI) inSubSession() bool {
	return len(m.sessionStack) > 0
}

// hasParentSession reports whether the session in view was drilled into or
// forked from another session, so there is a parent for /parent to reach.
// This is broader than viewingBranch: it also covers an ordinary
// read-only sub-agent transcript.
func (m *UI) hasParentSession() bool {
	return m.session != nil && m.session.ParentSessionID != ""
}

// viewingSubAgent reports that the transcript on screen belongs to a
// sub-agent rather than to the user.
//
// Nothing here accepts input: the run takes its instructions from the
// parent's model, so a prompt typed in would land in that agent's queue
// behind the parent's back, where the model that dispatched it never sees
// it. The editor is closed off rather than silently swallowing the text.
//
// A branch is the exception. It is also a child session, but it was forked
// precisely so the user could drive it, so it stays open for input.
func (m *UI) viewingSubAgent() bool {
	return m.session != nil && m.session.ParentSessionID != "" && !m.viewingBranch()
}

// viewingBranch reports that the transcript on screen is a branch: a child
// session the user talks to directly, and which suspends the conversation it
// was forked from until they resolve it.
//
// The answer is memoized at load time; the status line asks every frame and
// resolving it reaches through the workspace.
//
// Liveness comes from the agent process holding the suspended call, not from
// the agent's configured mode: a branch whose process is gone is finished,
// however the config still describes it.
func (m *UI) viewingBranch() bool {
	return m.session != nil && m.sessionIsBranch
}

// sessionTrail returns the titles from the root down to the level in view.
func (m *UI) sessionTrail() []string {
	if m.session == nil {
		return nil
	}
	trail := make([]string, 0, len(m.sessionStack)+1)
	for _, frame := range m.sessionStack {
		trail = append(trail, frame.title)
	}
	return append(trail, m.session.Title)
}

// selectedAgentTool returns the focused item when it is an agent tool call —
// the only kind of item with a session of its own behind it.
func (m *UI) selectedAgentTool() (*chat.AgentToolMessageItem, bool) {
	if m.chat == nil {
		return nil, false
	}
	item, ok := m.chat.SelectedItem().(*chat.AgentToolMessageItem)
	return item, ok
}

// enterSubSession drills into the session a sub-agent ran in. The parent
// frame is captured now but pushed onto the stack only when the child
// loads successfully — a failed load never leaves a phantom frame.
func (m *UI) enterSubSession(item *chat.AgentToolMessageItem) tea.Cmd {
	if m.session == nil {
		return nil
	}
	childID := m.com.Workspace.CreateAgentToolSessionID(item.MessageID(), item.ToolCall().ID)
	frame := sessionStackFrame{
		id:    m.session.ID,
		title: m.session.Title,
	}
	return m.loadSession(childID, loadSessionOpt{enterFrame: &frame})
}

// leaveSubSession pops back one level. The parent is reloaded from the
// database rather than restored from memory: the sub-agent kept writing to
// it while the view was elsewhere, so the copy we drilled down from is
// already stale. The stack frame is popped only after the reload succeeds
// so a transient error keeps the breadcrumb and lets the user retry.
//
// A branch opened without drilling down — picked straight from the session
// switcher, for instance — has no frame on the stack to pop even though it
// has a parent: that parent is data on the session itself, not a fact about
// how the view got here. Falling back to it is what lets such a branch
// still be left.
func (m *UI) leaveSubSession() tea.Cmd {
	if top := len(m.sessionStack) - 1; top >= 0 {
		parent := m.sessionStack[top]
		return m.loadSession(parent.id, loadSessionOpt{leaveLevel: true})
	}
	return m.loadSession(m.session.ParentSessionID)
}

// goToBreadcrumbLevel jumps directly to the ancestor session at the given
// sessionStack index, dropping every level below it — the multi-level
// counterpart to leaveSubSession, used when the user clicks a crumb in
// the header breadcrumb instead of leaving one level at a time.
func (m *UI) goToBreadcrumbLevel(index int) tea.Cmd {
	if index < 0 || index >= len(m.sessionStack) {
		return nil
	}
	frame := m.sessionStack[index]
	return m.loadSession(frame.id, loadSessionOpt{truncateStackTo: &index})
}

// escapeCancels reports whether the escape key means "stop what is running"
// rather than "go back a level".
//
// A sub-agent transcript never claims the key, busy or not: it is read
// only, so there is nothing on screen for the gesture to stop, and the run
// keeps going in the background no matter what is displayed. Skipping this
// check meant the key could never back out of a still-running sub-agent,
// since the overall turn stays busy for as long as its transcript exists.
//
// A branch is otherwise treated like any other session: escape interrupts
// its turn while busy, same as elsewhere, but never abandons it. /abort is
// the only way to give a branch up, so that one key keeps one job.
//
// An open completions popup keeps its own, more local meaning for escape
// (close the popup) regardless of busy state: typing "/" or "@" and backing
// out of it is routine and must not be read as a cancel press.
func (m *UI) escapeCancels() bool {
	if m.viewingSubAgent() || m.completionsOpen {
		return false
	}
	return m.isAgentBusy()
}

// abortBranch abandons a branch without merging and returns to the
// conversation it was forked from, releasing the turn that has been
// suspended there.
//
// Unlike the escape gesture this is unconditional: the user picked "abort"
// by name, so a turn still running is given up along with the branch rather
// than merely interrupted. That is why it goes through the dedicated
// abandon operation rather than the shared cancel, which spares a busy
// branch on purpose.
func (m *UI) abortBranch(sessionID string) tea.Cmd {
	m.com.Workspace.AgentAbandonBranch(sessionID)
	m.turnIsSpinning = false
	m.invalidateBusyCaches()

	cmds := []tea.Cmd{m.leaveSubSession()}
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}
