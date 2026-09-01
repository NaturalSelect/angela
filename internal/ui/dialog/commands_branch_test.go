package dialog

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// commandIDs lists the ids the menu would show, so an assertion can name a
// command instead of indexing into the slice.
func commandIDs(items []*CommandItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID())
	}
	return ids
}

// newCommandsForBranch builds the menu the way the dialog does, with only
// the fields defaultCommands reads.
func newCommandsForBranch(t *testing.T, inBranch bool) *Commands {
	t.Helper()

	sty := styles.AngelaTeal()
	return &Commands{
		com: &common.Common{
			Styles:    &sty,
			Workspace: &configWorkspace{cfg: &config.Config{}},
		},
		sessionID:  "branch-1",
		hasSession: true,
		inBranch:   inBranch,
	}
}

// TestAbortCommandOnlyAppearsInABranch pins the gate on /abort. Abandoning
// a branch is meaningless anywhere else, and the item carries a session ID,
// so offering it outside would name a session the user is not looking at.
func TestAbortCommandOnlyAppearsInABranch(t *testing.T) {
	t.Parallel()

	t.Run("inside a branch", func(t *testing.T) {
		t.Parallel()

		ids := commandIDs(newCommandsForBranch(t, true).defaultCommands())
		require.Contains(t, ids, "abort_branch")
	})

	t.Run("outside a branch", func(t *testing.T) {
		t.Parallel()

		ids := commandIDs(newCommandsForBranch(t, false).defaultCommands())
		require.NotContains(t, ids, "abort_branch")
	})
}

// TestAbortCommandCarriesTheBranchSession pins that the item aborts the
// session the menu was opened on. The handler acts on the ID in the action
// rather than on whatever session is current when it fires.
func TestAbortCommandCarriesTheBranchSession(t *testing.T) {
	t.Parallel()

	var found *CommandItem
	for _, item := range newCommandsForBranch(t, true).defaultCommands() {
		if item.ID() == "abort_branch" {
			found = item
			break
		}
	}
	require.NotNil(t, found, "the branch menu must offer abort")

	action, ok := found.Action().(ActionAbortBranch)
	require.True(t, ok, "abort must dispatch ActionAbortBranch")
	require.Equal(t, "branch-1", action.SessionID)
}

// newCommandsWithParent builds the menu the way the dialog does, with only
// the fields defaultCommands's go-to-parent gate reads. hasParent is
// broader than inBranch — it also covers an ordinary sub-agent
// transcript — so it is a field of its own rather than reusing inBranch.
func newCommandsWithParent(t *testing.T, hasParent bool) *Commands {
	t.Helper()

	sty := styles.AngelaTeal()
	return &Commands{
		com: &common.Common{
			Styles:    &sty,
			Workspace: &configWorkspace{cfg: &config.Config{}},
		},
		sessionID:  "session-1",
		hasSession: true,
		hasParent:  hasParent,
	}
}

// TestGoToParentCommandGatedOnHasParent pins that /parent is only offered
// when the session in view actually has somewhere to go back to.
func TestGoToParentCommandGatedOnHasParent(t *testing.T) {
	t.Parallel()

	t.Run("with a parent", func(t *testing.T) {
		t.Parallel()

		ids := commandIDs(newCommandsWithParent(t, true).defaultCommands())
		require.Contains(t, ids, "go_to_parent")
	})

	t.Run("without a parent", func(t *testing.T) {
		t.Parallel()

		ids := commandIDs(newCommandsWithParent(t, false).defaultCommands())
		require.NotContains(t, ids, "go_to_parent")
	})
}

// TestGoToParentCommandDispatchesAction pins the action payload: none, since
// the handler navigates from whatever session is current rather than from a
// session ID captured at menu-build time — unlike abort_branch, /parent
// never mutates a session, so there is nothing a stale ID would protect.
func TestGoToParentCommandDispatchesAction(t *testing.T) {
	t.Parallel()

	var found *CommandItem
	for _, item := range newCommandsWithParent(t, true).defaultCommands() {
		if item.ID() == "go_to_parent" {
			found = item
			break
		}
	}
	require.NotNil(t, found, "the menu must offer go-to-parent when hasParent is set")

	_, ok := found.Action().(ActionGoToParent)
	require.True(t, ok, "go-to-parent must dispatch ActionGoToParent")
}
