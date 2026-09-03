package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// pickerDlgWithConfig builds a UI wired like newDialogUI (open_dialog_test.go)
// but with a caller-supplied config, so tests can control exactly which
// agents/providers are configured instead of always getting the single
// "coder" primary agent newDialogUI hardcodes.
func pickerDlgWithConfig(t *testing.T, ws *MockWorkspace, cfg *config.Config) *UI {
	t.Helper()

	ws.EXPECT().Config().Return(cfg).AnyTimes()

	sty := styles.CharmtonePantera()
	m := &UI{
		com:     &common.Common{Workspace: ws, Styles: &sty},
		session: &session.Session{ID: "s1"},
		dialog:  dialog.NewOverlay(),
	}
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = "s1"
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		ModelCfg:   config.SelectedModel{Model: "test-model"},
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ReasoningLevels: []string{"low", "high"}}},
	}
	return m
}

// pickerDlgThreeAgents configures three switchable primary agents (coder,
// reviewer, writer — alphabetical, matching switchableAgents' sort order)
// so navigation tests can exercise real multi-item wrap-around.
func pickerDlgThreeAgents(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()
	return pickerDlgWithConfig(t, ws, &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{TUI: &config.TUIOptions{}},
		Agents: map[string]config.Agent{
			"coder":    {ID: "coder", Name: "Coder", Mode: config.AgentModePrimary},
			"reviewer": {ID: "reviewer", Name: "Reviewer", Mode: config.AgentModePrimary},
			"writer":   {ID: "writer", Name: "Writer", Mode: config.AgentModePrimary},
		},
	})
}

const noMatchFilter = "zzzzz_NO_MATCH_zzzzz"

// ---------------------------------------------------------------------
// Agents dialog
// ---------------------------------------------------------------------

func TestAgentsDialog_OpenRequiresSession(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.session = nil

	cmd := m.openAgentsDialog()
	require.NotNil(t, cmd)
	msg := cmd()
	info, ok := msg.(util.InfoMsg)
	require.True(t, ok, "expected util.InfoMsg, got %T", msg)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Start a session")
	require.False(t, m.dialog.ContainsDialog(dialog.AgentsID))
}

func TestAgentsDialog_OpenRequiresActiveAgent(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.agentActiveKnown = false // active agent probe hasn't landed yet

	cmd := m.openAgentsDialog()
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "starting up")
	require.False(t, m.dialog.ContainsDialog(dialog.AgentsID))
}

func TestAgentsDialog_OpenNoConfiguredAgentsReportsError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := pickerDlgWithConfig(t, ws, &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{TUI: &config.TUIOptions{}},
		Agents:    map[string]config.Agent{}, // no primary agents at all
	})

	cmd := m.openAgentsDialog()
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeError, info.Type)
	require.Contains(t, info.Msg, "no primary agents configured")
	require.False(t, m.dialog.ContainsDialog(dialog.AgentsID))
}

func TestAgentsDialog_ReopenBringsToFrontNotDuplicated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.Nil(t, m.openAgentsDialog())
	require.Nil(t, m.openAgentsDialog()) // must bring-to-front, not stack a second copy

	// CloseDialog removes only the first matching entry; if a duplicate
	// had been stacked, the dialog would still be present after one close.
	m.dialog.CloseDialog(dialog.AgentsID)
	require.False(t, m.dialog.ContainsDialog(dialog.AgentsID),
		"opening an already-open Agents dialog must not stack a duplicate")
}

func TestAgentsDialog_NavigationWrapsWithSingleAgent(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := pickerDlgWithConfig(t, ws, &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{TUI: &config.TUIOptions{}},
		Agents: map[string]config.Agent{
			"solo": {ID: "solo", Name: "Solo", Mode: config.AgentModePrimary},
		},
	})
	m.agentActive.AgentID = "solo"
	require.Nil(t, m.openAgentsDialog())

	require.NotPanics(t, func() {
		for range 5 {
			m.dialog.Update(keyMsg("down"))
			m.dialog.Update(keyMsg("up"))
		}
	})

	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectAgent{AgentID: "solo"}, action,
		"repeated wraparound on a single-item list must leave the only item selected")
}

