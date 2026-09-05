package model

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// mockInlineEditor combines three gomock-generated doubles into one
// concrete value so it satisfies every interface the UI type-asserts an
// activeInline component against: MouseClickableEditor (which embeds
// InlineEditor), PasteableEditor, and common.WheelScrollable. mockgen
// generates one mock per interface; embedding all three lets Update's
// activeInline routing branches be driven and verified through .EXPECT()
// like every other gomock-backed double in this package, instead of a
// hand-rolled fake with boolean fields a test reads after the fact.
type mockInlineEditor struct {
	*MockMouseClickableEditor
	*MockPasteableEditor
	*MockWheelScrollable
}

func newMockInlineEditor(ctrl *gomock.Controller) *mockInlineEditor {
	return &mockInlineEditor{
		MockMouseClickableEditor: NewMockMouseClickableEditor(ctrl),
		MockPasteableEditor:      NewMockPasteableEditor(ctrl),
		MockWheelScrollable:      NewMockWheelScrollable(ctrl),
	}
}

var (
	_ dialog.InlineEditor         = (*mockInlineEditor)(nil)
	_ dialog.MouseClickableEditor = (*mockInlineEditor)(nil)
	_ dialog.PasteableEditor      = (*mockInlineEditor)(nil)
	_ common.WheelScrollable      = (*mockInlineEditor)(nil)
)

func TestUpdate_EnvMsg_DetectsWindowsTerminal(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	require.False(t, m.sendProgressBar)

	m.Update(tea.EnvMsg{"WT_SESSION"})

	require.True(t, m.sendProgressBar)
}

func TestUpdate_ModeReportMsg_UpdatesNotificationBackend(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.notifyBackend = nil

	m.Update(tea.ModeReportMsg{})

	require.NotNil(t, m.notifyBackend)
}

func TestUpdate_UnknownOscEvent_UpdatesNotificationBackend(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.notifyBackend = nil

	m.Update(uv.UnknownOscEvent(""))

	require.NotNil(t, m.notifyBackend)
}

func TestUpdate_FocusAndBlurMsg(t *testing.T) {
	t.Parallel()

	t.Run("focus marks the window focused", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.notifyWindowFocused = false

		m.Update(tea.FocusMsg{})

		require.True(t, m.notifyWindowFocused)
	})

	t.Run("blur clears window focus", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.notifyWindowFocused = true

		m.Update(tea.BlurMsg{})

		require.False(t, m.notifyWindowFocused)
	})
}

func TestUpdate_SessionMessagesMsg(t *testing.T) {
	t.Parallel()

	t.Run("matching session applies the transcript", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}

		m.Update(sessionMessagesMsg{sessionID: "s1", lastUserMessageTime: 42})

		require.EqualValues(t, 42, m.lastUserMessageTime)
	})

	t.Run("stale session is dropped", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}
		m.lastUserMessageTime = 7

		m.Update(sessionMessagesMsg{sessionID: "some-other-session", lastUserMessageTime: 99})

		require.EqualValues(t, 7, m.lastUserMessageTime,
			"a load for a session the user navigated away from must not overwrite state")
	})
}

func TestUpdate_TransparentToggledMsg(t *testing.T) {
	// Not t.Parallel(): the "enabled" subtest pins package-level TTL
	// globals (pinTTLs), which would race against unrelated parallel
	// tests reading them through Update's TTL backstop.

	t.Run("enabled", func(t *testing.T) {
		pinTTLs(t)
		m, _ := newMockBusyUI(t)
		warmCaches(m, false)

		_, cmd := m.Update(transparentToggledMsg{on: true})

		require.True(t, m.isTransparent)
		found := false
		for _, msg := range runCmds(m, cmd) {
			if info, ok := msg.(util.InfoMsg); ok && info.Msg == "Transparent background enabled" {
				found = true
			}
		}
		require.True(t, found, "expected an info message reporting the enabled status")
	})

	t.Run("disabled", func(t *testing.T) {
		m, _ := newMockBusyUI(t)
		m.isTransparent = true

		m.Update(transparentToggledMsg{on: false})

		require.False(t, m.isTransparent)
	})
}

