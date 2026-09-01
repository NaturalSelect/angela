package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newKeyRoutingUI builds a full UI (chat, textarea, attachments,
// completions, keyMap) wired to a MockWorkspace with a rich Config
// (Providers, Agents, TUI options), a resolved active agent offering two
// reasoning-level variants, and a session list — everything every
// global dialog-opening key and cycleVariant need to succeed without
// panicking. Mirrors newDialogUI's config (open_dialog_test.go) on top
// of the full newBusyUIWithWorkspace fixture that newDialogUI omits.
func newKeyRoutingUI(t *testing.T) (*UI, *MockWorkspace) {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{TUI: &config.TUIOptions{}},
		Agents: map[string]config.Agent{
			"coder": {ID: "coder", Name: "Coder", Mode: config.AgentModePrimary},
		},
	}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().ListSessions(gomock.Any()).Return([]session.Session{{ID: "s1"}}, nil).AnyTimes()

	m := newBusyUIWithWorkspace(ws)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = "s1"
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		ModelCfg:   config.SelectedModel{Model: "test-model"},
		CatwalkCfg: catwalk.Model{ReasoningLevels: []string{"low", "high"}},
	}
	return m, ws
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	default:
		// Runes and ctrl/alt combos parse from their string form (e.g.
		// "ctrl+c", "x", "!").
		k := tea.KeyPressMsg{Text: s}
		if len(s) == 1 {
			k.Code = rune(s[0])
		}
		return k
	}
}

// ctrlKey builds a KeyPressMsg matching a "ctrl+X" binding.
func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func TestHandleKeyPressMsg_QuitKeyAlwaysWinsFirst(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	m.handleKeyPressMsg(ctrlKey('c'))

	require.True(t, m.dialog.ContainsDialog(dialog.QuitID))
}

func TestHandleKeyPressMsg_QuitKeyNoOpWhenQuitDialogAlreadyOpen(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.dialog.OpenDialogWithGrace(idOnlyDialog{id: dialog.QuitID})

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(ctrlKey('c'))
	})
	require.True(t, m.dialog.ContainsDialog(dialog.QuitID))
}

func TestHandleKeyPressMsg_RoutesToOpenDialog(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.dialog.OpenDialogWithGrace(idOnlyDialog{id: dialog.ModelsID})

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(keyMsg("x"))
	})
}

func TestHandleKeyPressMsg_TabTogglesActiveInlineFocus(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	editor := newMockInlineEditor(gomock.NewController(t))
	editor.MockMouseClickableEditor.EXPECT().Height(gomock.Any()).Return(3).AnyTimes()
	m.activeInline = editor
	m.focus = uiFocusEditor

	editor.MockMouseClickableEditor.EXPECT().SetFocused(false)
	m.handleKeyPressMsg(keyMsg("tab"))

	require.Equal(t, uiFocusMain, m.focus)

	editor.MockMouseClickableEditor.EXPECT().SetFocused(true)
	m.handleKeyPressMsg(keyMsg("tab"))

	require.Equal(t, uiFocusEditor, m.focus)
}

func TestHandleKeyPressMsg_ActiveInlineHandlesKeys(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusEditor
	editor := newMockInlineEditor(gomock.NewController(t))
	editor.MockMouseClickableEditor.EXPECT().HandleKey(gomock.Any()).Return(false, nil)
	editor.MockMouseClickableEditor.EXPECT().HeightChanged().Return(false)
	m.activeInline = editor

	m.handleKeyPressMsg(keyMsg("x"))

	require.NotNil(t, m.activeInline, "HandleKey returning done=false must keep the inline editor active")
}

func TestHandleKeyPressMsg_ChatCancelWhenEscapeCancelsTrue(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	m.agentReady = true
	m.isCanceling = true
	m.agentBusyCache.set(true)
	m.busyFetchInFlight = true // keeps dispatchBusyRefresh's returned cmd nil
	ws.EXPECT().AgentCancel("s1")

	m.handleKeyPressMsg(keyMsg("esc"))

	require.False(t, m.turnIsSpinning)
}

