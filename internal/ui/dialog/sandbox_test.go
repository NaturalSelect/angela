package dialog

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const (
	sandboxTestW = 100
	sandboxTestH = 30
)

// sandboxTestWorkspace is the least workspace NewSandbox needs: a
// working directory to seed the default read-write row, and a (nil)
// config so defaultSandboxReadWrite skips the data-directory entry.
type sandboxTestWorkspace struct {
	workspace.Workspace

	dir    string
	grants sandbox.ContainerGrants
}

func (w *sandboxTestWorkspace) WorkingDir() string     { return w.dir }
func (w *sandboxTestWorkspace) Config() *config.Config { return nil }

func (w *sandboxTestWorkspace) SandboxContainerGrants() sandbox.ContainerGrants { return w.grants }

func newTestSandbox(t *testing.T) *Sandbox {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &sandboxTestWorkspace{dir: t.TempDir()},
	}
	return NewSandbox(com)
}

// drawSandbox draws m at a fixed size, populating m.hitTargets and
// m.buttonHit the same way a real frame would before a mouse event.
func drawSandbox(m *Sandbox) {
	scr := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxTestH))
}

// targetPoint returns a point inside the first hit target matching
// want, mirroring where a real mouse click would land on it.
func targetPoint(t *testing.T, m *Sandbox, want func(sandboxHitTarget) bool) (x, y int) {
	t.Helper()
	for _, tgt := range m.hitTargets {
		if want(tgt) {
			return tgt.rect.Min.X, tgt.rect.Min.Y
		}
	}
	t.Fatalf("no matching hit target found among %d targets", len(m.hitTargets))
	return 0, 0
}

func rowTarget(row int, col sandboxCol) func(sandboxHitTarget) bool {
	return func(tgt sandboxHitTarget) bool {
		return tgt.area == sandboxFocusRow && tgt.row == row && tgt.col == col
	}
}

func addTarget() func(sandboxHitTarget) bool {
	return func(tgt sandboxHitTarget) bool { return tgt.area == sandboxFocusAdd }
}

func networkTarget() func(sandboxHitTarget) bool {
	return func(tgt sandboxHitTarget) bool { return tgt.area == sandboxFocusNetwork }
}

func continueTarget() func(sandboxHitTarget) bool {
	return func(tgt sandboxHitTarget) bool { return tgt.area == sandboxFocusContinue }
}

// compositorButtonPos scans the screen for the cell whose hit
// compositor resolves to the given button index, mirroring how a real
// mouse click would land on it. Unlike permissions_test.go's
// buttonScreenPos, this takes the compositor directly since the
// confirm-stage buttons aren't behind a dedicated accessor.
func compositorButtonPos(t *testing.T, hit *lipgloss.Compositor, idx, maxW, maxH int) (x, y int) {
	t.Helper()
	for y := range maxH {
		for x := range maxW {
			if common.HitButtonIndex(hit, x, y) == idx {
				return x, y
			}
		}
	}
	t.Fatalf("button %d not found on screen", idx)
	return 0, 0
}

func TestSandbox_DefaultRows(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	require.NotEmpty(t, m.rows)
	last := m.rows[len(m.rows)-1]
	require.Equal(t, "/", last.input.Value())
	require.True(t, last.readOnly)

	cfg := m.config()
	require.Contains(t, cfg.ReadWrite, m.com.Workspace.WorkingDir())
	require.Contains(t, cfg.ReadOnly, "/")
	require.True(t, cfg.AllowNetwork)
}

// TestSandbox_MouseClickTogglesReadOnly verifies clicking a row's
// RO/RW button flips that row's mode, without affecting other rows.
func TestSandbox_MouseClickTogglesReadOnly(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	before := m.rows[0].readOnly

	x, y := targetPoint(t, m, rowTarget(0, sandboxColToggle))
	action := m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})

	require.Nil(t, action)
	require.Equal(t, !before, m.rows[0].readOnly)
}

// TestSandbox_MouseClickRemovesRow verifies clicking a row's "-"
// button removes exactly that row.
func TestSandbox_MouseClickRemovesRow(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	before := len(m.rows)
	removedPath := m.rows[0].input.Value()

	x, y := targetPoint(t, m, rowTarget(0, sandboxColRemove))
	m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})

	require.Len(t, m.rows, before-1)
	for _, row := range m.rows {
		require.NotEqual(t, removedPath, row.input.Value())
	}
}

// TestSandbox_MouseClickAddsRow verifies clicking "+ Add Path" appends
// a blank, read-write row and focuses it.
func TestSandbox_MouseClickAddsRow(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	before := len(m.rows)

	x, y := targetPoint(t, m, addTarget())
	m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})

	require.Len(t, m.rows, before+1)
	last := m.rows[len(m.rows)-1]
	require.Empty(t, last.input.Value())
	require.False(t, last.readOnly)
	require.True(t, last.input.Focused(), "clicking + should focus the new row's input")

	area, row, col := m.focusTarget(m.focused)
	require.Equal(t, sandboxFocusRow, area)
	require.Equal(t, before, row)
	require.Equal(t, sandboxColInput, col)
}

// TestSandbox_MouseClickContinueSubmitsForm verifies that clicking the
// Continue button transitions to the confirmation stage, mirroring
// the keyboard path covered by TestSandbox_FormSubmitEntersConfirmStage.
func TestSandbox_MouseClickContinueSubmitsForm(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	m.selectedNo = true

	x, y := targetPoint(t, m, continueTarget())
	action := m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})

	require.Nil(t, action)
	require.Equal(t, sandboxStageConfirm, m.stage)
	require.False(t, m.selectedNo)
}

// TestSandbox_MouseHoverTracksTarget verifies mouse motion is recorded
// and resolves to the hovered target through the same hit-testing used
// for clicks.
func TestSandbox_MouseHoverTracksTarget(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)

	x, y := targetPoint(t, m, rowTarget(0, sandboxColToggle))
	m.HandleMsg(tea.MouseMotionMsg{X: x, Y: y})

	idx := m.hitTest(m.hoverX, m.hoverY)
	require.GreaterOrEqual(t, idx, 0)
	require.Equal(t, sandboxFocusRow, m.hitTargets[idx].area)
	require.Equal(t, 0, m.hitTargets[idx].row)
	require.Equal(t, sandboxColToggle, m.hitTargets[idx].col)
}