func TestUpdate_AgentRunSubmittedMsg_DispatchesRefreshes(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	_, cmd := m.Update(agentRunSubmittedMsg{})

	require.NotNil(t, cmd)
}

func TestUpdate_LoadSessionMsg(t *testing.T) {
	t.Parallel()

	t.Run("applies the session and switches to chat", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		child := &session.Session{ID: "child1"}

		m.Update(loadSessionMsg{session: child})

		require.Same(t, child, m.session)
		require.Equal(t, uiChat, m.state)
	})

	t.Run("enterFrame pushes the session stack", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		frame := &sessionStackFrame{id: "parent1", title: "Parent"}

		m.Update(loadSessionMsg{session: &session.Session{ID: "child1"}, enterFrame: frame})

		require.Len(t, m.sessionStack, 1)
		require.Equal(t, "parent1", m.sessionStack[0].id)
	})

	t.Run("leaveLevel pops the session stack", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.sessionStack = []sessionStackFrame{{id: "parent1"}}

		m.Update(loadSessionMsg{session: &session.Session{ID: "parent1"}, leaveLevel: true})

		require.Empty(t, m.sessionStack)
	})

	t.Run("clearStack empties the session stack", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.sessionStack = []sessionStackFrame{{id: "a"}, {id: "b"}}

		m.Update(loadSessionMsg{session: &session.Session{ID: "top"}, clearStack: true})

		require.Nil(t, m.sessionStack)
	})

	t.Run("a branch keeps the editor focused", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.focus = uiFocusEditor

		m.Update(loadSessionMsg{
			session:  &session.Session{ID: "branch1", ParentSessionID: "parent1"},
			isBranch: true,
		})

		require.Equal(t, uiFocusEditor, m.focus)
	})

	t.Run("a non-branch child session moves focus to the chat", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.focus = uiFocusEditor

		m.Update(loadSessionMsg{
			session:  &session.Session{ID: "child1", ParentSessionID: "parent1"},
			isBranch: false,
		})

		require.Equal(t, uiFocusMain, m.focus)
	})

	t.Run("forceCompactMode enables compact mode", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.forceCompactMode = true

		m.Update(loadSessionMsg{session: &session.Session{ID: "s1"}})

		require.True(t, m.isCompact)
	})

	t.Run("a pending bang command is started and cleared", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.pendingBangCommand = "ls"

		m.Update(loadSessionMsg{session: &session.Session{ID: "s1"}})

		require.Empty(t, m.pendingBangCommand)
	})
}

func TestUpdate_SessionFilesUpdatesMsg_StartsLSPs(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	files := []SessionFile{{LatestVersion: history.File{Path: "main.go"}}}

	_, cmd := m.Update(sessionFilesUpdatesMsg{sessionFiles: files})

	require.Equal(t, files, m.sessionFiles)
	require.NotNil(t, cmd)
}

func TestUpdate_SendMessageMsg_RoutesToSendMessage(t *testing.T) {
	pinTTLs(t)

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().AgentReadyErr().Return(errors.New("agent not ready"))
	m := newBusyUIWithWorkspace(ws)
	warmCaches(m, false)

	_, cmd := m.Update(sendMessageMsg{Content: "hello"})

	require.NotNil(t, cmd)
	found := false
	for _, msg := range runCmds(m, cmd) {
		if info, ok := msg.(util.InfoMsg); ok && info.Type == util.InfoTypeError {
			found = true
		}
	}
	require.True(t, found, "an unready agent must report an error, not silently drop the send")
}

