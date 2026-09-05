package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
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
// cycle through the four permission options.
func TestPermissions_NavigationCyclesOptions(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	require.Equal(t, 0, p.selectedOption)

	// Tab cycles forward.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 1, p.selectedOption)

	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 2, p.selectedOption)

	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 3, p.selectedOption)

	// Wrap around.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 0, p.selectedOption)

	// Left cycles backward.
	p.HandleMsg(keyMsg('h'))
	require.Equal(t, 3, p.selectedOption)
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

// TestPermissions_DenyWithReasonEntersTextMode verifies that choosing
// the "Reason" option, whether by its key shortcut or by selecting it
// and pressing enter, opens the text box instead of resolving the
// request right away.
func TestPermissions_DenyWithReasonEntersTextMode(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	action := p.HandleMsg(keyMsg('r'))
	require.Nil(t, action, "the shortcut should switch modes, not resolve the request")
	require.True(t, p.denyReasonMode)
	require.True(t, p.denyReasonInput.Focused())

	p2 := newTestPermissions(t)
	p2.selectedOption = len(p2.options()) - 1 // "Reason" is always last.
	action = p2.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, action)
	require.True(t, p2.denyReasonMode)
}

// TestPermissions_DenyWithReasonSubmitsTypedText verifies that text
// typed into the reason box travels through to the response on
// PermissionRequest.DenyReason, which is how it eventually reaches the
// model's tool response.
func TestPermissions_DenyWithReasonSubmitsTypedText(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	p.enterDenyReasonMode()

	for _, r := range "not now" {
		p.HandleMsg(keyMsg(r))
	}

	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok, "enter should submit and resolve the request")
	require.Equal(t, PermissionDeny, resp.Action)
	require.Equal(t, "not now", resp.Permission.DenyReason)
}

// TestPermissions_DenyWithReasonEscapeCancels verifies that escape
// inside the reason box backs out to the button row instead of
// resolving or closing the dialog, so a user who opened it by mistake
// isn't forced to submit a denial.
func TestPermissions_DenyWithReasonEscapeCancels(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	p.enterDenyReasonMode()

	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, action)
	require.False(t, p.denyReasonMode)
}

// TestPermissions_DenyWithReasonEmptySubmit verifies that submitting
// with no typed text still denies, just without a reason attached.
func TestPermissions_DenyWithReasonEmptySubmit(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	p.enterDenyReasonMode()

	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok)
	require.Equal(t, PermissionDeny, resp.Action)
	require.Empty(t, resp.Permission.DenyReason)
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
	// Some render paths (renderDiff) cache until something marks the
	// viewport dirty; Draw does that on its first pass by sizing it.
	p.viewportDirty = true
	return p
}

func newWritePermissions(filePath, oldContent, newContent string) *Permissions {
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	p := NewPermissions(com, permission.PermissionRequest{
		ID:         "perm-test",
		ToolCallID: "tool-call-test",
		ToolName:   toolnames.Write,
		Params: tools.WritePermissionsParams{
			FilePath:   filePath,
			OldContent: oldContent,
			NewContent: newContent,
		},
	})
	p.viewportDirty = true
	return p
}

// A merge proposal always hands back a fresh document (mergeTool.plan
// hardcodes its OldContent to ""), so there is nothing to diff it
// against. It renders as plain syntax-highlighted content instead of a
// diff where every line would show as an addition.
func TestPermissions_MergeShowsTheProposalAsPlainContent(t *testing.T) {
	t.Parallel()

	p := newMergePermissions("# Plan\n\nRewrite the parser.")
	require.False(t, p.hasDiffView(),
		"a merge proposal has no old content, so there is nothing to diff against")

	content := ansi.Strip(p.renderContent(80))
	require.Contains(t, content, "Rewrite the parser.")

	header := ansi.Strip(p.renderHeader(80))
	require.Contains(t, header, "Document")
	require.Contains(t, header, tools.ProposalDocumentName)
	require.NotContains(t, header, "Path",
		"the proposal is held in memory and has no path to show")
}

// The fullscreen toggle and the split/unified switch are offered only
// where they do something. A merge proposal has no old content to
// diff against, so neither one applies.
func TestPermissions_MergeOffersNoDiffControls(t *testing.T) {
	t.Parallel()

	p := newMergePermissions("# Plan\n\nRewrite the parser.")

	var names []string
	for _, b := range p.ShortHelp() {
		names = append(names, b.Help().Key)
	}
	require.NotContains(t, names, p.keyMap.ToggleFullscreen.Help().Key)
	require.NotContains(t, names, p.keyMap.ToggleDiffMode.Help().Key)
}