// TestSandbox_ConfirmMouseClickSelectsButton verifies clicking Cancel
// in the confirmation stage closes the dialog, and clicking Enter
// Sandbox produces ActionEnterSandbox.
func TestSandbox_ConfirmMouseClickSelectsButton(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm
	drawSandbox(m)
	require.NotNil(t, m.buttonHit)

	x, y := compositorButtonPos(t, m.buttonHit, 1, sandboxTestW, sandboxTestH) // Cancel
	action := m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	require.IsType(t, ActionClose{}, action)

	m2 := newTestSandbox(t)
	m2.stage = sandboxStageConfirm
	drawSandbox(m2)

	x, y = compositorButtonPos(t, m2.buttonHit, 0, sandboxTestW, sandboxTestH) // Enter Sandbox
	action = m2.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	resp, ok := action.(ActionEnterSandbox)
	require.True(t, ok)
	require.True(t, resp.Config.AllowNetwork)
}

// TestSandbox_KeyboardFocusCyclesAndActivates verifies Tab/Shift+Tab
// still cycle through every row column plus the add and network
// stops, and that space activates whatever non-input control is
// focused (matching how the network toggle already behaved).
func TestSandbox_KeyboardFocusCyclesAndActivates(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	rowCount := len(m.rows)
	require.Equal(t, rowCount*sandboxColCount+3, m.stopCount())

	// Walking Next stopCount times must land back on the first row's
	// input.
	for range m.stopCount() {
		m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	area, row, col := m.focusTarget(m.focused)
	require.Equal(t, sandboxFocusRow, area)
	require.Equal(t, 0, row)
	require.Equal(t, sandboxColInput, col)

	// Move focus to the network toggle (second-to-last stop, since the
	// continue button now follows it) and activate it with enter.
	m.setFocus(m.stopCount() - 2)
	before := m.allowNetwork
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, !before, m.allowNetwork)
}

// TestSandbox_ID verifies the dialog reports its identifier.
func TestSandbox_ID(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	require.Equal(t, SandboxID, m.ID())
}

// sandboxDataDirWorkspace additionally reports a non-nil config with
// a data directory set, exercising the branch sandboxTestWorkspace's
// nil Config() always skips.
type sandboxDataDirWorkspace struct {
	workspace.Workspace

	dir     string
	dataDir string
}

func (w *sandboxDataDirWorkspace) WorkingDir() string { return w.dir }
func (w *sandboxDataDirWorkspace) Config() *config.Config {
	return &config.Config{Options: &config.Options{DataDirectory: w.dataDir}}
}

func (w *sandboxDataDirWorkspace) SandboxContainerGrants() sandbox.ContainerGrants {
	return sandbox.ContainerGrants{}
}

// TestSandbox_DefaultConfigIncludesConfiguredDataDirectory verifies
// that sandboxDefaultConfig adds the workspace's configured data
// directory as a read-write path when one is set.
func TestSandbox_DefaultConfigIncludesConfiguredDataDirectory(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &sandboxDataDirWorkspace{dir: t.TempDir(), dataDir: dataDir},
	}
	m := NewSandbox(com)

	require.Contains(t, m.config().ReadWrite, dataDir)
}

// sandboxPermissionsWorkspace additionally reports a config with
// permission rules set, exercising the branch that folds
// permission.FilesystemAllowPaths into the pre-filled form.
type sandboxPermissionsWorkspace struct {
	workspace.Workspace

	dir   string
	rules []permission.Rule
}

func (w *sandboxPermissionsWorkspace) WorkingDir() string { return w.dir }
func (w *sandboxPermissionsWorkspace) Config() *config.Config {
	return &config.Config{Permissions: &config.Permissions{Rules: w.rules}}
}

func (w *sandboxPermissionsWorkspace) SandboxContainerGrants() sandbox.ContainerGrants {
	return sandbox.ContainerGrants{}
}

// TestSandbox_DefaultConfigIncludesPermissionRulePaths verifies that
// sandboxDefaultConfig widens the pre-filled form with directories a
// filesystem allow rule in the config already covers, so the dialog
// starts pre-populated with what the user has already approved
// without a prompt.
func TestSandbox_DefaultConfigIncludesPermissionRulePaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles: &s,
		Workspace: &sandboxPermissionsWorkspace{
			dir: dir,
			rules: []permission.Rule{
				{Action: permission.RuleAllow, Tool: "edit", Pattern: "external/**"},
			},
		},
	}
	m := NewSandbox(com)

	require.Contains(t, m.config().ReadWrite, filepath.Join(dir, "external"))
}

// TestSandbox_RemoveRowOutOfBounds verifies that removeRow silently
// ignores an out-of-range index instead of panicking or mutating rows.
func TestSandbox_RemoveRowOutOfBounds(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	before := len(m.rows)

	m.removeRow(-1)
	require.Len(t, m.rows, before)

	m.removeRow(before)
	require.Len(t, m.rows, before)
}

// TestSandbox_ActivateFocusedRowToggleAndRemoveViaKeyboard verifies
// that pressing enter while focused on a row's toggle or remove stop
// performs that action, mirroring the mouse-click behavior already
// covered by TestSandbox_MouseClickTogglesReadOnly and
// TestSandbox_MouseClickRemovesRow.
func TestSandbox_ActivateFocusedRowToggleAndRemoveViaKeyboard(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	before := m.rows[0].readOnly

	m.setFocus(0*sandboxColCount + int(sandboxColToggle))
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, !before, m.rows[0].readOnly)

	rowCount := len(m.rows)
	removedPath := m.rows[0].input.Value()
	m.setFocus(0*sandboxColCount + int(sandboxColRemove))
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Len(t, m.rows, rowCount-1)
	for _, row := range m.rows {
		require.NotEqual(t, removedPath, row.input.Value())
	}
}

