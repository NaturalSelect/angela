package model

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/event"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	fimage "github.com/NaturalSelect/angela/internal/ui/image"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// openDialog opens a dialog by its ID.
func (m *UI) openDialog(id string) tea.Cmd {
	var cmds []tea.Cmd
	switch id {
	case dialog.SessionsID:
		if cmd := m.openSessionsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.SubSessionsID:
		if cmd := m.openSubSessionsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ModelsID:
		if cmd := m.openModelsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.CommandsID:
		if cmd := m.openCommandsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.VariantsID:
		if cmd := m.openVariantsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.AgentsID:
		if cmd := m.openAgentsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.AgentModelAgentsID:
		if cmd := m.openAgentModelTargetDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.NotificationsID:
		if cmd := m.openNotificationsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.FilePickerID:
		if cmd := m.openFilesDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.QuitID:
		if cmd := m.openQuitDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.SandboxID:
		if cmd := m.openSandboxDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.MCPServersID:
		if cmd := m.openMCPServersDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	default:
		// Unknown dialog
		break
	}
	return tea.Batch(cmds...)
}

// openQuitDialog opens the quit confirmation dialog.
func (m *UI) openQuitDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.QuitID) {
		// Bring to front
		m.dialog.BringToFront(dialog.QuitID)
		return nil
	}

	quitDialog := dialog.NewQuit(m.com)
	m.dialog.OpenDialog(quitDialog)
	return nil
}

// openSandboxDialog opens the sandbox configuration dialog.
func (m *UI) openSandboxDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SandboxID) {
		// Bring to front
		m.dialog.BringToFront(dialog.SandboxID)
		return nil
	}

	m.dialog.OpenDialog(dialog.NewSandbox(m.com))
	return nil
}

// openModelsDialog opens the models dialog across every provider.
func (m *UI) openModelsDialog() tea.Cmd {
	return m.openModelsDialogFor("")
}

// openModelsDialogFor opens the models dialog, optionally narrowed to a
// single provider. Onboarding narrows it because the provider is already
// settled by then; ctrl+l does not, because switching provider is the
// whole point of that path.
func (m *UI) openModelsDialogFor(restrictTo catwalk.InferenceProvider) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ModelsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.ModelsID)
		return nil
	}

	isOnboarding := m.state == uiOnboarding
	modelsDialog := dialog.NewModels(m.com, isOnboarding, m.activeAgent())
	if restrictTo != "" {
		modelsDialog.RestrictToProvider(restrictTo)
	}

	m.dialog.OpenDialog(modelsDialog)

	return modelsDialog.InitialCmd()
}

// openProvidersDialog opens the provider selection dialog, the first
// step of onboarding.
func (m *UI) openProvidersDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.ProvidersID) {
		m.dialog.BringToFront(dialog.ProvidersID)
		return nil
	}

	providersDialog := dialog.NewProviders(m.com, m.state == uiOnboarding)
	m.dialog.OpenDialog(providersDialog)

	return providersDialog.InitialCmd()
}

// openCommandsDialog opens the commands dialog.
func (m *UI) openCommandsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.CommandsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.CommandsID)
		return nil
	}

	var sessionID string
	hasSession := m.session != nil
	if hasSession {
		sessionID = m.session.ID
	}
	hasTodos := hasSession && hasIncompleteTodos(m.session.Todos)
	hasQueue := m.promptQueue > 0

	commands, err := dialog.NewCommands(m.com, sessionID, hasSession, hasTodos, hasQueue, m.viewingBranch(), m.hasParentSession(), m.activeAgent(), m.customCommands, m.mcpPrompts)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(commands)

	return commands.InitialCmd()
}

// openAgentsDialog opens the primary-agent picker. With no session
// yet, a pick lands on the config-level default a new session starts
// on — the same way the variants dialog previews a preset before a
// session exists to scope it to.
func (m *UI) openAgentsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.AgentsID) {
		m.dialog.BringToFront(dialog.AgentsID)
		return nil
	}
	active := m.activeAgent()
	if active == nil {
		return util.ReportWarn("The agent is still starting up.")
	}

	agentsDialog, err := dialog.NewAgents(m.com, active.AgentID)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(agentsDialog)
	return nil
}