func TestUpdate_UserCommandsLoadedMsg(t *testing.T) {
	t.Parallel()

	t.Run("without a Commands dialog it just stores the commands", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		m.Update(userCommandsLoadedMsg{Commands: []commands.CustomCommand{{}}})

		require.Len(t, m.customCommands, 1)
	})

	t.Run("with a Commands dialog open it forwards the commands", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(&config.Config{}).AnyTimes()
		ws.EXPECT().WorkingDir().Return("").AnyTimes()
		ws.EXPECT().IsInSandbox().Return(false).AnyTimes()
		m := newBusyUIWithWorkspace(ws)
		m.openCommandsDialog()
		require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))

		m.Update(userCommandsLoadedMsg{Commands: []commands.CustomCommand{{}}})

		require.Len(t, m.customCommands, 1)
		_, ok := m.dialog.Dialog(dialog.CommandsID).(*dialog.Commands)
		require.True(t, ok)
	})
}

func TestUpdate_MCPStateChangedMsg_NoPendingAuthIsNoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().MCPPendingAuth().Return(nil)
	m := newBusyUIWithWorkspace(ws)

	m.Update(mcpStateChangedMsg{states: map[string]mcp.ClientInfo{}})

	require.False(t, m.dialog.ContainsDialog(dialog.MCPAuthID))
}

func TestUpdate_MCPPromptsLoadedMsg(t *testing.T) {
	t.Parallel()

	t.Run("without a Commands dialog it just stores the prompts", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		m.Update(mcpPromptsLoadedMsg{Prompts: []commands.MCPPrompt{{}}})

		require.Len(t, m.mcpPrompts, 1)
	})

	t.Run("with a Commands dialog open it forwards the prompts", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		ws := NewMockWorkspace(ctrl)
		ws.EXPECT().Config().Return(&config.Config{}).AnyTimes()
		ws.EXPECT().WorkingDir().Return("").AnyTimes()
		ws.EXPECT().IsInSandbox().Return(false).AnyTimes()
		m := newBusyUIWithWorkspace(ws)
		m.openCommandsDialog()

		m.Update(mcpPromptsLoadedMsg{Prompts: []commands.MCPPrompt{{}}})

		require.Len(t, m.mcpPrompts, 1)
	})
}

func TestUpdate_PromptHistoryLoadedMsg_SetsFields(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.promptHistory.index = 3
	m.promptHistory.draft = "unsent"

	m.Update(promptHistoryLoadedMsg{messages: []string{"one", "two"}})

	require.Equal(t, []string{"one", "two"}, m.promptHistory.messages)
	require.Equal(t, -1, m.promptHistory.index)
	require.Empty(t, m.promptHistory.draft)
}

func TestUpdate_CloseDialogMsg_ClosesFrontDialog(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

	m.Update(closeDialogMsg{})

	require.False(t, m.dialog.HasDialogs())
}

func TestUpdate_SessionEvent(t *testing.T) {
	t.Parallel()

	t.Run("deleting the current session returns to landing", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}

		m.Update(pubsub.Event[session.Session]{Type: pubsub.DeletedEvent, Payload: session.Session{ID: "s1"}})

		require.Nil(t, m.session)
	})

	t.Run("deleting a different session is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}

		m.Update(pubsub.Event[session.Session]{Type: pubsub.DeletedEvent, Payload: session.Session{ID: "other"}})

		require.NotNil(t, m.session)
		require.Equal(t, "s1", m.session.ID)
	})

	t.Run("an update for the current session refreshes it in place", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1", Title: "old"}

		m.Update(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{ID: "s1", Title: "new"}})

		require.Equal(t, "new", m.session.Title)
	})
}