func TestAgentsDialog_NavigationWrapsWithMultipleAgents(t *testing.T) {
	t.Parallel()

	t.Run("previous from first wraps to last", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := pickerDlgThreeAgents(t, ws)
		require.Nil(t, m.openAgentsDialog())

		m.dialog.Update(keyMsg("up")) // coder (first) -> wraps to writer (last)
		action := m.dialog.Update(keyMsg("enter"))
		require.Equal(t, dialog.ActionSelectAgent{AgentID: "writer"}, action)
	})

	t.Run("next from last wraps to first", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := pickerDlgThreeAgents(t, ws)
		require.Nil(t, m.openAgentsDialog())

		m.dialog.Update(keyMsg("up"))   // coder -> writer (last)
		m.dialog.Update(keyMsg("down")) // writer -> wraps to coder (first)
		action := m.dialog.Update(keyMsg("enter"))
		require.Equal(t, dialog.ActionSelectAgent{AgentID: "coder"}, action)
	})

	t.Run("selecting the already-current agent still dispatches it", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := pickerDlgThreeAgents(t, ws)
		require.Nil(t, m.openAgentsDialog())

		action := m.dialog.Update(keyMsg("enter")) // no navigation: current agent stays selected
		require.Equal(t, dialog.ActionSelectAgent{AgentID: "coder"}, action)
	})
}

func TestAgentsDialog_FullChain_OpenNavigateSelect(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := pickerDlgThreeAgents(t, ws)

	require.Nil(t, m.openAgentsDialog())
	require.True(t, m.dialog.ContainsDialog(dialog.AgentsID))

	m.dialog.Update(keyMsg("down")) // coder -> reviewer
	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectAgent{AgentID: "reviewer"}, action)
}

func TestAgentsDialog_FilterToNoMatchThenEnterIsSafe(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := pickerDlgThreeAgents(t, ws)
	require.Nil(t, m.openAgentsDialog())

	m.dialog.Update(keyMsg(noMatchFilter))

	var action dialog.Action
	require.NotPanics(t, func() {
		action = m.dialog.Update(keyMsg("enter"))
	})
	_, isSelect := action.(dialog.ActionSelectAgent)
	require.False(t, isSelect, "enter on an empty filtered list must not select anything")
}

func TestAgentsDialog_FilterAdversarialInputDoesNotPanic(t *testing.T) {
	t.Parallel()

	adversarial := []string{
		"",
		" ",
		"\t\n",
		"(a)[b].*\\c+",
		"日本語🎉テスト",
		stringsRepeat("x", 500),
	}

	for _, input := range adversarial {
		input := input
		t.Run("filter_"+truncateForName(input), func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			ws := NewMockWorkspace(ctrl)
			m := pickerDlgThreeAgents(t, ws)
			require.Nil(t, m.openAgentsDialog())

			require.NotPanics(t, func() {
				m.dialog.Update(keyMsg(input))
				m.dialog.Update(keyMsg("enter"))
				m.dialog.Update(keyMsg("esc"))
			})
		})
	}
}

func TestAgentsDialog_CloseKeyAlwaysCloses(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := pickerDlgThreeAgents(t, ws)
	require.Nil(t, m.openAgentsDialog())

	m.dialog.Update(keyMsg(noMatchFilter)) // mid-filter, nothing matches
	action := m.dialog.Update(keyMsg("esc"))
	require.Equal(t, dialog.ActionClose{}, action)
}

// ---------------------------------------------------------------------
// Variants dialog
// ---------------------------------------------------------------------

func TestVariantsDialog_OpenRequiresSession(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.session = nil

	cmd := m.openVariantsDialog()
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "Start a session")
	require.False(t, m.dialog.ContainsDialog(dialog.VariantsID))
}

