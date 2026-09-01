package model

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// commandsPassThroughDialog stands in for the commands dialog: like
// passThroughDialog (subsession_test.go), it hands the action straight
// back, but reports CommandsID so the "close the commands dialog" side
// effect most handleDialogMsg branches perform has a real dialog to close.
type commandsPassThroughDialog struct{ dialog.Dialog }

func (commandsPassThroughDialog) ID() string { return dialog.CommandsID }

func (commandsPassThroughDialog) HandleMsg(msg tea.Msg) dialog.Action { return msg }

// newHandleDialogUI builds a full UI (chat/textarea/status all wired via
// newTestUI) backed by a mock workspace with the commands dialog open, so
// handleDialogMsg's switch can be driven directly with a chosen action
// while the surrounding UI machinery behaves like production code.
func newHandleDialogUI(t *testing.T, ws *MockWorkspace) *UI {
	t.Helper()

	m := newTestUI()
	m.com = &common.Common{Workspace: ws, Styles: m.com.Styles}
	m.dialog = dialog.NewOverlay(commandsPassThroughDialog{})
	return m
}

func TestHandleDialogMsg_NoDialogsIsNoOp(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay()

	cmd := m.handleDialogMsg(dialog.ActionToggleHelp{})
	if cmd != nil {
		require.Nil(t, cmd())
	}
}

func TestHandleDialogMsg_UnknownActionPassesThrough(t *testing.T) {
	t.Parallel()

	type customMsg struct{ n int }

	m := newTestUI()
	m.dialog = dialog.NewOverlay(passThroughDialog{})

	cmd := m.handleDialogMsg(customMsg{n: 7})
	require.NotNil(t, cmd)
	require.Equal(t, customMsg{n: 7}, cmd())
}

func TestHandleDialogMsg_ActionClose(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.focus = uiFocusEditor
	m.textarea.Blur()

	cmd := m.handleDialogMsg(dialog.ActionClose{})
	require.False(t, m.dialog.HasDialogs(), "the front dialog must close")
	// The textarea has no virtual cursor in tests, so Focus() may return a
	// nil cmd; what matters is that closing back to the editor does not
	// panic and does not leave the palette open.
	if cmd != nil {
		cmd()
	}
}

func TestHandleDialogMsg_ActionCmd(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	ran := false
	cmd := m.handleDialogMsg(dialog.ActionCmd{Cmd: func() tea.Msg { ran = true; return nil }})
	require.NotNil(t, cmd)
	cmd()
	require.True(t, ran)
}

func TestHandleDialogMsg_ActionCmd_NilIsHarmless(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionCmd{Cmd: nil})
	if cmd != nil {
		require.Nil(t, cmd())
	}
}

func TestHandleDialogMsg_ActionOpenDialog(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	m.handleDialogMsg(dialog.ActionOpenDialog{DialogID: dialog.QuitID})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette must close before the target dialog opens")
	require.True(t, m.dialog.ContainsDialog(dialog.QuitID))
}

func TestHandleDialogMsg_ActionCyclePermissionMode(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().PermissionMode().Return(permission.ModeManual)
	ws.EXPECT().PermissionSetMode(permission.ModeAutoAcceptEdits)
	m := newHandleDialogUI(t, ws)

	m.handleDialogMsg(dialog.ActionCyclePermissionMode{})
	require.False(t, m.dialog.HasDialogs())
	require.Equal(t, permission.ModeAutoAcceptEdits, m.permissionModeCached())
}

func TestHandleDialogMsg_ActionSelectNotificationStyle(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	cfg := &config.Config{Options: &config.Options{}}
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().SetConfigField(config.ScopeGlobal, "options.notifications", "desktop").Return(nil)
	m := newHandleDialogUI(t, ws)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.NotificationsID})

	cmd := m.handleDialogMsg(dialog.ActionSelectNotificationStyle{Style: "desktop"})
	require.NotNil(t, cmd)
	require.Equal(t, "desktop", cfg.Options.Notifications)
	require.False(t, m.dialog.ContainsDialog(dialog.NotificationsID))
}

func TestHandleDialogMsg_ActionSelectNotificationStyle_ReportsSetError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	cfg := &config.Config{Options: &config.Options{}}
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().SetConfigField(config.ScopeGlobal, "options.notifications", "desktop").
		Return(errors.New("boom"))
	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionSelectNotificationStyle{Style: "desktop"})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeError, msg.Type)
}

func TestHandleDialogMsg_ActionNewSession_BusyIsRefusedAndDialogStays(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newHandleDialogUI(t, ws)
	m.agentBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionNewSession{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID),
		"a refused new-session request must not close the palette")
}

func TestHandleDialogMsg_ActionNewSession_WithoutSessionIsNoOpButClosesPalette(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	m := newHandleDialogUI(t, ws)
	m.agentBusyCache.set(false)

	m.handleDialogMsg(dialog.ActionNewSession{})
	require.False(t, m.dialog.HasDialogs(), "the palette must close even when there was no session to clear")
}