// TestSandbox_ActivateFocusedAddViaKeyboard verifies that pressing
// enter while focused on the "+ Add Path" stop adds a row, mirroring
// the mouse-click behavior already covered by
// TestSandbox_MouseClickAddsRow.
func TestSandbox_ActivateFocusedAddViaKeyboard(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	before := len(m.rows)

	m.setFocus(before * sandboxColCount)
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Len(t, m.rows, before+1)
	last := m.rows[len(m.rows)-1]
	require.Empty(t, last.input.Value())
	require.False(t, last.readOnly)
}

// TestSandbox_HandleFormClickMiss verifies that clicking outside every
// hit target leaves the dialog's state untouched.
func TestSandbox_HandleFormClickMiss(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	require.Equal(t, -1, m.hitTest(0, 0), "top-left corner should be outside any hit target")

	beforeFocus := m.focused
	beforeRows := len(m.rows)

	action := m.HandleMsg(tea.MouseClickMsg{X: 0, Y: 0, Button: uv.MouseLeft})

	require.Nil(t, action)
	require.Equal(t, beforeFocus, m.focused)
	require.Equal(t, beforeRows, len(m.rows))
}

// TestSandbox_HandleFormClickInputFocusesRow verifies that clicking a
// row's path input moves focus there.
func TestSandbox_HandleFormClickInputFocusesRow(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	m.setFocus(m.stopCount() - 1) // start elsewhere so the change is observable

	x, y := targetPoint(t, m, rowTarget(0, sandboxColInput))
	action := m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})

	require.Nil(t, action)
	area, row, col := m.focusTarget(m.focused)
	require.Equal(t, sandboxFocusRow, area)
	require.Equal(t, 0, row)
	require.Equal(t, sandboxColInput, col)
	require.True(t, m.rows[0].input.Focused())
}

// TestSandbox_HandleFormClickNetworkToggle verifies that clicking the
// network line flips allowNetwork and focuses that control, mirroring
// the row-button click behavior already covered elsewhere.
func TestSandbox_HandleFormClickNetworkToggle(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)
	before := m.allowNetwork

	x, y := targetPoint(t, m, networkTarget())
	action := m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})

	require.Nil(t, action)
	require.Equal(t, !before, m.allowNetwork)
	area, _, _ := m.focusTarget(m.focused)
	require.Equal(t, sandboxFocusNetwork, area)
}

// TestSandbox_ConfigSkipsEmptyPaths verifies that a row whose path is
// blank or whitespace-only contributes nothing to the parsed config.
func TestSandbox_ConfigSkipsEmptyPaths(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	before := m.config()

	m.rows = append(m.rows, newSandboxRow(m.com.Styles, "   ", false))
	after := m.config()

	require.Equal(t, before.ReadWrite, after.ReadWrite)
	require.Equal(t, before.ReadOnly, after.ReadOnly)
}

// TestSandbox_FormCloseReturnsActionClose verifies that the close key
// closes the dialog while still on the form stage.
func TestSandbox_FormCloseReturnsActionClose(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.IsType(t, ActionClose{}, action)
}

// TestSandbox_FormSubmitEntersConfirmStage verifies that activating
// the continue button transitions to the confirmation stage and
// resets a stale "Cancel" selection back to "Enter Sandbox".
func TestSandbox_FormSubmitEntersConfirmStage(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.selectedNo = true
	m.setFocus(m.stopCount() - 1) // the continue button is the last stop

	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Nil(t, action)
	require.Equal(t, sandboxStageConfirm, m.stage)
	require.False(t, m.selectedNo)
}

// TestSandbox_FormEnterOnInputDoesNotSubmit verifies that pressing
// Enter while a path input is focused no longer jumps straight to the
// confirmation stage: only activating the continue button does that.
func TestSandbox_FormEnterOnInputDoesNotSubmit(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.setFocus(0) // row 0's input

	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Nil(t, action)
	require.Equal(t, sandboxStageForm, m.stage)
}

// TestSandbox_FormPreviousKeyMovesFocusBackAndWraps verifies that the
// Previous binding steps focus back one stop, wrapping to the last
// stop from the first.
func TestSandbox_FormPreviousKeyMovesFocusBackAndWraps(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.setFocus(2)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, 1, m.focused)

	m.setFocus(0)
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, m.stopCount()-1, m.focused)
}

// TestSandbox_FormDefaultKeyFeedsInput verifies that a plain character
// key, matching none of the form's bindings, is fed into the focused
// row's path input.
func TestSandbox_FormDefaultKeyFeedsInput(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.setFocus(0) // row 0's input
	before := m.rows[0].input.Value()

	m.HandleMsg(keyMsg('x'))

	require.Equal(t, before+"x", m.rows[0].input.Value())
}

// TestSandbox_FormPasteInsertsTextIntoFocusedInput verifies that a
// paste event is fed into the focused row's path input.
func TestSandbox_FormPasteInsertsTextIntoFocusedInput(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.setFocus(0)
	before := m.rows[0].input.Value()

	action := m.HandleMsg(tea.PasteMsg{Content: "/pasted/path"})

	require.Nil(t, action)
	require.Equal(t, before+"/pasted/path", m.rows[0].input.Value())
}

// TestSandbox_ConfirmCloseReturnsActionClose verifies that the close
// key closes the dialog from the confirmation stage.
func TestSandbox_ConfirmCloseReturnsActionClose(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm

	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.IsType(t, ActionClose{}, action)
}

// TestSandbox_ConfirmLeftRightTogglesSelection verifies that Left and
// Right toggle which confirmation button is selected.
func TestSandbox_ConfirmLeftRightTogglesSelection(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm
	require.False(t, m.selectedNo)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.True(t, m.selectedNo)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	require.False(t, m.selectedNo)
}

// TestSandbox_ConfirmEnterSpaceEntersSandboxOrCancels verifies that
// confirming with "Enter Sandbox" selected returns ActionEnterSandbox,
// and confirming with "Cancel" selected closes the dialog instead.
func TestSandbox_ConfirmEnterSpaceEntersSandboxOrCancels(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm
	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionEnterSandbox)
	require.True(t, ok)
	require.Equal(t, m.config(), resp.Config)

	m2 := newTestSandbox(t)
	m2.stage = sandboxStageConfirm
	m2.selectedNo = true
	action2 := m2.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.IsType(t, ActionClose{}, action2)
}