func TestHandleKeyPressMsg_ChatCancelFallsThroughWhenEscapeCancelsFalse(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	// Default fixture: not busy, not viewing a sub-agent, not a branch,
	// so escapeCancels() is false and esc must not reach AgentCancel (no
	// expectation is set, so gomock fails loudly if it does).

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(keyMsg("esc"))
	})
}

func TestHandleKeyPressMsg_GlobalHelp(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	require.False(t, m.status.ShowingAll())

	m.handleKeyPressMsg(ctrlKey('g'))

	require.True(t, m.status.ShowingAll())
}

func TestHandleKeyPressMsg_GlobalCommands(t *testing.T) {
	t.Parallel()

	m, _ := newKeyRoutingUI(t)

	m.handleKeyPressMsg(ctrlKey('p'))

	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
}

func TestHandleKeyPressMsg_GlobalModels(t *testing.T) {
	t.Parallel()

	m, _ := newKeyRoutingUI(t)

	m.handleKeyPressMsg(ctrlKey('l'))

	require.True(t, m.dialog.ContainsDialog(dialog.ModelsID))
}

func TestHandleKeyPressMsg_GlobalSessions(t *testing.T) {
	t.Parallel()

	m, _ := newKeyRoutingUI(t)

	m.handleKeyPressMsg(ctrlKey('s'))

	require.True(t, m.dialog.ContainsDialog(dialog.SessionsID))
}

func TestHandleKeyPressMsg_GlobalCycleVariant(t *testing.T) {
	t.Parallel()

	t.Run("no active agent warns", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		cmd := m.handleKeyPressMsg(ctrlKey('e'))

		require.NotNil(t, cmd)
	})

	t.Run("a resolved agent with variants cycles", func(t *testing.T) {
		t.Parallel()
		m, _ := newKeyRoutingUI(t)

		// cycleVariant only builds the AgentEditActive closure here; it
		// is not invoked unless the returned cmd tree is drained, so no
		// workspace expectation is needed to prove this path succeeds.
		cmd := m.handleKeyPressMsg(ctrlKey('e'))

		require.NotNil(t, cmd)
	})
}

func TestHandleKeyPressMsg_GlobalDetailsTogglesWhenHasSession(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	require.False(t, m.detailsOpen)

	m.handleKeyPressMsg(ctrlKey('d'))

	require.True(t, m.detailsOpen)
}

func TestHandleKeyPressMsg_GlobalEndFollow(t *testing.T) {
	t.Parallel()

	t.Run("ctrl+end", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.updateLayoutAndSize()

		require.NotPanics(t, func() {
			m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
		})
	})

	t.Run("ctrl+down", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.updateLayoutAndSize()

		require.NotPanics(t, func() {
			m.handleKeyPressMsg(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl})
		})
	})
}

func TestHandleKeyPressMsg_GlobalSuspend(t *testing.T) {
	t.Parallel()

	t.Run("busy warns instead of suspending", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentBusyCache.set(true)

		cmd := m.handleKeyPressMsg(ctrlKey('z'))

		require.NotNil(t, cmd)
	})

	t.Run("idle suspends", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		cmd := m.handleKeyPressMsg(ctrlKey('z'))

		require.NotNil(t, cmd)
	})
}

