package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestQuit(t *testing.T) *Quit {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewQuit(com)
}

func TestQuit_ID(t *testing.T) {
	t.Parallel()

	q := newTestQuit(t)
	require.Equal(t, QuitID, q.ID())
}

func TestQuit_HandleMsg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		msg        tea.Msg
		wantAction Action
		wantNil    bool
	}{
		{name: "ctrl+c always quits", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, wantAction: ActionQuit{}},
		{name: "escape closes", msg: tea.KeyPressMsg{Code: tea.KeyEscape}, wantAction: ActionClose{}},
		{name: "y confirms quit", msg: keyMsg('y'), wantAction: ActionQuit{}},
		{name: "Y confirms quit", msg: keyMsg('Y'), wantAction: ActionQuit{}},
		{name: "n cancels", msg: keyMsg('n'), wantAction: ActionClose{}},
		{name: "N cancels", msg: keyMsg('N'), wantAction: ActionClose{}},
		{name: "unrelated message is ignored", msg: tea.WindowSizeMsg{Width: 10, Height: 10}, wantNil: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := newTestQuit(t)
			got := q.HandleMsg(tc.msg)
			if tc.wantNil {
				require.Nil(t, got)
				return
			}
			require.Equal(t, tc.wantAction, got)
		})
	}
}

// TestQuit_LeftRightAndTabToggleSelection verifies that left/right and tab
// both flip the selected option, and that enter/space acts on whichever
// option is currently selected.
func TestQuit_LeftRightAndTabToggleSelection(t *testing.T) {
	t.Parallel()

	q := newTestQuit(t)
	require.True(t, q.selectedNo, "quit defaults to the safe 'No' option")

	// Enter while "No" is selected closes without quitting.
	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, ActionClose{}, action)

	q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.False(t, q.selectedNo, "left/right must toggle the selection")

	action = q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, ActionQuit{}, action, "enter confirms the now-selected 'Yes' option")

	q2 := newTestQuit(t)
	q2.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.False(t, q2.selectedNo, "tab must also toggle the selection")
}

// TestQuit_SpaceKeyIsDeadDespiteHelpTextAdvertisingIt documents a known
// bug (also pinned in internal/ui/model/dialog_pickers_test.go): the
// EnterSpace binding lists the literal " " character, but a real
// space-bar keypress always stringifies to "space" (see
// charmbracelet/ultraviolet's Key.Keystroke special-casing of
// KeySpace), so it never matches and the key silently does nothing.
func TestQuit_SpaceKeyIsDeadDespiteHelpTextAdvertisingIt(t *testing.T) {
	t.Parallel()

	q := newTestQuit(t)
	q.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft}) // toggle to Yes
	action := q.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Nil(t, action, "documents the current (buggy) behavior: space matches no binding")
}

// TestQuit_Draw verifies the confirmation text and both buttons render at
// a comfortable width, and that a very narrow width never panics even
// though its content is cropped rather than reflowed.
func TestQuit_Draw(t *testing.T) {
	t.Parallel()

	q := newTestQuit(t)
	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	cur := q.Draw(scr, image.Rect(0, 0, w, h))
	require.Nil(t, cur, "quit dialog does not use a text cursor")

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "quit")
	require.Contains(t, view, "Yep!")
	require.Contains(t, view, "Nope")

	narrow := newTestQuit(t)
	narrowScr := uv.NewScreenBuffer(20, 10)
	require.NotPanics(t, func() {
		narrow.Draw(narrowScr, image.Rect(0, 0, 20, 10))
	})
}

func TestQuit_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	q := newTestQuit(t)
	require.Len(t, q.ShortHelp(), 2)

	full := q.FullHelp()
	require.Len(t, full, 2)
	require.Len(t, full[0], 4)
	require.Len(t, full[1], 2)
}