// TestSandbox_ConfirmMouseMotionTracksHover verifies that mouse motion
// on the confirmation stage records the hover position without
// producing an action.
func TestSandbox_ConfirmMouseMotionTracksHover(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm
	drawSandbox(m)

	action := m.HandleMsg(tea.MouseMotionMsg{X: 5, Y: 7})

	require.Nil(t, action)
	require.Equal(t, 5, m.hoverX)
	require.Equal(t, 7, m.hoverY)
}

// TestSandbox_DrawFormHoverRerendersRowButtonStyle verifies that
// hovering a row's toggle button changes its rendered style on the
// next draw.
// TestSandbox_DrawFormHoverRerendersRowButtonStyle verifies that
// hovering a row's toggle button changes its rendered style on the
// next draw. It uses a short, fixed working directory rather than
// newTestSandbox's t.TempDir() so row 0's input stays short enough
// that the toggle button's hit target lands inside the dialog's
// fixed 64-column width regardless of the running test's name.
func TestSandbox_DrawFormHoverRerendersRowButtonStyle(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &sandboxTestWorkspace{dir: "/tmp/short"},
	}
	m := NewSandbox(com)

	scr1 := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr1, image.Rect(0, 0, sandboxTestW, sandboxTestH))
	var rect image.Rectangle
	for _, tgt := range m.hitTargets {
		if tgt.area == sandboxFocusRow && tgt.row == 0 && tgt.col == sandboxColToggle {
			rect = tgt.rect
		}
	}
	require.NotZero(t, rect, "row 0's toggle target should exist")

	m.HandleMsg(tea.MouseMotionMsg{X: rect.Min.X, Y: rect.Min.Y})
	scr2 := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr2, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	changed := false
	for x := rect.Min.X; x < rect.Max.X; x++ {
		before, after := scr1.CellAt(x, rect.Min.Y), scr2.CellAt(x, rect.Min.Y)
		if before != nil && after != nil && before.Style != after.Style {
			changed = true
			break
		}
	}
	require.True(t, changed, "hovering the toggle button should change some cell's rendered style")
}

// TestSandbox_DrawFormHoverRerendersContinueButtonStyle verifies that
// hovering the continue button changes its rendered style on the next
// draw, mirroring TestSandbox_DrawFormHoverRerendersRowButtonStyle. It
// uses the same short, fixed working directory as that test rather
// than newTestSandbox's t.TempDir(), so row 0 stays short enough that
// the dialog's fixed 64-column width never wraps it and shifts the
// continue button's hit target, regardless of the running test's name.
func TestSandbox_DrawFormHoverRerendersContinueButtonStyle(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &sandboxTestWorkspace{dir: "/tmp/short"},
	}
	m := NewSandbox(com)
	scr1 := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr1, image.Rect(0, 0, sandboxTestW, sandboxTestH))
	var rect image.Rectangle
	for _, tgt := range m.hitTargets {
		if tgt.area == sandboxFocusContinue {
			rect = tgt.rect
		}
	}
	require.NotZero(t, rect, "continue button target should exist")

	m.HandleMsg(tea.MouseMotionMsg{X: rect.Min.X, Y: rect.Min.Y})
	scr2 := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr2, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	changed := false
	for x := rect.Min.X; x < rect.Max.X; x++ {
		before, after := scr1.CellAt(x, rect.Min.Y), scr2.CellAt(x, rect.Min.Y)
		if before != nil && after != nil && before.Style != after.Style {
			changed = true
			break
		}
	}
	require.True(t, changed, "hovering the continue button should change some cell's rendered style")
}

// TestSandbox_NetworkViewUsesFocusedButtonStyle verifies that the
// network toggle's button switches to the focused style once the
// toggle has focus.
func TestSandbox_NetworkViewUsesFocusedButtonStyle(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	blurred, _ := m.networkView(2, 40, false)

	m.setFocus(m.stopCount() - 2) // the network toggle is second-to-last
	focused, _ := m.networkView(2, 40, false)

	require.NotEqual(t, blurred, focused)
}

// TestSandbox_DrawConfirmHoverChangesButtonStyle verifies that
// hovering a confirmation button changes its rendered style on the
// next draw.
func TestSandbox_DrawConfirmHoverChangesButtonStyle(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm
	scr1 := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr1, image.Rect(0, 0, sandboxTestW, sandboxTestH))
	x, y := compositorButtonPos(t, m.buttonHit, 1, sandboxTestW, sandboxTestH) // Cancel

	m.HandleMsg(tea.MouseMotionMsg{X: x, Y: y})
	scr2 := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr2, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	changed := false
	for cx := range sandboxTestW {
		if common.HitButtonIndex(m.buttonHit, cx, y) != 1 {
			continue
		}
		before, after := scr1.CellAt(cx, y), scr2.CellAt(cx, y)
		if before != nil && after != nil && before.Style != after.Style {
			changed = true
			break
		}
	}
	require.True(t, changed, "hovering the Cancel button should change some cell's rendered style")
}

// TestSandbox_DrawConfirmNarrowWidthNeverPanics verifies that a
// terminal too narrow for the confirmation dialog's usual padding
// never panics, mirroring quit.go's identical narrow-width handling
// (see TestQuit_Draw).
func TestSandbox_DrawConfirmNarrowWidthNeverPanics(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm

	scr := uv.NewScreenBuffer(20, 10)
	require.NotPanics(t, func() {
		m.Draw(scr, image.Rect(0, 0, 20, 10))
	})
}

// TestSandbox_ShortHelpConfirmStage verifies the confirmation stage's
// distinct short-help bindings.
func TestSandbox_ShortHelpConfirmStage(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	m.stage = sandboxStageConfirm

	require.Equal(t, []key.Binding{m.keyMap.LeftRight, m.keyMap.EnterSpace, m.keyMap.Close}, m.ShortHelp())
}