func TestUpdate_MessageEvent(t *testing.T) {
	t.Parallel()

	t.Run("without a current session it is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = nil

		require.NotPanics(t, func() {
			m.Update(pubsub.Event[message.Message]{Payload: message.Message{ID: "m1", SessionID: "s1"}})
		})
	})

	t.Run("a created message for the current session is appended", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}
		before := m.chat.Len()

		m.Update(pubsub.Event[message.Message]{
			Type: pubsub.CreatedEvent,
			Payload: message.Message{
				ID:        "m1",
				Role:      message.User,
				SessionID: "s1",
				CreatedAt: 100,
			},
		})

		require.Greater(t, m.chat.Len(), before)
	})

	t.Run("a deleted message for the current session is removed", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}
		m.Update(pubsub.Event[message.Message]{
			Type: pubsub.CreatedEvent,
			Payload: message.Message{
				ID:        "m1",
				Role:      message.User,
				SessionID: "s1",
			},
		})
		require.NotNil(t, m.chat.MessageItem("m1"))

		m.Update(pubsub.Event[message.Message]{
			Type:    pubsub.DeletedEvent,
			Payload: message.Message{ID: "m1", SessionID: "s1"},
		})

		require.Nil(t, m.chat.MessageItem("m1"))
	})

	t.Run("a message for a different session routes to the child-session handler", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = &session.Session{ID: "s1"}

		require.NotPanics(t, func() {
			m.Update(pubsub.Event[message.Message]{
				Type:    pubsub.CreatedEvent,
				Payload: message.Message{ID: "m1", SessionID: "child-session"},
			})
		})
	})
}

func TestUpdate_HistoryFileEvent_NoSessionIsNoOp(t *testing.T) {
	pinTTLs(t)

	m, _ := newMockBusyUI(t)
	warmCaches(m, false)
	m.session = nil

	_, cmd := m.Update(pubsub.Event[history.File]{Payload: history.File{SessionID: "s1"}})

	require.Nil(t, cmd)
}

func TestUpdate_AppLSPEvent_DispatchesRefresh(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	_, cmd := m.Update(pubsub.Event[app.LSPEvent]{Payload: app.LSPEvent{Type: app.LSPEventStateChanged}})

	require.NotNil(t, cmd)
}

func TestUpdate_SkillsEvent_SetsStates(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	states := []*skills.SkillState{{Name: "jq"}}

	m.Update(pubsub.Event[skills.Event]{Payload: skills.Event{States: states}})

	require.Equal(t, states, m.skillStates)
}

func TestUpdate_MCPEvent(t *testing.T) {
	t.Parallel()

	t.Run("state changed dispatches a refresh batch", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		_, cmd := m.Update(pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventStateChanged}})

		require.NotNil(t, cmd)
	})

	t.Run("prompts list changed dispatches a refresh", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		_, cmd := m.Update(pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventPromptsListChanged, Name: "srv"}})

		require.NotNil(t, cmd)
	})

	t.Run("tools list changed dispatches a refresh", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		_, cmd := m.Update(pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventToolsListChanged, Name: "srv"}})

		require.NotNil(t, cmd)
	})

	t.Run("resources list changed dispatches a refresh", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		_, cmd := m.Update(pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventResourcesListChanged, Name: "srv"}})

		require.NotNil(t, cmd)
	})
}

func TestUpdate_PermissionRequestEvent_OpensDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	m := newBusyUIWithWorkspace(ws)

	m.Update(pubsub.Event[permission.PermissionRequest]{
		Payload: permission.PermissionRequest{ID: "p1", ToolCallID: "t1", ToolName: "bash"},
	})

	require.True(t, m.dialog.ContainsDialog(dialog.PermissionsID))
}

func TestUpdate_PermissionNotificationEvent_ClosesMatchingDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	m := newBusyUIWithWorkspace(ws)
	perm := permission.PermissionRequest{ID: "p1", ToolCallID: "t1", ToolName: "bash"}
	m.dialog.OpenDialogWithGrace(dialog.NewPermissions(m.com, perm))
	require.True(t, m.dialog.ContainsDialog(dialog.PermissionsID))

	m.Update(pubsub.Event[permission.PermissionNotification]{
		Payload: permission.PermissionNotification{ToolCallID: "t1", Granted: true},
	})

	require.False(t, m.dialog.ContainsDialog(dialog.PermissionsID))
}

func TestUpdate_QuestionRequestEvent_OpensBatchForm(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return((*config.Config)(nil)).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	m := newBusyUIWithWorkspace(ws)

	m.Update(pubsub.Event[question.Request]{
		Payload: question.Request{
			ID:        "b1",
			Questions: []question.Question{{ID: "q1", Type: question.TypeFreeText, Text: "Continue?"}},
		},
	})

	require.NotNil(t, m.activeInline)
}