func TestHandleDialogMsg_ActionAbortBranch(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, true)
	ui.dialog = dialog.NewOverlay(commandsPassThroughDialog{})

	drain(ui.handleDialogMsg(dialog.ActionAbortBranch{SessionID: "branch-1"}))
	require.Equal(t, []string{"branch-1"}, ws.abandoned)
	require.False(t, ui.dialog.ContainsDialog(dialog.CommandsID))
}

// TestHandleDialogMsg_ActionGoToParent pins /parent as the non-destructive
// counterpart to /abort: it must reach the branch's parent without ending
// the branch — no AgentCancel, no AgentAbandonBranch.
func TestHandleDialogMsg_ActionGoToParent(t *testing.T) {
	t.Parallel()

	ui, ws := newBranchCancelUI(t, false)
	ui.dialog = dialog.NewOverlay(commandsPassThroughDialog{})

	drain(ui.handleDialogMsg(dialog.ActionGoToParent{}))
	require.Contains(t, ws.loaded, "parent-1", "go-to-parent must navigate to the session's parent")
	require.Empty(t, ws.abandoned, "go-to-parent must not abandon the branch")
	require.Empty(t, ws.cancelled, "go-to-parent must not cancel the branch's turn")
	require.False(t, ui.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionToggleHelp(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	require.False(t, m.status.ShowingAll())

	m.handleDialogMsg(dialog.ActionToggleHelp{})
	require.True(t, m.status.ShowingAll())
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionExternalEditor_BusyIsRefusedAndDialogStays(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.agentBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionExternalEditor{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionToggleCompactMode(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().SetCompactMode(config.ScopeGlobal, gomock.Any()).Return(nil)
	m := newHandleDialogUI(t, ws)
	before := m.forceCompactMode

	m.handleDialogMsg(dialog.ActionToggleCompactMode{})
	require.Equal(t, !before, m.forceCompactMode)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionToggleDetails(t *testing.T) {
	t.Parallel()

	t.Run("with a session it flips detailsOpen", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		before := m.detailsOpen

		m.handleDialogMsg(dialog.ActionToggleDetails{})
		require.Equal(t, !before, m.detailsOpen)
		require.False(t, m.dialog.HasDialogs())
	})

	t.Run("without a session detailsOpen is untouched but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = nil

		m.handleDialogMsg(dialog.ActionToggleDetails{})
		require.False(t, m.detailsOpen)
		require.False(t, m.dialog.HasDialogs())
	})
}

func TestHandleDialogMsg_ActionSuspend(t *testing.T) {
	t.Parallel()

	t.Run("idle agent suspends and always closes the palette", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.agentBusyCache.set(false)

		cmd := m.handleDialogMsg(dialog.ActionSuspend{})
		require.NotNil(t, cmd)
		require.False(t, m.dialog.HasDialogs())
	})

	t.Run("busy agent is refused but the palette still closes", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.agentBusyCache.set(true)

		cmd := m.handleDialogMsg(dialog.ActionSuspend{})
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeWarn, msg.Type)
		require.False(t, m.dialog.HasDialogs(), "Suspend closes the palette unconditionally, unlike NewSession/ExternalEditor")
	})
}

func TestHandleDialogMsg_ActionToggleThinking(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.session = nil // no session -> toggleThinkingCmd warns, but the palette still closes

	cmd := m.handleDialogMsg(dialog.ActionToggleThinking{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionToggleTransparentBackground(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}).AnyTimes()
	ws.EXPECT().SetConfigField(config.ScopeGlobal, "options.tui.transparent", true).Return(nil)
	m := newHandleDialogUI(t, ws)

	cmd := m.handleDialogMsg(dialog.ActionToggleTransparentBackground{})
	require.NotNil(t, cmd)
	msg := cmd().(transparentToggledMsg)
	require.True(t, msg.on)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionQuit(t *testing.T) {
	t.Parallel()

	m := newTestUI()
	m.dialog = dialog.NewOverlay(passThroughDialog{})

	cmd := m.handleDialogMsg(tea.QuitMsg{})
	require.NotNil(t, cmd)
	_, isQuit := cmd().(tea.QuitMsg)
	require.True(t, isQuit)
}

func TestHandleDialogMsg_ActionEnableDockerMCP(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionEnableDockerMCP{})
	require.NotNil(t, cmd)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionDisableDockerMCP(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	cmd := m.handleDialogMsg(dialog.ActionDisableDockerMCP{})
	require.NotNil(t, cmd)
	require.False(t, m.dialog.HasDialogs())
}

func TestHandleDialogMsg_ActionInitializeProject_BusyIsRefusedAndDialogStays(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.agentBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionInitializeProject{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionSelectModel_BusyIsRefused(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.agentBusyCache.set(true)

	cmd := m.handleDialogMsg(dialog.ActionSelectModel{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeWarn, msg.Type)
}

func TestHandleDialogMsg_ActionSelectModel_MissingConfigReportsError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(nil)
	m := newHandleDialogUI(t, ws)
	m.agentBusyCache.set(false)

	cmd := m.handleDialogMsg(dialog.ActionSelectModel{})
	require.NotNil(t, cmd)
	msg := cmd().(util.InfoMsg)
	require.Equal(t, util.InfoTypeError, msg.Type)
}

func TestHandleDialogMsg_ActionSelectAgent(t *testing.T) {
	t.Parallel()

	t.Run("without a session it warns and still closes the agents dialog", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.AgentsID})

		cmd := m.handleDialogMsg(dialog.ActionSelectAgent{AgentID: "coder"})
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeWarn, msg.Type)
		require.False(t, m.dialog.ContainsDialog(dialog.AgentsID))
	})

	t.Run("selecting the already-active agent is a no-op", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.AgentID = "coder"

		cmd := m.handleDialogMsg(dialog.ActionSelectAgent{AgentID: "coder"})
		require.Nil(t, cmd)
	})

	t.Run("switching agent dispatches a refresh", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.AgentID = "coder"

		cmd := m.handleDialogMsg(dialog.ActionSelectAgent{AgentID: "reviewer"})
		require.NotNil(t, cmd, "switching to a different agent must dispatch a refresh command")
	})
}

