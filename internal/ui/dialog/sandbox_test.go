package dialog

import (
	"image"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
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

	dir string
}

func (w *sandboxTestWorkspace) WorkingDir() string     { return w.dir }
func (w *sandboxTestWorkspace) Config() *config.Config { return nil }

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

// TestSandbox_NetworkViewUsesFocusedLabelStyle verifies that the
// "Network" label switches to the focused style once the network
// toggle has focus.
func TestSandbox_NetworkViewUsesFocusedLabelStyle(t *testing.T) {
	t.Parallel()

	m := newTestSandbox(t)
	blurred, _ := m.networkView(2)

	m.setFocus(m.stopCount() - 2) // the network toggle is second-to-last
	focused, _ := m.networkView(2)

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