// Creating a new file has nothing to diff against, so the dialog shows
// the new content as plain code rather than a diff where every line
// would show as an addition.
func TestPermissions_WriteNewFileShowsPlainContent(t *testing.T) {
	t.Parallel()

	p := newWritePermissions("main.go", "", "package main\n")
	require.False(t, p.hasDiffView(),
		"a new file has no old content, so there is nothing to diff against")

	content := ansi.Strip(p.renderContent(80))
	require.Contains(t, content, "package main")
}

// Overwriting an existing file does have old content to compare
// against, so it keeps the diff view.
func TestPermissions_WriteOverwriteShowsDiff(t *testing.T) {
	t.Parallel()

	p := newWritePermissions("main.go", "package old\n", "package main\n")
	require.True(t, p.hasDiffView(),
		"overwriting a file has old content to diff against")

	content := ansi.Strip(p.renderContent(80))
	require.Contains(t, content, "package main")
}

// A merge ends the branch the instant it's approved (mergeTool.apply sets
// StopTurn), so a session-wide grant for it could never be consulted
// again. Offering "Allow for Session" would mislead the user into
// thinking it does something distinct from Allow.
func TestPermissions_MergeOffersNoAllowForSession(t *testing.T) {
	t.Parallel()

	p := newMergePermissions("# Plan\n\nRewrite the parser.")

	var actions []PermissionAction
	for _, o := range p.options() {
		actions = append(actions, o.action)
	}
	require.Equal(t, []PermissionAction{PermissionAllow, PermissionDeny, PermissionDenyWithReason}, actions)

	rendered, _, _ := p.renderButtons(p.buttonOpts(), 80, false)
	require.NotContains(t, ansi.Strip(rendered), "Allow for Session")

	// Tab must cycle between exactly the three remaining options.
	require.Equal(t, 0, p.selectedOption)
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 1, p.selectedOption)
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 2, p.selectedOption)
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 0, p.selectedOption)

	// The direct "s" shortcut must also be a no-op.
	require.Nil(t, p.HandleMsg(keyMsg('s')))
}

// buttonScreenPos scans a drawn dialog for the screen cell whose hit
// compositor resolves to the given button index, mirroring how a real
// mouse click would land on it.
func buttonScreenPos(t *testing.T, p *Permissions, idx, maxW, maxH int) (x, y int) {
	t.Helper()
	for y := range maxH {
		for x := range maxW {
			if common.HitButtonIndex(p.buttonHit, x, y) == idx {
				return x, y
			}
		}
	}
	t.Fatalf("button %d not found on screen", idx)
	return 0, 0
}

// TestPermissions_MouseClickSelectsButton verifies that clicking a
// button with the mouse resolves it, the same as pressing its key
// shortcut would.
func TestPermissions_MouseClickSelectsButton(t *testing.T) {
	t.Parallel()

	const w, h = 100, 30
	p := newTestPermissions(t)
	scr := uv.NewScreenBuffer(w, h)
	p.Draw(scr, image.Rect(0, 0, w, h))
	require.NotNil(t, p.buttonHit, "a normal-width dialog should offer a single-row button layout")

	x, y := buttonScreenPos(t, p, 2, w, h) // Deny
	action := p.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok, "clicking the Deny button should produce a response")
	require.Equal(t, PermissionDeny, resp.Action)
	require.Equal(t, 2, p.selectedOption, "the click should also update the visible selection")
}

// TestPermissions_MouseHoverTracksButton verifies that mouse motion is
// recorded and resolves to the hovered button through the same
// compositor used for clicks.
func TestPermissions_MouseHoverTracksButton(t *testing.T) {
	t.Parallel()

	const w, h = 100, 30
	p := newTestPermissions(t)
	scr := uv.NewScreenBuffer(w, h)
	p.Draw(scr, image.Rect(0, 0, w, h))

	x, y := buttonScreenPos(t, p, 0, w, h) // Allow
	p.HandleMsg(tea.MouseMotionMsg{X: x, Y: y})
	require.Equal(t, 0, common.HitButtonIndex(p.buttonHit, p.hoverX, p.hoverY))
}
