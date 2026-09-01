package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func charKeyMsg(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// newCompactPaletteUI builds a UI with the real Commands dialog open
// (hasSession=true, so "Summarize Session" is registered) and a
// gomock-generated MockWorkspace standing in for everything past the
// UI/dialog boundary. Whatever the mock is (or is not) told to expect is
// exactly what must (or must not) leave the UI layer: an unset expectation
// that gets called, or a set expectation that never fires, fails the test.
func newCompactPaletteUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()

	// The dialog's own command-list construction reads global config
	// (Docker MCP toggle wording, transparent-background toggle) that has
	// nothing to do with the compact path; stub it so those unrelated
	// reads do not themselves become unexpected calls.
	ws.EXPECT().Config().Return(&config.Config{}).AnyTimes()

	sess := session.Session{ID: "current"}
	sty := styles.CharmtonePantera()
	com := &common.Common{Workspace: ws, Styles: &sty}
	cmdsDialog, err := dialog.NewCommands(com, sess.ID, true, false, false, false, false, nil, nil, nil)
	require.NoError(t, err)

	return &UI{
		com:        com,
		session:    &sess,
		agentReady: true,
		dialog:     dialog.NewOverlay(cmdsDialog),
	}
}

// TestCompactPaletteKeyboardSelectionReachesWorkspace drives the real
// Commands dialog by keyboard exactly like a user: type "compact" to
// filter the palette down to the Summarize Session entry, then press
// enter. This is the regression test for the actual root cause: the
// palette ranks items by github.com/sahilm/fuzzy score, and until the
// "Toggle Compact Mode" item was renamed, "compact" scored higher against
// its title (match starting at index 7) than against "Summarize Session"
// (match starting at index 18, inside the alias suffix) — so the exact,
// documented alias for this command silently toggled a UI layout flag
// instead of summarizing. This is the baseline the mouse case below is
// compared against.
func TestCompactPaletteKeyboardSelectionReachesWorkspace(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsSessionBusy("current").Return(false)
	ws.EXPECT().AgentSummarize(gomock.Any(), "current").Return(nil)

	m := newCompactPaletteUI(t, ws)

	for _, r := range "compact" {
		drain(m.handleDialogMsg(charKeyMsg(r)))
	}
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "selecting compact by keyboard produced no command")
	drain(cmd)
}

// TestCompactPaletteMouseClickNeverReachesWorkspace is the actual bug:
// dialog.Commands.HandleMsg switches only on tea.KeyPressMsg (plus two
// internal messages), and CommandItem implements no mouse-click
// interface, so a mouse click on "Summarize Session" is silently dropped
// by dialog.Overlay.Update before it ever becomes an Action — no toast,
// no error, nothing. The mock has zero expectations: any call at all
// (busy check or summarize) fails the test, which matches exactly what a
// user experiences as a click that does nothing.
func TestCompactPaletteMouseClickNeverReachesWorkspace(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)

	m := newCompactPaletteUI(t, ws)

	for _, r := range "compact" {
		drain(m.handleDialogMsg(charKeyMsg(r)))
	}

	click := tea.MouseClickMsg(tea.Mouse{X: 5, Y: 5, Button: uv.MouseLeft})
	cmd := m.handleDialogMsg(click)
	require.Nil(t, cmd, "a mouse click on the command item produced a command — "+
		"the dialog now handles clicks and this test's premise is stale")
}