func TestHandleDialogMsg_ActionSelectVariant(t *testing.T) {
	t.Parallel()

	t.Run("without a session it warns", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.VariantsID})

		cmd := m.handleDialogMsg(dialog.ActionSelectVariant{Variant: "fast"})
		require.NotNil(t, cmd)
		msg := cmd().(util.InfoMsg)
		require.Equal(t, util.InfoTypeWarn, msg.Type)
		require.False(t, m.dialog.ContainsDialog(dialog.VariantsID))
	})

	t.Run("selecting the already-active variant is a no-op", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.Variant = "fast"

		cmd := m.handleDialogMsg(dialog.ActionSelectVariant{Variant: "fast"})
		require.Nil(t, cmd)
	})

	t.Run("switching variant dispatches a refresh", func(t *testing.T) {
		t.Parallel()

		m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
		m.session = &session.Session{ID: "s1"}
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive.Variant = "fast"

		cmd := m.handleDialogMsg(dialog.ActionSelectVariant{Variant: "careful"})
		require.NotNil(t, cmd)
	})
}

func TestHandleDialogMsg_ActionPermissionResponse(t *testing.T) {
	t.Parallel()

	perm := permission.PermissionRequest{ID: "p1", ToolCallID: "t1", ToolName: "bash"}

	t.Run("allow grants once", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().PermissionGrant(perm).Return(true)
		m := newHandleDialogUI(t, ws)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.PermissionsID})

		m.handleDialogMsg(dialog.ActionPermissionResponse{Permission: perm, Action: dialog.PermissionAllow})
		require.False(t, m.dialog.ContainsDialog(dialog.PermissionsID))
	})

	t.Run("allow for session grants persistently", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().PermissionGrantPersistent(perm).Return(true)
		m := newHandleDialogUI(t, ws)

		m.handleDialogMsg(dialog.ActionPermissionResponse{Permission: perm, Action: dialog.PermissionAllowForSession})
	})

	t.Run("deny refuses the request", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().PermissionDeny(perm).Return(true)
		m := newHandleDialogUI(t, ws)

		m.handleDialogMsg(dialog.ActionPermissionResponse{Permission: perm, Action: dialog.PermissionDeny})
	})
}

func TestHandleDialogMsg_ActionFilePickerSelected_EmptyPathIsHarmless(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.FilePickerID})

	// tea.Sequence's returned cmd yields an unexported msg type that only
	// tea.Program's runtime knows how to execute step by step; a unit
	// test can only confirm handleDialogMsg builds the sequence (picker
	// still open here) rather than that the deferred close step ran.
	cmd := m.handleDialogMsg(dialog.ActionFilePickerSelected{Path: ""})
	require.NotNil(t, cmd)
	require.True(t, m.dialog.ContainsDialog(dialog.FilePickerID))
}

func TestHandleDialogMsg_ActionRunCustomCommand_OpensArgumentsDialogWhenUnfilled(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	action := dialog.ActionRunCustomCommand{
		Content:   "do $THING",
		Arguments: []commands.Argument{{ID: "THING"}},
	}
	m.handleDialogMsg(action)
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleDialogMsg_ActionRunMCPPrompt_OpensArgumentsDialogWhenUnfilled(t *testing.T) {
	t.Parallel()

	m := newHandleDialogUI(t, NewMockWorkspace(gomock.NewController(t)))

	action := dialog.ActionRunMCPPrompt{
		Title:     "Prompt",
		Arguments: []commands.Argument{{ID: "THING"}},
	}
	m.handleDialogMsg(action)
	require.True(t, m.dialog.ContainsDialog(dialog.ArgumentsID))
}