// TestSandbox_FullHelp verifies that FullHelp wraps ShortHelp in a
// single group.
func TestSandbox_FullHelp(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	require.Equal(t, [][]key.Binding{m.ShortHelp()}, m.FullHelp())
}

// TestSandbox_CursorAccountsForWideRunes verifies that the focused
// row's screen cursor lands past double-width runes (CJK text) by
// their real display width, not by rune count.
func TestSandbox_CursorAccountsForWideRunes(t *testing.T) {
	t.Parallel()

	mASCII := newTestSandbox(t)
	mASCII.rows[0].input.SetValue("")
	for _, r := range "abc" {
		mASCII.HandleMsg(keyMsg(r))
	}
	scrASCII := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	curASCII := mASCII.Draw(scrASCII, image.Rect(0, 0, sandboxTestW, sandboxTestH))
	require.NotNil(t, curASCII)

	mCJK := newTestSandbox(t)
	mCJK.rows[0].input.SetValue("")
	mCJK.HandleMsg(tea.PasteMsg{Content: "不好呀"})
	scrCJK := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	curCJK := mCJK.Draw(scrCJK, image.Rect(0, 0, sandboxTestW, sandboxTestH))
	require.NotNil(t, curCJK)

	require.Equal(t, curASCII.X+3, curCJK.X,
		"three double-width runes should land the cursor 3 columns further right than three single-width runes")
}

const sandboxShortH = 20

// newOverflowingSandbox returns a sandbox with far more rows than fit
// in sandboxShortH lines, drawn once so the viewport is laid out.
func newOverflowingSandbox(t *testing.T) *Sandbox {
	t.Helper()

	m := newTestSandbox(t)
	for range 25 {
		m.addRow()
	}
	m.setFocus(0)
	drawSandboxShort(m)
	return m
}

func drawSandboxShort(m *Sandbox) *tea.Cursor {
	scr := uv.NewScreenBuffer(sandboxTestW, sandboxShortH)
	return m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxShortH))
}

// TestSandbox_ManyRowsStayWithinScreen verifies that a form with more
// rows than the screen can hold scrolls instead of growing past the
// terminal: every control the form still shows must have a hit target
// on screen, and the rows block must be shorter than its content.
func TestSandbox_ManyRowsStayWithinScreen(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)

	require.Less(t, m.rowsHeight, m.rowsLineCount(), "rows should scroll")
	for _, tgt := range m.hitTargets {
		require.GreaterOrEqual(t, tgt.rect.Min.Y, 0)
		require.LessOrEqual(t, tgt.rect.Max.Y, sandboxShortH, "target %+v is below the screen", tgt)
	}

	var sawNetwork, sawContinue bool
	for _, tgt := range m.hitTargets {
		sawNetwork = sawNetwork || tgt.area == sandboxFocusNetwork
		sawContinue = sawContinue || tgt.area == sandboxFocusContinue
	}
	require.True(t, sawNetwork, "network toggle must stay reachable")
	require.True(t, sawContinue, "continue button must stay reachable")
}

// TestSandbox_FewRowsDoNotScroll verifies the default form fits and
// shows no scrollbar-induced narrowing.
func TestSandbox_FewRowsDoNotScroll(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	drawSandbox(m)

	require.Equal(t, m.rowsLineCount(), m.rowsHeight)
	require.Zero(t, m.rowsOffset)
}

// TestSandbox_FocusScrollsRowIntoView verifies that tabbing to a row
// below the viewport scrolls it into view, and that every visible row
// has a hit target while hidden rows have none.
func TestSandbox_FocusScrollsRowIntoView(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	last := len(m.rows) - 1

	m.setFocus(last * sandboxColCount)
	drawSandboxShort(m)

	require.Positive(t, m.rowsOffset)
	require.True(t, m.lineVisible(last))
	require.NotPanics(t, func() { targetPoint(t, m, rowTarget(last, sandboxColToggle)) })

	hidden := 0
	for _, tgt := range m.hitTargets {
		if tgt.area == sandboxFocusRow {
			require.True(t, m.lineVisible(tgt.row), "row %d has a target but is scrolled out", tgt.row)
		}
	}
	for i := range m.rows {
		if !m.lineVisible(i) {
			hidden++
		}
	}
	require.Positive(t, hidden)
}

// TestSandbox_FocusAddScrollsToBottom verifies the add button, the last
// line of the block, can be scrolled to and clicked.
func TestSandbox_FocusAddScrollsToBottom(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	before := len(m.rows)

	m.setFocus(len(m.rows) * sandboxColCount)
	drawSandboxShort(m)
	require.Equal(t, m.rowsLineCount()-m.rowsHeight, m.rowsOffset)

	x, y := targetPoint(t, m, addTarget())
	m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	require.Len(t, m.rows, before+1)
}

// TestSandbox_WheelScrollsAndClamps verifies the mouse wheel moves the
// viewport without being snapped back to the focused row, and never
// scrolls past either end.
func TestSandbox_WheelScrollsAndClamps(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	require.Zero(t, m.rowsOffset)

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 3})
	drawSandboxShort(m)
	require.Equal(t, 3, m.rowsOffset, "focus is on row 0, yet the wheel must be able to scroll it away")

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 1000})
	drawSandboxShort(m)
	require.Equal(t, m.rowsLineCount()-m.rowsHeight, m.rowsOffset)

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: -1000})
	drawSandboxShort(m)
	require.Zero(t, m.rowsOffset)
}

// TestSandbox_KeyPressScrollsFocusBackIntoView verifies typing in a
// row that the wheel scrolled out of view brings it back.
func TestSandbox_KeyPressScrollsFocusBackIntoView(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 1000})
	drawSandboxShort(m)
	require.False(t, m.lineVisible(0))

	m.HandleMsg(keyMsg('x'))
	drawSandboxShort(m)
	require.True(t, m.lineVisible(0))
}

