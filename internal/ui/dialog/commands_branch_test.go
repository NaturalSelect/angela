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