func TestVariantsDialog_OpenRequiresActiveAgent(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.agentActiveKnown = false

	cmd := m.openVariantsDialog()
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Contains(t, info.Msg, "starting up")
	require.False(t, m.dialog.ContainsDialog(dialog.VariantsID))
}

func TestVariantsDialog_OpenWithZeroVariantsWarns(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.agentActive.CatwalkCfg = config.ProviderModel{} // no reasoning levels
	m.agentActive.ModelCfg = config.SelectedModel{}

	cmd := m.openVariantsDialog()
	require.NotNil(t, cmd)
	info, ok := cmd().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, info.Type)
	require.Contains(t, info.Msg, "no variants")
	require.False(t, m.dialog.ContainsDialog(dialog.VariantsID))
}

func TestVariantsDialog_ReopenBringsToFrontNotDuplicated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.Nil(t, m.openVariantsDialog())
	require.Nil(t, m.openVariantsDialog())

	m.dialog.CloseDialog(dialog.VariantsID)
	require.False(t, m.dialog.ContainsDialog(dialog.VariantsID),
		"opening an already-open Variants dialog must not stack a duplicate")
}

func TestVariantsDialog_BaselineIsFirstAndSelectable(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws) // ReasoningLevels: ["low", "high"], current == ""
	require.Nil(t, m.openVariantsDialog())

	// No navigation: baseline is first because current variant is "".
	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectVariant{Variant: ""}, action)
}

func TestVariantsDialog_NavigationWrapsAcrossBaselineAndLevels(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws) // items: [Baseline, low, high]
	require.Nil(t, m.openVariantsDialog())

	m.dialog.Update(keyMsg("up")) // Baseline (first) -> wraps to "high" (last)
	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectVariant{Variant: "high"}, action)
}

func TestVariantsDialog_FilterToNoMatchThenEnterIsSafe(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openVariantsDialog())

	m.dialog.Update(keyMsg(noMatchFilter))

	var action dialog.Action
	require.NotPanics(t, func() {
		action = m.dialog.Update(keyMsg("enter"))
	})
	_, isSelect := action.(dialog.ActionSelectVariant)
	require.False(t, isSelect)
}

func TestVariantsDialog_FilterAdversarialInputDoesNotPanic(t *testing.T) {
	t.Parallel()

	adversarial := []string{"", " ", "[.*]", "🎉variant🎉", stringsRepeat("v", 500)}
	for _, input := range adversarial {
		input := input
		t.Run("filter_"+truncateForName(input), func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			ws := NewMockWorkspace(ctrl)
			m := newDialogUI(t, ws)
			require.Nil(t, m.openVariantsDialog())

			require.NotPanics(t, func() {
				m.dialog.Update(keyMsg(input))
				m.dialog.Update(keyMsg("enter"))
			})
		})
	}
}

// ---------------------------------------------------------------------
// Notifications dialog
// ---------------------------------------------------------------------

func TestNotificationsDialog_OpenHasNoGuards(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.session = nil // notifications are a global preference, unlike Agents/Variants

	require.Nil(t, m.openNotificationsDialog())
	require.True(t, m.dialog.ContainsDialog(dialog.NotificationsID))
}

func TestNotificationsDialog_ReopenBringsToFrontNotDuplicated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.Nil(t, m.openNotificationsDialog())
	require.Nil(t, m.openNotificationsDialog())

	m.dialog.CloseDialog(dialog.NotificationsID)
	require.False(t, m.dialog.ContainsDialog(dialog.NotificationsID))
}

func TestNotificationsDialog_DefaultSelectionIsFirstStyle(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openNotificationsDialog())

	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectNotificationStyle{Style: "auto"}, action)
}