// TestSandbox_CursorHiddenWhenFocusedRowScrolledOut verifies no cursor
// is drawn for an input the wheel scrolled out of the viewport, and
// that the cursor tracks the row's position when it is visible.
func TestSandbox_CursorHiddenWhenFocusedRowScrolledOut(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	visible := drawSandboxShort(m)
	require.NotNil(t, visible)

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 2})
	require.Nil(t, drawSandboxShort(m), "row 0 is scrolled out, so it can have no cursor")

	m.setFocus(2 * sandboxColCount)
	cur := drawSandboxShort(m)
	require.NotNil(t, cur)
	require.Less(t, cur.Y, sandboxShortH)
}

// TestSandbox_RemoveRowKeepsFocusVisible verifies removing a row while
// scrolled to the bottom leaves a valid, in-range viewport.
func TestSandbox_RemoveRowKeepsFocusVisible(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	m.setFocus(len(m.rows) * sandboxColCount)
	drawSandboxShort(m)

	for range 10 {
		m.removeRow(len(m.rows) - 1)
		drawSandboxShort(m)
		require.GreaterOrEqual(t, m.rowsOffset, 0)
		require.LessOrEqual(t, m.rowsOffset+m.rowsHeight, m.rowsLineCount())
	}
}

// TestSandbox_ClickHitsScrolledRow verifies hit targets follow the
// scroll offset, so a click lands on the row actually drawn there.
func TestSandbox_ClickHitsScrolledRow(t *testing.T) {
	t.Parallel()

	m := newOverflowingSandbox(t)
	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 5})
	drawSandboxShort(m)
	require.Equal(t, 5, m.rowsOffset)

	before := m.rows[5].readOnly
	x, y := targetPoint(t, m, rowTarget(5, sandboxColToggle))
	m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	require.Equal(t, !before, m.rows[5].readOnly)
}

// TestSandbox_LongPathStaysOnItsRow verifies a path wider than the row
// is clipped to its input instead of pushing the RW and remove buttons
// out of the dialog, whether or not the row is focused.
func TestSandbox_LongPathStaysOnItsRow(t *testing.T) {
	t.Parallel()

	const width = 60
	m := newTestSandbox(t)
	m.rows = []sandboxPathRow{
		newSandboxRow(m.com.Styles, "/short", false),
		newSandboxRow(m.com.Styles, "/home/nature/.local/share/containers/storage/overlay", false),
	}

	for _, focus := range []int{0, sandboxColCount} {
		m.setFocus(focus)
		scr := uv.NewScreenBuffer(width, sandboxTestH)
		m.Draw(scr, image.Rect(0, 0, width, sandboxTestH))

		var rows int
		for line := range strings.SplitSeq(ansi.Strip(scr.Render()), "\n") {
			if !strings.Contains(line, "│ > ") {
				continue
			}
			rows++
			require.Regexp(t, `RW\s+-\s+│\s*$`, line)
		}
		require.Equal(t, len(m.rows), rows)
	}
}

// TestSandbox_DefaultConfigIncludesContainerGrants verifies that the
// sockets, which have no row, still reach the submitted config.
func TestSandbox_DefaultConfigIncludesContainerGrants(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles: &s,
		Workspace: &sandboxTestWorkspace{
			dir:    t.TempDir(),
			grants: sandbox.ContainerGrants{UnixSockets: []string{"/var/run/docker.sock"}},
		},
	}
	m := NewSandbox(com)

	require.Equal(t, []string{"/var/run/docker.sock"}, m.config().UnixSockets)
}

// TestSandbox_NoContainerGrantsLeavesConfigUnchanged verifies the
// opted-out path: with nothing reported, no socket reaches the config.
func TestSandbox_NoContainerGrantsLeavesConfigUnchanged(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	require.Empty(t, m.config().UnixSockets)
}

// TestSandbox_ConfirmStageListsUnixSockets verifies the confirmation
// screen names the sockets about to be opened, since a container
// daemon socket is the broadest grant the dialog can add.
func TestSandbox_ConfirmStageListsUnixSockets(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles: &s,
		Workspace: &sandboxTestWorkspace{
			dir:    t.TempDir(),
			grants: sandbox.ContainerGrants{UnixSockets: []string{"/var/run/docker.sock"}},
		},
	}
	m := NewSandbox(com)
	m.submit()

	scr := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	out := ansi.Strip(scr.Render())
	require.Contains(t, out, "Unix sockets (1)")
	require.Contains(t, out, "docker.sock")
}

func socketTarget(idx int) func(sandboxHitTarget) bool {
	return func(tgt sandboxHitTarget) bool { return tgt.area == sandboxFocusSocket && tgt.row == idx }
}

// newSandboxWithOptionalSockets builds a dialog whose workspace detected
// two daemon sockets that were not granted.
func newSandboxWithOptionalSockets(t *testing.T) *Sandbox {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles: &s,
		Workspace: &sandboxTestWorkspace{
			dir: t.TempDir(),
			grants: sandbox.ContainerGrants{
				OptionalSockets: []string{"/var/run/docker.sock", "/run/podman/podman.sock"},
			},
		},
	}
	return NewSandbox(com)
}

// TestSandbox_OptionalSocketsStartBlocked verifies a detected socket
// that was not opted into is offered in the form but left out of the
// config until the user turns it on.
func TestSandbox_OptionalSocketsStartBlocked(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	require.Len(t, m.sockets, 2)
	require.Empty(t, m.config().UnixSockets)
}

// TestSandbox_PreselectedSocketStartsAllowed verifies an optional socket
// the workspace preselected starts allowed while the other detected ones
// stay blocked, and that the user can still turn it off.
func TestSandbox_PreselectedSocketStartsAllowed(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles: &s,
		Workspace: &sandboxTestWorkspace{
			dir: t.TempDir(),
			grants: sandbox.ContainerGrants{
				OptionalSockets:    []string{"/var/run/docker.sock", "/run/podman/podman.sock"},
				PreselectedSockets: []string{"/run/podman/podman.sock"},
			},
		},
	}
	m := NewSandbox(com)

	require.Len(t, m.sockets, 2)
	require.False(t, m.sockets[0].allowed)
	require.True(t, m.sockets[1].allowed)
	require.Equal(t, []string{"/run/podman/podman.sock"}, m.config().UnixSockets)

	m.setFocus(m.socketStop(1))
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Empty(t, m.config().UnixSockets)
}