func TestUpdate_QuestionNotificationEvent_DismissesForm(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	form := newMockInlineEditor(gomock.NewController(t))
	m.activeInline = form

	require.NotPanics(t, func() {
		m.Update(pubsub.Event[question.Notification]{Payload: question.Notification{BatchID: "b1"}})
	})
	// handleQuestionNotification only dismisses *dialog.QuestionForm, not
	// an arbitrary InlineEditor, so the mock is left in place untouched
	// (no .EXPECT() calls were set, so any call on it would fail the
	// test); this exercises the type-assertion-fails branch.
	require.Same(t, form, m.activeInline)
}

func TestUpdate_TerminalVersionMsg_DetectsKnownTerminal(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	require.False(t, m.sendProgressBar)

	m.Update(tea.TerminalVersionMsg{Name: "ghostty"})

	require.True(t, m.sendProgressBar)
}

func TestUpdate_WindowSizeMsg_UpdatesDimensionsAndLayout(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	require.Equal(t, 100, m.width)
	require.Equal(t, 40, m.height)
	require.NotZero(t, m.layout.main.Dx())
}

func TestUpdate_KeyboardEnhancementsMsg_StoresCapabilities(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	m.Update(tea.KeyboardEnhancementsMsg{})

	require.Equal(t, tea.KeyboardEnhancementsMsg{}, m.keyenh)
}

func TestUpdate_CopyChatHighlightMsg_Dispatches(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	_, cmd := m.Update(copyChatHighlightMsg{})

	require.NotNil(t, cmd)
}

func TestUpdate_DelayedClickMsg_Dispatches(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	require.NotPanics(t, func() {
		m.Update(DelayedClickMsg{ClickID: 1, ItemIdx: 0})
	})
}

// permissionClickDialog stands in for the real Permissions dialog: like
// idOnlyDialog, it reports PermissionsID so handleDialogMsg's
// dialog.ActionPermissionResponse branch has something to close, but
// unlike idOnlyDialog it only reacts to a mouse click — exactly like a
// rendered "Allow" button would — rather than echoing every message
// back. This isolates the routing bug from real button geometry: the
// dialog layer that turns a click into an action is already covered by
// dialog.TestPermissions_MouseClickSelectsButton.
type permissionClickDialog struct {
	dialog.Dialog
	perm permission.PermissionRequest
}

func (permissionClickDialog) ID() string { return dialog.PermissionsID }

func (d permissionClickDialog) HandleMsg(msg tea.Msg) dialog.Action {
	if _, ok := msg.(tea.MouseClickMsg); ok {
		return dialog.ActionPermissionResponse{Permission: d.perm, Action: dialog.PermissionAllow}
	}
	return nil
}

func TestUpdate_MouseClickMsg(t *testing.T) {
	t.Parallel()

	t.Run("routes to an open dialog first", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

		require.NotPanics(t, func() {
			m.Update(tea.MouseClickMsg{X: 5, Y: 5})
		})
	})

	// Regression test: a click used to reach dialog.HandleMsg (which
	// resolved to the clicked button, per
	// dialog.TestPermissions_MouseClickSelectsButton) but Update then
	// discarded the returned Action instead of routing it through
	// handleDialogMsg like every other message type does, so approving
	// or denying a permission request with the mouse silently did
	// nothing. Compare TestCompactPaletteMouseClickNeverReachesWorkspace
	// in compact_pipeline_test.go, which documents a still-open, unrelated
	// gap in the Commands dialog.
	t.Run("an allow response reaches the workspace and closes the dialog", func(t *testing.T) {
		t.Parallel()
		m, ws := newMockBusyUI(t)
		perm := permission.PermissionRequest{ID: "p1", ToolCallID: "t1", ToolName: "bash"}
		ws.EXPECT().PermissionGrant(perm).Return(true)
		m.dialog = dialog.NewOverlay(permissionClickDialog{perm: perm})

		m.Update(tea.MouseClickMsg{X: 5, Y: 5, Button: uv.MouseLeft})

		require.False(t, m.dialog.ContainsDialog(dialog.PermissionsID),
			"clicking Allow with the mouse should close the permissions dialog")
	})

	t.Run("a completed click clears the active inline editor", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		editor := newMockInlineEditor(gomock.NewController(t))
		editor.MockMouseClickableEditor.EXPECT().HandleMouseClick(gomock.Any(), gomock.Any()).Return(true, true)
		m.activeInline = editor
		m.focus = uiFocusEditor

		m.Update(tea.MouseClickMsg{X: 5, Y: 5})

		require.Nil(t, m.activeInline)
	})

	t.Run("without a dialog or active inline it routes to the chat", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay()
		m.updateLayoutAndSize()

		require.NotPanics(t, func() {
			m.Update(tea.MouseClickMsg{X: m.layout.main.Min.X, Y: m.layout.main.Min.Y})
		})
	})
}