func TestHandleKeyPressMsg_GlobalCyclePermissionMode(t *testing.T) {
	t.Parallel()

	m, ws := newMockBusyUI(t)
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

	ws.EXPECT().PermissionMode().Return(permission.ModeManual)
	ws.EXPECT().PermissionSetMode(permission.ModeAutoAcceptEdits)
	require.NotPanics(t, func() {
		m.handleKeyPressMsg(shiftTab)
	})
	require.Equal(t, permission.ModeAutoAcceptEdits, m.permissionModeCached())

	ws.EXPECT().PermissionMode().Return(permission.ModeAutoAcceptEdits)
	ws.EXPECT().PermissionSetMode(permission.ModeYolo)
	m.handleKeyPressMsg(shiftTab)
	require.Equal(t, permission.ModeYolo, m.permissionModeCached())

	ws.EXPECT().PermissionMode().Return(permission.ModeYolo)
	ws.EXPECT().PermissionSetMode(permission.ModeManual)
	m.handleKeyPressMsg(shiftTab)
	require.Equal(t, permission.ModeManual, m.permissionModeCached())
}

func TestHandleKeyPressMsg_OnboardingStateIsNoOp(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.state = uiOnboarding

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(keyMsg("x"))
	})
}

func TestHandleKeyPressMsg_InitializeState(t *testing.T) {
	t.Parallel()

	t.Run("Switch flips the selection", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.state = uiInitialize
		before := m.onboarding.yesInitializeSelected

		m.handleKeyPressMsg(keyMsg("tab"))

		require.NotEqual(t, before, m.onboarding.yesInitializeSelected)
	})

	t.Run("No declines and returns to landing", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.state = uiInitialize

		m.handleKeyPressMsg(keyMsg("n"))

		require.Equal(t, uiLanding, m.state)
	})
}

func TestHandleKeyPressMsg_CompletionsOpenGateDoesNotPanic(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.completionsOpen = true

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(keyMsg("x"))
	})
}

func TestHandleKeyPressMsg_AddImage(t *testing.T) {
	t.Parallel()

	t.Run("unsupported model is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: catwalk.Model{SupportsImages: false}}

		m.handleKeyPressMsg(ctrlKey('f'))

		require.False(t, m.dialog.ContainsDialog(dialog.FilePickerID))
	})

	t.Run("supported opens the file picker", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: catwalk.Model{SupportsImages: true}}

		m.handleKeyPressMsg(ctrlKey('f'))

		require.True(t, m.dialog.ContainsDialog(dialog.FilePickerID))
	})
}

func TestHandleKeyPressMsg_PasteImage(t *testing.T) {
	t.Parallel()

	t.Run("unsupported model is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: catwalk.Model{SupportsImages: false}}

		require.NotPanics(t, func() {
			m.handleKeyPressMsg(ctrlKey('v'))
		})
	})

	t.Run("supported queues a clipboard read without executing it", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentReady = true
		m.agentActiveKnown = true
		m.agentActiveSession = "s1"
		m.agentActive = workspace.ActiveAgent{CatwalkCfg: catwalk.Model{SupportsImages: true}}

		require.NotPanics(t, func() {
			m.handleKeyPressMsg(ctrlKey('v'))
		})
	})
}