// TestSandbox_GrantedSocketsStartAllowed verifies a socket the workspace
// already granted starts allowed, can be turned off, and is not offered
// twice when it is also listed as optional.
func TestSandbox_GrantedSocketsStartAllowed(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles: &s,
		Workspace: &sandboxTestWorkspace{
			dir: t.TempDir(),
			grants: sandbox.ContainerGrants{
				UnixSockets:     []string{"/var/run/docker.sock"},
				OptionalSockets: []string{"/var/run/docker.sock"},
			},
		},
	}
	m := NewSandbox(com)

	require.Len(t, m.sockets, 1)
	require.Equal(t, []string{"/var/run/docker.sock"}, m.config().UnixSockets)

	m.setFocus(m.socketStop(0))
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Empty(t, m.config().UnixSockets)
}

// TestSandbox_SocketToggleByKeyboard verifies each socket is its own
// focus stop between the network toggle and the continue button, and
// that enter flips only the focused one.
func TestSandbox_SocketToggleByKeyboard(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	require.Equal(t, len(m.rows)*sandboxColCount+3+2, m.stopCount())

	m.setFocus(m.socketStop(1))
	area, idx, _ := m.focusTarget(m.focused)
	require.Equal(t, sandboxFocusSocket, area)
	require.Equal(t, 1, idx)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.sockets[0].allowed)
	require.True(t, m.sockets[1].allowed)
	require.Equal(t, []string{"/run/podman/podman.sock"}, m.config().UnixSockets)

	area, _, _ = m.focusTarget(m.stopCount() - 1)
	require.Equal(t, sandboxFocusContinue, area)
}

// TestSandbox_SocketToggleByClick verifies clicking a socket line flips
// it and focuses it.
func TestSandbox_SocketToggleByClick(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	drawSandbox(m)

	x, y := targetPoint(t, m, socketTarget(0))
	require.Nil(t, m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft}))
	require.True(t, m.sockets[0].allowed)
	require.False(t, m.sockets[1].allowed)
	require.True(t, m.isFocused(sandboxFocusSocket, 0, 0))
	require.Equal(t, []string{"/var/run/docker.sock"}, m.config().UnixSockets)
}

// TestSandbox_ClickOnToggleButtonFlipsSetting verifies the hit target
// spans the whole line, so clicking the status button at its right end
// flips the setting just like clicking the label.
func TestSandbox_ClickOnToggleButtonFlipsSetting(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	drawSandbox(m)

	endOf := func(want func(sandboxHitTarget) bool) (x, y int) {
		for _, tgt := range m.hitTargets {
			if want(tgt) {
				return tgt.rect.Max.X - 2, tgt.rect.Min.Y
			}
		}
		t.Fatal("target not found")
		return 0, 0
	}

	before := m.allowNetwork
	x, y := endOf(networkTarget())
	m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	require.Equal(t, !before, m.allowNetwork)

	x, y = endOf(socketTarget(1))
	m.HandleMsg(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	require.True(t, m.sockets[1].allowed)
	require.False(t, m.sockets[0].allowed)
}

// TestSandbox_AllowedSocketIsDrawnAsAllowed verifies a socket's state
// reads from the button on its own line.
func TestSandbox_AllowedSocketIsDrawnAsAllowed(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	m.sockets[0].allowed = true // docker.sock; podman.sock stays blocked
	scr := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	for line := range strings.SplitSeq(ansi.Strip(scr.Render()), "\n") {
		switch {
		case strings.Contains(line, "docker.sock"):
			require.Contains(t, line, "Allowed")
		case strings.Contains(line, "podman.sock"):
			require.Contains(t, line, "Blocked")
		}
	}
}

// TestSandbox_SocketsAreDrawn verifies the form names every detected
// socket under its own section heading.
func TestSandbox_SocketsAreDrawn(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	scr := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	out := ansi.Strip(scr.Render())
	require.Contains(t, out, "Container access")
	require.Contains(t, out, "/var/run/docker.sock")
	require.Contains(t, out, "/run/podman/podman.sock")
}

// TestSandbox_NoSocketsHidesSection verifies a machine without a
// detected socket gets no socket section and no extra focus stops.
func TestSandbox_NoSocketsHidesSection(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	require.Empty(t, m.sockets)
	require.Equal(t, len(m.rows)*sandboxColCount+3, m.stopCount())

	scr := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxTestH))
	require.NotContains(t, ansi.Strip(scr.Render()), "Container access")
}

// TestSandbox_ConfirmStageListsOnlyAllowedSockets verifies the
// confirmation screen names a socket only after it was turned on.
func TestSandbox_ConfirmStageListsOnlyAllowedSockets(t *testing.T) {
	t.Parallel()

	m := newSandboxWithOptionalSockets(t)
	m.sockets[1].allowed = true
	m.submit()

	scr := uv.NewScreenBuffer(sandboxTestW, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, sandboxTestH))

	out := ansi.Strip(scr.Render())
	require.Contains(t, out, "Unix sockets (1)")
	require.Contains(t, out, "podman.sock")
	require.NotContains(t, out, "docker.sock")
}

// newConfirmSandbox returns a sandbox on its confirm stage whose only
// paths are n read-write ones named /data/path-00, /data/path-01, ...
func newConfirmSandbox(t *testing.T, n int) *Sandbox {
	t.Helper()

	m := newTestSandbox(t)
	m.rows = nil
	for i := range n {
		m.addRow()
		m.rows[len(m.rows)-1].input.SetValue(fmt.Sprintf("/data/path-%02d", i))
	}
	m.submit()
	return m
}

func drawSandboxConfirm(m *Sandbox, height int) string {
	scr := uv.NewScreenBuffer(sandboxTestW, height)
	m.Draw(scr, image.Rect(0, 0, sandboxTestW, height))
	return ansi.Strip(scr.Render())
}