func TestUpdate_MouseMotionMsg(t *testing.T) {
	t.Parallel()

	t.Run("routes to an open dialog first", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

		require.NotPanics(t, func() {
			m.Update(tea.MouseMotionMsg{X: 5, Y: 5})
		})
	})

	t.Run("tracks hover for the active inline editor", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay()
		editor := newMockInlineEditor(gomock.NewController(t))
		editor.MockMouseClickableEditor.EXPECT().SetHover(3, 4)
		m.activeInline = editor

		m.Update(tea.MouseMotionMsg{X: 3, Y: 4})
	})

	t.Run("edge motion scrolls the chat", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay()
		m.updateLayoutAndSize()

		require.NotPanics(t, func() {
			m.Update(tea.MouseMotionMsg{X: m.layout.main.Min.X, Y: 0})
		})
	})
}

func TestUpdate_MouseReleaseMsg(t *testing.T) {
	t.Parallel()

	t.Run("routes to an open dialog first", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

		require.NotPanics(t, func() {
			m.Update(tea.MouseReleaseMsg{X: 5, Y: 5})
		})
	})

	t.Run("releases in the chat", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay()
		m.updateLayoutAndSize()

		require.NotPanics(t, func() {
			m.Update(tea.MouseReleaseMsg{X: m.layout.main.Min.X, Y: m.layout.main.Min.Y})
		})
	})
}

func TestUpdate_CoalescedWheelMsg(t *testing.T) {
	t.Parallel()

	t.Run("routes to the active inline editor when the mouse is over the editor", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.updateLayoutAndSize()
		editor := newMockInlineEditor(gomock.NewController(t))
		editor.MockWheelScrollable.EXPECT().HandleWheel(gomock.Any(), gomock.Any())
		m.activeInline = editor

		m.Update(common.CoalescedWheelMsg{
			Mouse:  tea.Mouse{X: m.layout.editor.Min.X, Y: m.layout.editor.Min.Y},
			DeltaY: 1,
		})
	})

	t.Run("routes to an open dialog when there is no active inline editor", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.updateLayoutAndSize()
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

		require.NotPanics(t, func() {
			m.Update(common.CoalescedWheelMsg{Mouse: tea.Mouse{X: -100, Y: -100}, DeltaY: 1})
		})
	})

	t.Run("scrolls the chat otherwise", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.updateLayoutAndSize()
		m.dialog = dialog.NewOverlay()

		require.NotPanics(t, func() {
			m.Update(common.CoalescedWheelMsg{Mouse: tea.Mouse{X: -100, Y: -100}, DeltaX: 1, DeltaY: 1})
		})
	})
}

func TestUpdate_AnimStepMsg_AnimatesChat(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	require.NotPanics(t, func() {
		m.Update(anim.StepMsg{ID: "spinner", Gen: 1})
	})
}

func TestUpdate_ScrollbarHideMsg_HidesScrollbar(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	require.NotPanics(t, func() {
		m.Update(scrollbarHideMsg{seq: 1})
	})
}

func TestUpdate_ChatWarmMsg_WarmsStep(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	require.NotPanics(t, func() {
		m.Update(chatWarmMsg{seq: 1})
	})
}

