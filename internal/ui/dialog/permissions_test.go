package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestPermissions(t *testing.T) *Permissions {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	perm := permission.PermissionRequest{
		ID:         "perm-test",
		ToolCallID: "tool-call-test",
		ToolName:   "bash",
	}
	return NewPermissions(com, perm)
}

// TestPermissions_ActionKeysResolve verifies that action keys produce the
// correct permission response.
func TestPermissions_ActionKeysResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key    tea.KeyPressMsg
		action PermissionAction
	}{
		{keyMsg('a'), PermissionAllow},
		{keyMsg('A'), PermissionAllow},
		{keyMsg('d'), PermissionDeny},
		{keyMsg('D'), PermissionDeny},
		{keyMsg('s'), PermissionAllowForSession},
		{keyMsg('S'), PermissionAllowForSession},
	}

	for _, tc := range tests {
		p := newTestPermissions(t)
		action := p.HandleMsg(tc.key)
		resp, ok := action.(ActionPermissionResponse)
		require.Truef(t, ok, "key %q should produce ActionPermissionResponse", tc.key.Text)
		require.Equal(t, tc.action, resp.Action)
	}
}

// TestPermissions_NavigationCyclesOptions verifies that tab and arrow keys
// cycle through the three permission options.
func TestPermissions_NavigationCyclesOptions(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	require.Equal(t, 0, p.selectedOption)

	// Tab cycles forward.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 1, p.selectedOption)

	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 2, p.selectedOption)

	// Wrap around.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 0, p.selectedOption)

	// Left cycles backward.
	p.HandleMsg(keyMsg('h'))
	require.Equal(t, 2, p.selectedOption)
}

// TestPermissions_EnterConfirmsSelection verifies that enter confirms the
// currently selected option.
func TestPermissions_EnterConfirmsSelection(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	p.selectedOption = 1 // Allow for session.

	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok)
	require.Equal(t, PermissionAllowForSession, resp.Action)
}

// TestPermissions_EscapeDenies verifies that escape denies the request.
func TestPermissions_EscapeDenies(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok)
	require.Equal(t, PermissionDeny, resp.Action)
}

// TestPermissions_RenameShowsSymbolChange pins that approving a rename
// tells the user which symbol becomes which. A rename spans files the
// dialog never shows, so the two names are the whole basis for the
// decision.
func TestPermissions_RenameShowsSymbolChange(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	p := NewPermissions(com, permission.PermissionRequest{
		ID:         "perm-test",
		ToolCallID: "tool-call-test",
		ToolName:   toolnames.LSPRename,
		Params: tools.RenamePermissionsParams{
			Symbol:  "OldName",
			NewName: "NewName",
		},
	})

	content := ansi.Strip(p.renderContent(80))
	require.Contains(t, content,
		"Symbol: OldName "+styles.ArrowRightIcon+" NewName",
		"the rename must read as a symbol change, not a dump of the raw params")
}

func newMergePermissions(doc string) *Permissions {
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	p := NewPermissions(com, permission.PermissionRequest{
		ID:         "perm-test",
		ToolCallID: "tool-call-test",
		ToolName:   toolnames.Merge,
		Params: tools.MergePermissionsParams{
			Name:       tools.ProposalDocumentName,
			NewContent: doc,
		},
	})
	// renderDiff serves a cache until something marks it stale; Draw does
	// that on its first pass by sizing the viewport.
	p.viewportDirty = true
	return p
}

// A merge hands a whole proposal back and ends the branch, so the user
// has to be able to read the document before approving it. The diff view
// is what gives the prompt the room, the scrollbar and the fullscreen
// key; the plain text panel caps out at half the terminal with no way to
// expand.
func TestPermissions_MergeShowsTheProposalAsADiff(t *testing.T) {
	t.Parallel()

	p := newMergePermissions("# Plan\n\nRewrite the parser.")
	require.True(t, p.hasDiffView(),
		"a proposal is too long for the simple prompt, which cannot go fullscreen")

	content := ansi.Strip(p.renderContent(80))
	require.Contains(t, content, "Rewrite the parser.")

	header := ansi.Strip(p.renderHeader(80))
	require.Contains(t, header, "Document")
	require.Contains(t, header, tools.ProposalDocumentName)
	require.NotContains(t, header, "Path",
		"the proposal is held in memory and has no path to show")
}

// The fullscreen toggle and the split/unified switch are offered only
// where they do something. Merge earns them by rendering a diff.
func TestPermissions_MergeOffersDiffControls(t *testing.T) {
	t.Parallel()

	p := newMergePermissions("# Plan\n\nRewrite the parser.")

	var names []string
	for _, b := range p.ShortHelp() {
		names = append(names, b.Help().Key)
	}
	require.Contains(t, names, p.keyMap.ToggleFullscreen.Help().Key)
	require.Contains(t, names, p.keyMap.ToggleDiffMode.Help().Key)
}