// openAgentModelTargetDialog opens the agent picker for the "Switch
// Agent Model" command: the first step of pinning some agent — any
// configured agent, not just the one driving this session — to a
// model+variant for the rest of this process. Unlike openAgentsDialog
// it does not require a known active agent: overriding another
// agent's model has nothing to do with what this session currently
// runs, so there is no reason to make it wait on that probe.
func (m *UI) openAgentModelTargetDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.AgentModelAgentsID) {
		m.dialog.BringToFront(dialog.AgentModelAgentsID)
		return nil
	}
	currentAgent := ""
	if active := m.activeAgent(); active != nil {
		currentAgent = active.AgentID
	}

	agentsDialog, err := dialog.NewAgentModelTarget(m.com, currentAgent)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(agentsDialog)
	return nil
}

// openNotificationsDialog opens the notification style picker dialog.
func (m *UI) openNotificationsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.NotificationsID) {
		m.dialog.BringToFront(dialog.NotificationsID)
		return nil
	}

	notificationsDialog := dialog.NewNotifications(m.com)
	m.dialog.OpenDialog(notificationsDialog)
	return nil
}

// sessionsListedMsg carries the session list read off the Update goroutine,
// so the sessions dialog can be opened once it arrives.
type sessionsListedMsg struct {
	sessions []session.Session
}

// openSessionsDialog opens the sessions dialog. If the dialog is already open,
// it brings it to the front. Otherwise, it lists all the sessions off the
// Update goroutine and opens the dialog once they arrive.
func (m *UI) openSessionsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SessionsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.SessionsID)
		return nil
	}

	ws := m.com.Workspace
	return func() tea.Msg {
		sessions, err := ws.ListSessions(context.Background())
		if err != nil {
			return util.ReportError(err)()
		}
		return sessionsListedMsg{sessions: sessions}
	}
}

// openSubSessionsDialog opens the picker for the sub-sessions of the session
// in view. The children are read off the Update goroutine; a session that has
// dispatched none gets a notice instead of an empty dialog once they arrive.
func (m *UI) openSubSessionsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SubSessionsID) {
		m.dialog.BringToFront(dialog.SubSessionsID)
		return nil
	}
	if m.session == nil {
		return nil
	}
	return m.loadSubSessionsCmd(m.session.ID)
}

// openFilesDialog opens the file picker dialog.
func (m *UI) openFilesDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.FilePickerID) {
		// Bring to front
		m.dialog.BringToFront(dialog.FilePickerID)
		return nil
	}

	filePicker, cmd := dialog.NewFilePicker(m.com)
	filePicker.SetImageCapabilities(&m.caps)
	m.dialog.OpenDialog(filePicker)
	event.FilePickerOpened()

	return cmd
}

// openPermissionsDialog opens the permissions dialog for a permission request.
func (m *UI) openPermissionsDialog(perm permission.PermissionRequest) tea.Cmd {
	// Close any existing permissions dialog first.
	m.dialog.CloseDialog(dialog.PermissionsID)

	// Get diff mode from config.
	var opts []dialog.PermissionsOption
	if diffMode := m.com.Config().Options.TUI.DiffMode; diffMode != "" {
		opts = append(opts, dialog.WithDiffMode(diffMode == "split"))
	}

	permDialog := dialog.NewPermissions(m.com, perm, opts...)
	m.dialog.OpenDialogWithGrace(permDialog)
	return nil
}

// queuePermissionRequest shows perm immediately if no permission
// dialog is currently open, or queues it otherwise. Concurrent
// sub-agents can each raise a request; none may be silently replaced
// and lost while the user is still looking at an earlier one.
func (m *UI) queuePermissionRequest(perm permission.PermissionRequest) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.PermissionsID) {
		m.pendingPermissions = append(m.pendingPermissions, perm)
		return nil
	}
	return m.openPermissionsDialog(perm)
}

// nextPermissionRequest opens the next queued permission request, if
// any, once the current one has been answered or otherwise dismissed.
func (m *UI) nextPermissionRequest() tea.Cmd {
	if len(m.pendingPermissions) == 0 {
		return nil
	}
	next := m.pendingPermissions[0]
	m.pendingPermissions = m.pendingPermissions[1:]
	return m.openPermissionsDialog(next)
}

// openBatchFormDialog activates a tabbed multi-question form in
// the editor area. Single questions render without tabs or confirm.
func (m *UI) openBatchFormDialog(batch question.Request) {
	// Close any existing question form first to prevent stacking.
	if qf, ok := m.activeInline.(*dialog.QuestionForm); ok && qf != nil {
		m.activeInline = nil
	}

	form := dialog.NewQuestionForm(m.com.Styles, batch)
	form.OnAnswer = func(responses []question.Answer) {
		m.com.Workspace.QuestionAnswer(responses)
	}
	form.OnCancel = func() {
		m.com.Workspace.QuestionCancel()
	}
	m.activeInline = form
	m.textarea.Blur()
	m.focus = uiFocusEditor
	m.activeInline.SetFocused(true)
	m.updateLayoutAndSize()
}