func TestNotificationsDialog_NavigationWrapsAcrossAllFiveStyles(t *testing.T) {
	t.Parallel()

	t.Run("previous from first wraps to last (disabled)", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := newDialogUI(t, ws)
		require.Nil(t, m.openNotificationsDialog())

		m.dialog.Update(keyMsg("up"))
		action := m.dialog.Update(keyMsg("enter"))
		require.Equal(t, dialog.ActionSelectNotificationStyle{Style: "disabled"}, action)
	})

	t.Run("next four times lands on last (disabled)", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := newDialogUI(t, ws)
		require.Nil(t, m.openNotificationsDialog())

		for range 4 {
			m.dialog.Update(keyMsg("down"))
		}
		action := m.dialog.Update(keyMsg("enter"))
		require.Equal(t, dialog.ActionSelectNotificationStyle{Style: "disabled"}, action)
	})

	t.Run("next five times wraps back to first (auto)", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := newDialogUI(t, ws)
		require.Nil(t, m.openNotificationsDialog())

		for range 5 {
			m.dialog.Update(keyMsg("down"))
		}
		action := m.dialog.Update(keyMsg("enter"))
		require.Equal(t, dialog.ActionSelectNotificationStyle{Style: "auto"}, action)
	})
}

func TestNotificationsDialog_FilterNarrowsToExactMatch(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openNotificationsDialog())

	m.dialog.Update(keyMsg("o"))
	m.dialog.Update(keyMsg("s"))
	m.dialog.Update(keyMsg("c"))
	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionSelectNotificationStyle{Style: "osc"}, action)
}

func TestNotificationsDialog_FilterToNoMatchThenEnterIsSafe(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openNotificationsDialog())

	m.dialog.Update(keyMsg(noMatchFilter))

	var action dialog.Action
	require.NotPanics(t, func() {
		action = m.dialog.Update(keyMsg("enter"))
	})
	_, isSelect := action.(dialog.ActionSelectNotificationStyle)
	require.False(t, isSelect)
}

func TestNotificationsDialog_CloseKeyAlwaysCloses(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openNotificationsDialog())

	action := m.dialog.Update(keyMsg("esc"))
	require.Equal(t, dialog.ActionClose{}, action)
}

// ---------------------------------------------------------------------
// Quit dialog
// ---------------------------------------------------------------------

func TestQuitDialog_OpenHasNoGuards(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	m.session = nil

	require.Nil(t, m.openQuitDialog())
	require.True(t, m.dialog.ContainsDialog(dialog.QuitID))
}

func TestQuitDialog_ReopenBringsToFrontNotDuplicated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)

	require.Nil(t, m.openQuitDialog())
	require.Nil(t, m.openQuitDialog())

	m.dialog.CloseDialog(dialog.QuitID)
	require.False(t, m.dialog.ContainsDialog(dialog.QuitID))
}

// TestQuitDialog_DefaultIsSafeNo pins that a bare enter/space press, with
// no prior navigation, closes rather than quits. The dialog defaults its
// selection to "No" specifically so a reflexive enter-press (e.g. muscle
// memory from dismissing some other prompt) cannot quit the app.
func TestQuitDialog_DefaultIsSafeNo(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openQuitDialog())

	action := m.dialog.Update(keyMsg("enter"))
	require.Equal(t, dialog.ActionClose{}, action)
}