func TestSandbox_ConfirmManyPathsKeepsButtonsOnScreen(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 25)
	out := drawSandboxConfirm(m, sandboxShortH)

	require.Positive(t, m.confirmHeight)
	require.Contains(t, out, "Read-write (")
	require.Contains(t, out, "path-00")
	require.NotContains(t, out, "path-24", "the last path starts below the fold")
	require.Contains(t, out, "Network:")
	require.Contains(t, out, "Enter Sandbox")
	require.Contains(t, out, "Cancel")
	require.Contains(t, out, "undone")

	lines := strings.Split(out, "\n")
	for idx, text := range []string{"Enter Sandbox", "Cancel"} {
		_, y := compositorButtonPos(t, m.buttonHit, idx, sandboxTestW, sandboxShortH)
		require.Contains(t, lines[y], text, "button %d hit area must sit on its own row", idx)
	}
}

func TestSandbox_ConfirmButtonClickWorksWhileScrolled(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 25)
	drawSandboxConfirm(m, sandboxShortH)
	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 4})
	drawSandboxConfirm(m, sandboxShortH)

	x, y := compositorButtonPos(t, m.buttonHit, 0, sandboxTestW, sandboxShortH)
	action := m.HandleMsg(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	_, ok := action.(ActionEnterSandbox)
	require.True(t, ok, "clicking Enter Sandbox should confirm, got %T", action)
}

func TestSandbox_ConfirmWheelScrollsAndClamps(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 25)
	drawSandboxConfirm(m, sandboxShortH)
	require.Zero(t, m.confirmOffset)

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 3})
	out := drawSandboxConfirm(m, sandboxShortH)
	require.Equal(t, 3, m.confirmOffset)
	require.Contains(t, out, "Enter Sandbox")

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 1000})
	out = drawSandboxConfirm(m, sandboxShortH)
	require.Positive(t, m.confirmOffset)
	require.Contains(t, out, "path-24", "scrolled to the bottom, the last path is visible")
	require.NotContains(t, out, "path-00")
	require.Contains(t, out, "Enter Sandbox")

	atBottom := m.confirmOffset
	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 1})
	drawSandboxConfirm(m, sandboxShortH)
	require.Equal(t, atBottom, m.confirmOffset, "offset must not run past the end")

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: -1000})
	out = drawSandboxConfirm(m, sandboxShortH)
	require.Zero(t, m.confirmOffset)
	require.Contains(t, out, "path-00")
}

func TestSandbox_ConfirmKeysScroll(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 25)
	drawSandboxConfirm(m, sandboxShortH)
	page := m.confirmHeight

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, m.confirmOffset)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Zero(t, m.confirmOffset)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Equal(t, page, m.confirmOffset)

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyPgUp})
	require.Zero(t, m.confirmOffset)

	// Scrolling must not disturb which button is selected.
	require.False(t, m.selectedNo)
}

// TestSandbox_ConfirmListUsesMoreThanFormHeightBudget verifies the
// confirm stage may grow past the form's share of the screen, so its
// path list keeps room on a short terminal.
func TestSandbox_ConfirmListUsesMoreThanFormHeightBudget(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 25)
	out := drawSandboxConfirm(m, sandboxShortH)

	rows := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	require.Greater(t, rows, int(sandboxHeightRatio*sandboxShortH))
	require.LessOrEqual(t, rows, sandboxShortH)
}

func TestSandbox_ConfirmFewPathsDoNotScroll(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 3)
	out := drawSandboxConfirm(m, sandboxTestH)
	require.Contains(t, out, "path-02")
	require.Contains(t, out, "Network:")

	m.HandleMsg(common.CoalescedWheelMsg{DeltaY: 5})
	drawSandboxConfirm(m, sandboxTestH)
	require.Zero(t, m.confirmOffset)
}

func TestSandbox_ConfirmListsEveryPath(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 12)
	out := drawSandboxConfirm(m, 60)

	require.Contains(t, out, "path-11")
	require.NotContains(t, out, "more")
	require.Zero(t, m.confirmOffset)
}

// confirmFrameWidth is the display width of the dialog border on a
// wide screen, found from its top edge.
func confirmFrameWidth(t *testing.T, out string) int {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "╭") {
			return lipgloss.Width(strings.TrimSpace(line))
		}
	}
	t.Fatal("dialog border not found")
	return 0
}

// TestSandbox_ConfirmWidthIsCapped verifies a long hint and long
// paths neither stretch the dialog past the form's width nor wrap
// the hint away.
func TestSandbox_ConfirmWidthIsCapped(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 3)
	m.rows[0].input.SetValue("/data/" + strings.Repeat("deeply-nested/", 20) + "file")
	scr := uv.NewScreenBuffer(200, sandboxTestH)
	m.Draw(scr, image.Rect(0, 0, 200, sandboxTestH))
	out := ansi.Strip(scr.Render())

	require.LessOrEqual(t, confirmFrameWidth(t, out), m.frame.Measure(image.Rect(0, 0, 200, sandboxTestH)).Width)
	require.Contains(t, out, "…", "an over-long path is clipped")
	require.Contains(t, out, "undone")
	require.Contains(t, out, "process.")
}

// TestSandbox_ConfirmScrollbarSitsAtTheRightEdge verifies the
// scrollbar hugs the dialog's right edge instead of trailing the
// longest hidden path.
func TestSandbox_ConfirmScrollbarSitsAtTheRightEdge(t *testing.T) {
	t.Parallel()

	m := newConfirmSandbox(t, 25)
	out := drawSandboxConfirm(m, sandboxShortH)

	var barLine string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.ContainsAny(line, styles.ScrollbarThumb+styles.ScrollbarTrack) {
			barLine = line
			break
		}
	}
	require.NotEmpty(t, barLine, "a scrollbar must be drawn")

	// Past the bar only the frame's right padding and border remain.
	frame := m.com.Styles.Dialog.Quit.Frame
	rest := strings.TrimSpace(barLine[strings.IndexAny(barLine, styles.ScrollbarThumb+styles.ScrollbarTrack):])
	require.Equal(t, 1+frame.GetPaddingRight()+frame.GetBorderRightSize(), lipgloss.Width(rest))
}
