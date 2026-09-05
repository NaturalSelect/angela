package dialog

import (
	"image"
	"testing"

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
	require.Equal(t, rowCount*sandboxColCount+2, m.stopCount())

	// Walking Next stopCount times must land back on the first row's
	// input.
	for range m.stopCount() {
		m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	area, row, col := m.focusTarget(m.focused)
	require.Equal(t, sandboxFocusRow, area)
	require.Equal(t, 0, row)
	require.Equal(t, sandboxColInput, col)

	// Move focus to the network toggle (the last stop) and activate it
	// with space. Space must be sent as tea.KeySpace, not the raw rune:
	// ultraviolet's Keystroke() special-cases it (see undo_test.go).
	m.setFocus(m.stopCount() - 1)
	before := m.allowNetwork
	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Equal(t, !before, m.allowNetwork)
}