func TestQuitDialog_ToggleThenConfirm(t *testing.T) {
	t.Parallel()

	t.Run("single toggle then enter quits", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := newDialogUI(t, ws)
		require.Nil(t, m.openQuitDialog())

		m.dialog.Update(keyMsg("left"))
		action := m.dialog.Update(keyMsg("enter"))
		_, isQuit := action.(dialog.ActionQuit)
		require.True(t, isQuit, "toggling to Yes then confirming must quit, got %#v", action)
	})

	t.Run("double toggle returns to safe No", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := newDialogUI(t, ws)
		require.Nil(t, m.openQuitDialog())

		m.dialog.Update(keyMsg("tab"))
		m.dialog.Update(keyMsg("tab"))
		action := m.dialog.Update(keyMsg("enter"))
		require.Equal(t, dialog.ActionClose{}, action)
	})

	// TestQuitDialog_SpaceKeyIsDeadDespiteHelpTextAdvertisingIt is a
	// confirmed bug, not a test assumption: quit.go binds EnterSpace to
	// key.WithKeys("enter", " ") (a literal space character), but
	// ultraviolet's Key.Keystroke() special-cases KeySpace (== rune(' '))
	// to always render as the word "space" (see keyTypeString in
	// charmbracelet/ultraviolet key.go). key.Matches compares that
	// rendered string against the binding's registered names, so a real
	// space-bar press can never equal the literal " " the binding
	// expects — the key silently does nothing, even though the dialog's
	// own help text reads "enter/space: confirm". The fix belongs in
	// quit.go (key.WithKeys("enter", "space")), not in this test.
	t.Run("space key is dead despite help text advertising it (bug)", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		m := newDialogUI(t, ws)
		require.Nil(t, m.openQuitDialog())

		m.dialog.Update(keyMsg("right")) // toggle to Yes
		action := m.dialog.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		require.Nil(t, action, "documents the current (buggy) behavior: space matches no binding at all")
	})
}

// TestQuitDialog_YesKeyBypassesSelection pins that y/Y quits regardless of
// the toggle state, including when the toggle currently points at "No".
func TestQuitDialog_YesKeyBypassesSelection(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"y", "Y"} {
		key := key
		t.Run("key_"+key, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			ws := NewMockWorkspace(ctrl)
			m := newDialogUI(t, ws)
			require.Nil(t, m.openQuitDialog())

			// Selection defaults to "No"; y/Y must still quit.
			action := m.dialog.Update(keyMsg(key))
			_, isQuit := action.(dialog.ActionQuit)
			require.True(t, isQuit)
		})
	}
}

// TestQuitDialog_NoKeyBypassesSelection pins that n/N closes regardless of
// the toggle state, including when the toggle currently points at "Yes".
func TestQuitDialog_NoKeyBypassesSelection(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"n", "N"} {
		key := key
		t.Run("key_"+key, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			ws := NewMockWorkspace(ctrl)
			m := newDialogUI(t, ws)
			require.Nil(t, m.openQuitDialog())

			m.dialog.Update(keyMsg("left")) // toggle to Yes
			action := m.dialog.Update(keyMsg(key))
			require.Equal(t, dialog.ActionClose{}, action)
		})
	}
}

// TestQuitDialog_CtrlCAlwaysQuits pins that ctrl+c quits even though the
// Quit dialog's own Yes binding also lists ctrl+c: the dedicated Quit
// binding is matched first in HandleMsg's switch, so the Yes case's
// ctrl+c entry is unreachable dead weight. Documented here rather than
// changed, since removing it is a production-code decision, not a test
// concern.
func TestQuitDialog_CtrlCAlwaysQuits(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openQuitDialog())

	action := m.dialog.Update(ctrlKey('c'))
	_, isQuit := action.(dialog.ActionQuit)
	require.True(t, isQuit)
}

func TestQuitDialog_EscAlwaysClosesRegardlessOfToggle(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newDialogUI(t, ws)
	require.Nil(t, m.openQuitDialog())

	m.dialog.Update(keyMsg("left")) // toggle to Yes
	action := m.dialog.Update(keyMsg("esc"))
	require.Equal(t, dialog.ActionClose{}, action)
}

// ---------------------------------------------------------------------
// small local helpers (kept private to this file; avoid colliding with
// helpers of the same shape elsewhere in the package)
// ---------------------------------------------------------------------

func stringsRepeat(s string, n int) string {
	b := make([]byte, 0, len(s)*n)
	for range n {
		b = append(b, s...)
	}
	return string(b)
}

func truncateForName(s string) string {
	const max = 12
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "..."
	}
	if s == "" {
		return "empty"
	}
	return s
}