func TestHandleKeyPressMsg_SendMessage(t *testing.T) {
	t.Parallel()

	t.Run("backslash continuation inserts a newline instead of sending", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.textarea.SetValue(`line one\`)

		m.handleKeyPressMsg(keyMsg("enter"))

		require.Contains(t, m.textarea.Value(), "line one")
		require.False(t, m.dialog.HasDialogs())
	})

	t.Run("exit opens the quit dialog", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.textarea.SetValue("exit")

		m.handleKeyPressMsg(keyMsg("enter"))

		require.True(t, m.dialog.ContainsDialog(dialog.QuitID))
	})

	t.Run("empty prompt with no attachments is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.textarea.SetValue("")

		cmd := m.handleKeyPressMsg(keyMsg("enter"))

		require.Nil(t, cmd)
	})

	t.Run("empty prompt with a pasted image attachment discards it silently", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.textarea.SetValue("")
		m.attachments.Update(message.Attachment{
			FileName: "paste_0.png",
			MimeType: "image/png",
			Content:  []byte("fake-png-bytes"),
		})
		require.Len(t, m.attachments.List(), 1, "the pasted image must be queued before Enter is pressed")

		cmd := m.handleKeyPressMsg(keyMsg("enter"))

		// ContainsTextAttachment only recognizes text/* attachments, so an
		// image-only send is rejected same as an empty prompt. But unlike
		// the empty-prompt case, the attachment list was already reset
		// before this check runs (see the SendMessage handling in ui.go),
		// so the pasted image is lost with no error shown to the user
		// instead of being kept for a retry.
		require.Nil(t, cmd, "an image-only prompt is not sent")
		require.Empty(t, m.attachments.List(), "pins current behavior: the pasted image is silently discarded")
	})

	t.Run("bang mode runs a shell command instead of sending", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.bangMode = true
		m.textarea.SetValue("ls")

		cmd := m.handleKeyPressMsg(keyMsg("enter"))

		require.NotNil(t, cmd)
		require.False(t, m.bangMode)
	})

	t.Run("a normal prompt sends the message", func(t *testing.T) {
		t.Parallel()
		m, ws := newMockBusyUI(t)
		ws.EXPECT().AgentReadyErr().Return(nil)
		m.textarea.SetValue("hello")

		cmd := m.handleKeyPressMsg(keyMsg("enter"))

		require.NotNil(t, cmd)
		require.Empty(t, m.textarea.Value())
	})
}

func TestHandleKeyPressMsg_NewSession(t *testing.T) {
	t.Parallel()

	t.Run("no session is a no-op", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.session = nil

		require.NotPanics(t, func() {
			m.handleKeyPressMsg(ctrlKey('n'))
		})
	})

	t.Run("busy warns instead of starting a new session", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentBusyCache.set(true)

		cmd := m.handleKeyPressMsg(ctrlKey('n'))

		require.NotNil(t, cmd)
		require.Equal(t, "s1", m.session.ID, "a busy agent must not be interrupted by a new session")
	})
}

func TestHandleKeyPressMsg_EditorTabSwitchesFocusToChat(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusEditor

	m.handleKeyPressMsg(keyMsg("tab"))

	require.Equal(t, uiFocusMain, m.focus)
}

func TestHandleKeyPressMsg_OpenEditor(t *testing.T) {
	t.Parallel()

	t.Run("busy warns instead of opening the editor", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)
		m.agentBusyCache.set(true)

		cmd := m.handleKeyPressMsg(ctrlKey('o'))

		require.NotNil(t, cmd)
	})

	t.Run("idle opens the external editor", func(t *testing.T) {
		t.Parallel()
		m, _ := newMockBusyUI(t)

		cmd := m.handleKeyPressMsg(ctrlKey('o'))

		require.NotNil(t, cmd)
	})
}

func TestHandleKeyPressMsg_Newline(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)

	m.handleKeyPressMsg(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})

	require.Equal(t, "\n", m.textarea.Value())
}

func TestHandleKeyPressMsg_HistoryNavigation(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.promptHistory.messages = []string{"first", "second"}
	m.promptHistory.index = -1

	m.handleKeyPressMsg(keyMsg("up"))
	require.Equal(t, "first", m.textarea.Value())

	// historyPrev() leaves the cursor at column 0, so the first down only
	// walks the cursor back to the end of the line; the second down (now
	// at editor end) is what actually returns to the saved draft.
	m.handleKeyPressMsg(keyMsg("down"))
	m.handleKeyPressMsg(keyMsg("down"))
	require.Empty(t, m.textarea.Value())
}

func TestHandleKeyPressMsg_EditorEscapeRestoresDraft(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.promptHistory.messages = []string{"first"}
	m.promptHistory.index = -1
	m.handleKeyPressMsg(keyMsg("up"))
	require.Equal(t, "first", m.textarea.Value())

	m.handleKeyPressMsg(keyMsg("esc"))

	require.Equal(t, -1, m.promptHistory.index)
}

func TestHandleKeyPressMsg_EditorCommandsOnlyWhenEmpty(t *testing.T) {
	t.Parallel()

	t.Run("empty textarea opens commands", func(t *testing.T) {
		t.Parallel()
		m, _ := newKeyRoutingUI(t)
		m.textarea.SetValue("")

		m.handleKeyPressMsg(keyMsg("/"))

		require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
	})

	t.Run("non-empty textarea types the slash instead", func(t *testing.T) {
		t.Parallel()
		m, _ := newKeyRoutingUI(t)
		m.textarea.Focus()
		m.textarea.SetValue("hello")

		m.handleKeyPressMsg(keyMsg("/"))

		require.False(t, m.dialog.HasDialogs())
		require.Contains(t, m.textarea.Value(), "/")
	})
}

func TestHandleKeyPressMsg_PlainTypingUpdatesTextareaAndDraft(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.textarea.Focus()

	m.handleKeyPressMsg(keyMsg("x"))

	require.Equal(t, "x", m.textarea.Value())
}

func TestHandleKeyPressMsg_BangModeEntryAndExit(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.textarea.Focus()
	require.False(t, m.bangMode)

	m.handleKeyPressMsg(keyMsg("!"))

	require.True(t, m.bangMode)
	require.True(t, m.bangWasEmpty)

	m.handleKeyPressMsg(keyMsg("backspace"))

	require.False(t, m.bangMode)
}

func TestHandleKeyPressMsg_MentionAgentTriggerOpensCompletions(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().Config().Return(&config.Config{
		Agents: map[string]config.Agent{
			"reviewer": {ID: "reviewer", Name: "Reviewer", Mode: config.AgentModeSubagent},
		},
	}).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	m := newBusyUIWithWorkspace(ws)

	m.handleKeyPressMsg(keyMsg("@"))

	require.True(t, m.completionsOpen)
}

func TestHandleKeyPressMsg_MainFocusTabReturnsToEditor(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain

	m.handleKeyPressMsg(keyMsg("tab"))

	require.Equal(t, uiFocusEditor, m.focus)
}

func TestHandleKeyPressMsg_MainFocusTabStaysWhenViewingSubAgent(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain
	m.session = &session.Session{ID: "child1", ParentSessionID: "parent1"}
	m.sessionIsBranch = false

	m.handleKeyPressMsg(keyMsg("tab"))

	require.Equal(t, uiFocusMain, m.focus)
}

func TestHandleKeyPressMsg_MainFocusNewSession(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain
	m.agentBusyCache.set(true)

	cmd := m.handleKeyPressMsg(ctrlKey('n'))

	require.NotNil(t, cmd)
}

func TestHandleKeyPressMsg_MainFocusOpenSubSessionWithoutSelectionIsNoOp(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(keyMsg("enter"))
	})
}

func TestHandleKeyPressMsg_MainFocusExpand(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain

	require.NotPanics(t, func() {
		m.handleKeyPressMsg(keyMsg(" "))
	})
}

func TestHandleKeyPressMsg_MainFocusScrolling(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain
	m.updateLayoutAndSize()

	for _, k := range []tea.KeyPressMsg{
		keyMsg("up"), keyMsg("down"),
		{Code: 'K', Text: "K"},
		{Code: 'J', Text: "J"},
		keyMsg("u"), keyMsg("d"),
		{Code: tea.KeyPgUp},
		{Code: tea.KeyPgDown},
		{Code: 'g', Text: "g"},
		{Code: 'G', Text: "G"},
	} {
		require.NotPanics(t, func() {
			m.handleKeyPressMsg(k)
		})
	}
}

func TestHandleKeyPressMsg_MainFocusDefaultFallsThroughToGlobalKeys(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.focus = uiFocusMain
	require.False(t, m.status.ShowingAll())

	m.handleKeyPressMsg(ctrlKey('g'))

	require.True(t, m.status.ShowingAll())
}
