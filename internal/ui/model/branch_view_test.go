package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

// TestLoadingADeadBranchIsReadOnly is the restart case. The rendezvous that
// suspends a parent on its branch lives in the agent process, so nothing
// survives a restart: config still describes a branch-mode agent, but no
// turn is held open for it any more. Loading such a session must land on
// the ordinary read-only sub-agent path rather than offering an editor,
// a merge and an abort that have nothing left to act on.
func TestLoadingADeadBranchIsReadOnly(t *testing.T) {
	t.Parallel()

	m := newSubSessionUI(t)
	require.Equal(t, uiFocusEditor, m.focus, "the fixture should start in the editor")

	m.Update(loadSessionMsg{
		session:  &session.Session{ID: "msg-1$$call-1", ParentSessionID: "root", Agent: "pairing"},
		isBranch: false,
	})

	require.False(t, m.viewingBranch())
	require.True(t, m.viewingSubAgent(),
		"a branch nobody is suspended on is just a finished transcript")
	require.Equal(t, uiFocusMain, m.focus)
	require.False(t, m.textarea.Focused())
	require.False(t, m.escapeCancels(),
		"esc must go back a level, not abandon a branch that is already over")
	require.False(t, m.cancelLeavesBranch())
}

// TestLoadingALiveBranchKeepsTheEditor is the control for the case above:
// while the process still holds the suspended call, the branch is the
// user's to drive.
func TestLoadingALiveBranchKeepsTheEditor(t *testing.T) {
	t.Parallel()

	m := newSubSessionUI(t)

	m.Update(loadSessionMsg{
		session:  &session.Session{ID: "msg-1$$call-1", ParentSessionID: "root", Agent: "pairing"},
		isBranch: true,
	})

	require.True(t, m.viewingBranch())
	require.False(t, m.viewingSubAgent(), "a branch must keep its editor")
	require.Equal(t, uiFocusEditor, m.focus)
	require.True(t, m.textarea.Focused())
	require.True(t, m.escapeCancels(),
		"esc on an idle branch abandons it, which is the only way to release the parent")
}

// TestViewingSubAgentExcludesBranches is the guard that makes a branch
// usable. All five read-only gates hang off viewingSubAgent, so a branch
// answering true there would be locked out of its own editor.
func TestViewingSubAgentExcludesBranches(t *testing.T) {
	t.Parallel()

	t.Run("a branch is not treated as a sub-agent transcript", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"}
		ui.sessionIsBranch = true

		require.True(t, ui.viewingBranch())
		require.False(t, ui.viewingSubAgent(),
			"a branch must keep its editor: the user is the one driving it")
	})

	t.Run("an ordinary sub-agent stays read only", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "task"}
		ui.sessionIsBranch = false

		require.False(t, ui.viewingBranch())
		require.True(t, ui.viewingSubAgent())
	})

	t.Run("a top-level session is neither", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)
		ui.session = &session.Session{ID: "root"}

		require.False(t, ui.viewingBranch())
		require.False(t, ui.viewingSubAgent())
	})

	t.Run("no session loaded is neither", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, nil)

		require.False(t, ui.viewingBranch())
		require.False(t, ui.viewingSubAgent())
	})
}

// TestViewingBranchReadsTheMemoizedFlag pins that the predicate answers from
// the flag settled at load time. The status line calls it every frame and
// resolving it reaches through the workspace.
func TestViewingBranchReadsTheMemoizedFlag(t *testing.T) {
	t.Parallel()

	ui := newTestUIWithConfig(t, nil)
	ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"}
	ui.sessionIsBranch = true

	require.True(t, ui.viewingBranch(),
		"the memoized answer must stand on its own: nothing is consulted per frame")
}

// TestSendMessageIsAllowedOnABranch pins the gate that would otherwise
// swallow the user's prompt. sendMessage refuses on a sub-agent transcript;
// a branch exists to be typed into.
func TestSendMessageIsAllowedOnABranch(t *testing.T) {
	t.Parallel()

	ui := newTestUIWithConfig(t, nil)
	ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"}
	ui.sessionIsBranch = true

	require.False(t, ui.viewingSubAgent(),
		"sendMessage rejects on viewingSubAgent, so a branch must not answer true")
}