func TestUpdate_SpinnerTickMsg(t *testing.T) {
	t.Parallel()

	t.Run("routes to an open dialog", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

		require.NotPanics(t, func() {
			m.Update(spinner.TickMsg{})
		})
	})

	t.Run("updates the turn spinner while spinning", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay()
		m.turnIsSpinning = true

		require.NotPanics(t, func() {
			m.Update(spinner.TickMsg{})
		})
	})
}

func TestUpdate_KeyPressMsg_Dispatches(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.dialog = dialog.NewOverlay()

	require.NotPanics(t, func() {
		m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	})
}

func TestUpdate_PasteMsg(t *testing.T) {
	t.Parallel()

	t.Run("routes to the active inline editor when focused", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		editor := newMockInlineEditor(gomock.NewController(t))
		editor.MockPasteableEditor.EXPECT().HandlePaste(tea.PasteMsg{Content: "pasted"}).Return(nil)
		m.activeInline = editor
		m.focus = uiFocusEditor

		m.Update(tea.PasteMsg{Content: "pasted"})
	})

	t.Run("without an active inline editor it routes to handlePasteMsg", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay()
		m.focus = uiFocusEditor

		require.NotPanics(t, func() {
			m.Update(tea.PasteMsg{Content: "hello world"})
		})
	})
}

// TestHandlePasteMsg_EmptyContentFallsBackToClipboardImage covers the
// common real-world path for image paste: most terminals intercept the
// paste gesture themselves and deliver it as a bracketed-paste PasteMsg
// rather than forwarding the raw PasteImage key chord, so an image-only
// clipboard (no text form to paste) arrives here as empty content.
func TestHandlePasteMsg_EmptyContentFallsBackToClipboardImage(t *testing.T) {
	t.Parallel()

	t.Run("unsupported model is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.focus = uiFocusEditor
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{SupportsImages: false}}}

		require.NotPanics(t, func() {
			m.handlePasteMsg(tea.PasteMsg{Content: ""})
		})
	})

	t.Run("supported model queues a clipboard image read", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.focus = uiFocusEditor
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{SupportsImages: true}}}

		cmd := m.handlePasteMsg(tea.PasteMsg{Content: "   "})

		require.NotNil(t, cmd, "whitespace-only paste content falls back to a clipboard image read")
	})

	t.Run("non-empty content is not treated as an image fallback", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.focus = uiFocusEditor
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{SupportsImages: true}}}

		require.NotPanics(t, func() {
			m.handlePasteMsg(tea.PasteMsg{Content: "hello"})
		})
	})
}

func TestUpdate_OpenEditorMsg_SetsTextareaValue(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	m.Update(openEditorMsg{Text: "edited content"})

	require.Equal(t, "edited content", m.textarea.Value())
}

func TestUpdate_ShellStreamMsg(t *testing.T) {
	t.Parallel()

	t.Run("appends output to an existing pending item", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		item := chat.NewPendingShellItem(m.com.Styles, "ls")
		m.chat.AppendMessages(item)

		m.Update(shellStreamMsg{PendingID: item.ID(), Chunk: "output chunk"})

		require.NotPanics(t, func() {})
	})

	t.Run("continues draining the stream channel", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		ch := make(chan string, 1)
		ch <- "next"

		_, cmd := m.Update(shellStreamMsg{PendingID: "missing", Chunk: "chunk", streamCh: ch})

		require.NotNil(t, cmd)
	})
}

func TestUpdate_ShellResultMsg(t *testing.T) {
	t.Parallel()

	t.Run("completes an existing pending item", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		item := chat.NewPendingShellItem(m.com.Styles, "ls")
		m.chat.AppendMessages(item)

		m.Update(shellResultMsg{PendingID: item.ID(), Command: "ls", Output: "done", ExitCode: 0})

		require.NotPanics(t, func() {})
	})

	t.Run("creates a new item when nothing was pending", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		before := m.chat.Len()

		m.Update(shellResultMsg{Command: "ls", Output: "done", ExitCode: 0})

		require.Greater(t, m.chat.Len(), before)
	})
}