// handleQuestionNotification dismisses an open question form when
// any client resolved the pending batch. Only one question can be
// pending at a time, so any notification means the current form
// is stale regardless of BatchID.
func (m *UI) handleQuestionNotification(_ question.Notification) {
	if _, ok := m.activeInline.(*dialog.QuestionForm); ok {
		m.activeInline = nil
		m.textarea.Focus()
		m.updateLayoutAndSize()
	}
}

// editorContentWidth returns the content width available to the
// editor area for the current state. It depends only on terminal
// width and layout (not on editor height), so it can be computed
// before the editor's height is known. This is the single source
// of truth for the inline editor width used by both layout sizing
// and Height() queries.
func (m *UI) editorContentWidth() int {
	return m.width - 2*appMarginX // appRect horizontal margins
}

// shouldCollapseQuestion reports whether a question form should render
// in its collapsed one-line view. This is true only when the form is
// unfocused and would consume more than half the terminal height.
func (m *UI) shouldCollapseQuestion(qf *dialog.QuestionForm) bool {
	return m.focus != uiFocusEditor && m.height > 0 && qf.Height(m.editorContentWidth()) > m.height*2/5
}

// handleRunCustomCommand handles dialog.ActionRunCustomCommand.
func (m *UI) handleRunCustomCommand(msg dialog.ActionRunCustomCommand) tea.Cmd {
	var cmds []tea.Cmd
	if len(msg.Arguments) > 0 && msg.Args == nil {
		m.dialog.CloseFrontDialog()
		argsDialog := dialog.NewArguments(
			m.com,
			"Custom Command Arguments",
			"",
			msg.Arguments,
			msg, // Pass the action as the result
		)
		m.dialog.OpenDialog(argsDialog)
		return tea.Batch(cmds...)
	}
	content := msg.Content
	if msg.Args != nil {
		content = substituteArgs(content, msg.Args)
	}
	// If this is a skill command, format it using the skill's FormatInvocation method
	if msg.Skill != nil {
		content = msg.Skill.FormatInvocation()
	}
	cmds = append(cmds, m.sendMessage(content))
	m.dialog.CloseFrontDialog()
	return tea.Batch(cmds...)
}

// handlePermissionResponse handles dialog.ActionPermissionResponse.
func (m *UI) handlePermissionResponse(msg dialog.ActionPermissionResponse) tea.Cmd {
	var cmds []tea.Cmd
	m.dialog.CloseDialog(dialog.PermissionsID)
	switch msg.Action {
	case dialog.PermissionAllow:
		m.com.Workspace.PermissionGrant(msg.Permission)
	case dialog.PermissionAllowForSession:
		m.com.Workspace.PermissionGrantPersistent(msg.Permission)
	case dialog.PermissionDeny:
		m.com.Workspace.PermissionDeny(msg.Permission)
	}
	if cmd := m.nextPermissionRequest(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// handleSelectNotificationStyle handles dialog.ActionSelectNotificationStyle.
func (m *UI) handleSelectNotificationStyle(msg dialog.ActionSelectNotificationStyle) tea.Cmd {
	var cmds []tea.Cmd
	cfg := m.com.Config()
	if cfg != nil && cfg.Options != nil {
		cfg.Options.Notifications = msg.Style
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.notifications", msg.Style); err != nil {
			cmds = append(cmds, util.ReportError(err))
		} else {
			cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Notifications set to: "+msg.Style)))
		}
		// Reinitialize notification backend with new style.
		m.notifyBackend = selectNotificationBackend(m.caps, cfg)
	}
	m.dialog.CloseDialog(dialog.NotificationsID)
	return tea.Batch(cmds...)
}

// handleClose handles dialog.ActionClose.
func (m *UI) handleClose() tea.Cmd {
	var cmds []tea.Cmd
	if m.state == uiOnboarding {
		if cmd := m.closeOnboardingDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...)
	}

	// NOTE: The deferred reset runs after the batch below is built, exactly
	// as it did when this code sat inline in handleDialogMsg.
	if m.dialog.ContainsDialog(dialog.FilePickerID) {
		defer fimage.ResetCache()
	}

	m.dialog.CloseFrontDialog()

	if m.focus == uiFocusEditor {
		cmds = append(cmds, m.textarea.Focus())
	}
	return tea.Batch(cmds...)
}