func TestUpdate_ModelsCatalogMsg_UpdatesOpenDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	m := newBusyUIWithWorkspace(ws)
	m.openModelsDialog()
	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))

	require.NotPanics(t, func() {
		m.Update(dialog.ModelsCatalogMsg{Providers: []catwalk.Provider{}})
	})
}

func TestUpdate_ProvidersCatalogMsg_UpdatesOpenDialog(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	m := newBusyUIWithWorkspace(ws)
	m.dialog = dialog.NewOverlay(dialog.NewProviders(m.com, false))
	require.True(t, m.dialog.ContainsDialog(dialog.ProvidersID))

	require.NotPanics(t, func() {
		m.Update(dialog.ProvidersCatalogMsg{Providers: []catwalk.Provider{}})
	})
}

func TestUpdate_InfoMsg_SetsStatusAndSchedulesClear(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	_, cmd := m.Update(util.InfoMsg{Type: util.InfoTypeSuccess, Msg: "done"})

	require.NotNil(t, cmd)
}

func TestUpdate_UpdateAvailableMsg_SetsStatus(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	_, cmd := m.Update(app.UpdateAvailableMsg{CurrentVersion: "1.0.0", LatestVersion: "1.1.0"})

	require.NotNil(t, cmd)
}

func TestUpdate_ConnectionEvent(t *testing.T) {
	t.Parallel()

	t.Run("degraded reports a warning", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = nil

		_, cmd := m.Update(workspace.ConnectionEvent{State: workspace.ConnectionDegraded})

		require.NotNil(t, cmd)
	})

	t.Run("degraded and stuck reports an error", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = nil

		m.Update(workspace.ConnectionEvent{State: workspace.ConnectionDegraded, Stuck: true})

		require.Equal(t, util.InfoTypeError, m.status.msg.Type)
	})

	t.Run("recovered without a session just clears the warning", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = nil

		m.Update(workspace.ConnectionEvent{State: workspace.ConnectionRecovered})

		require.Equal(t, util.InfoTypeSuccess, m.status.msg.Type)
	})
}

func TestUpdate_ClearStatusMsg_ClearsStatus(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.status.SetInfoMsg(util.InfoMsg{Type: util.InfoTypeSuccess, Msg: "hi"})

	m.Update(util.ClearStatusMsg{})

	require.Empty(t, m.status.msg.Msg)
}

func TestUpdate_CompletionItemsLoadedMsg(t *testing.T) {
	t.Parallel()

	t.Run("ignored while completions are closed", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.completionsOpen = false

		require.NotPanics(t, func() {
			m.Update(completions.CompletionItemsLoadedMsg{})
		})
	})

	t.Run("applied while completions are open", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.completionsOpen = true

		require.NotPanics(t, func() {
			m.Update(completions.CompletionItemsLoadedMsg{})
		})
	})
}

func TestUpdate_KittyGraphicsEvent_WarnsOnNonOKPayload(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	require.NotPanics(t, func() {
		m.Update(uv.KittyGraphicsEvent{Payload: []byte("ERROR")})
	})
}

func TestUpdate_MCPAuthStarted_DispatchesAuthentication(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	_, cmd := m.Update(dialog.ActionMCPAuthStarted{Name: "srv"})

	require.NotNil(t, cmd)
}

func TestUpdate_MCPAuthCompleteErrored_RoutesToDialogWhenOpen(t *testing.T) {
	t.Parallel()

	t.Run("complete", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.MCPAuthID})

		require.NotPanics(t, func() {
			m.Update(dialog.ActionMCPAuthComplete{Name: "srv"})
		})
	})

	t.Run("errored", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.MCPAuthID})

		require.NotPanics(t, func() {
			m.Update(dialog.ActionMCPAuthErrored{Name: "srv", Error: errors.New("boom")})
		})
	})
}

func TestUpdate_DefaultCase_RoutesUnknownMsgToDialog(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})

	type unknownMsg struct{}

	require.NotPanics(t, func() {
		m.Update(unknownMsg{})
	})
}
