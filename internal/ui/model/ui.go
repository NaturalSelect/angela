package model

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	fimage "github.com/NaturalSelect/angela/internal/ui/image"
	"github.com/NaturalSelect/angela/internal/ui/logo"
	"github.com/NaturalSelect/angela/internal/ui/notification"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/undo"
	"github.com/NaturalSelect/angela/internal/version"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	xstrings "github.com/charmbracelet/x/exp/strings"
)

// Compact mode breakpoints.
const (
	compactModeHeightBreakpoint = 30
)

// If pasted text has more than 10 newlines, treat it as a file attachment.
const pasteLinesThreshold = 10

// If pasted text has more than 1000 columns, treat it as a file attachment.
const pasteColsThreshold = 1000

// Session details panel max size.
const (
	sessionDetailsMaxHeight = 24
	sessionDetailsMaxWidth  = 100
)

// TextareaMaxHeight is the maximum height of the prompt textarea.
const TextareaMaxHeight = 15

// editorHeightMargin is the vertical margin added to the textarea height: the
// attachments row on top, plus the box's own top and bottom border rows.
const editorHeightMargin = 3

// editorPromptWidth is the gutter width: marker and gap.
const editorPromptWidth = 2

// editorPromptGlyph is the marker on the first editor line.
const editorPromptGlyph = "❯ "

// editorBoxBorders is the column count the box border takes from the editor
// band: one on each side.
const editorBoxBorders = 2

// TextareaMinHeight is the minimum height of the prompt textarea.
const TextareaMinHeight = 3

// uiFocusState represents the current focus state of the UI.
type uiFocusState uint8

// Possible uiFocusState values.
const (
	uiFocusNone uiFocusState = iota
	uiFocusEditor
	uiFocusMain
)

type uiState uint8

// Possible uiState values.
const (
	uiOnboarding uiState = iota
	uiInitialize
	uiLanding
	uiChat
)

type openEditorMsg struct {
	Text string
}

type shellResultMsg struct {
	PendingID string // ID of the pending ShellItem to update.
	Command   string
	Output    string
	ExitCode  int
}

// shellStreamMsg carries incremental output from a streaming shell command.
type shellStreamMsg struct {
	PendingID string
	Chunk     string
	streamCh  <-chan string // unexported; used to continue draining
}

type (
	// cancelTimerExpiredMsg is sent when the cancel timer expires.
	cancelTimerExpiredMsg struct{}
	// userCommandsLoadedMsg is sent when user commands are loaded.

	userCommandsLoadedMsg struct {
		Commands []commands.CustomCommand
		Skills   []skills.CatalogEntry
	}
	// mcpPromptsLoadedMsg is sent when mcp prompts are loaded.
	mcpPromptsLoadedMsg struct {
		Prompts []commands.MCPPrompt
	}
	// mcpStateChangedMsg is sent when there is a change in MCP client states.
	mcpStateChangedMsg struct {
		states map[string]mcp.ClientInfo
	}
	// sendMessageMsg is sent to send a message.
	// currently only used for mcp prompts.
	sendMessageMsg struct {
		Content     string
		Attachments []message.Attachment
	}

	// closeDialogMsg is sent to close the current dialog.
	closeDialogMsg struct{}

	// copyChatHighlightMsg is sent to copy the current chat highlight to clipboard.
	copyChatHighlightMsg struct{}

	// sessionFilesUpdatesMsg is sent when the files for this session have been updated
	sessionFilesUpdatesMsg struct {
		sessionFiles []SessionFile
	}
)

// UI represents the main user interface model.
type UI struct {
	com     *common.Common
	session *session.Session
	// sessionStack holds the levels drilled down from, root first. session
	// is always the level in view, so everything reading it — the pubsub
	// filter, the busy cache, the sidebar state — follows the stack top
	// without knowing the stack exists.
	sessionStack []sessionStackFrame
	sessionFiles []SessionFile

	// keeps track of read files while we don't have a session id
	sessionFileReads []string

	// initialSessionID is set when loading a specific session on startup.
	initialSessionID string
	// continueLastSession is set to continue the most recent session on startup.
	continueLastSession bool

	lastUserMessageTime int64

	// The width and height of the terminal in cells.
	width  int
	height int
	layout uiLayout

	isTransparent bool

	focus uiFocusState
	state uiState

	keyMap KeyMap
	keyenh tea.KeyboardEnhancementsMsg

	dialog *dialog.Overlay
	// pendingPermissions holds PermissionRequests that arrived while a
	// permission dialog was already open, in FIFO order, so a sibling
	// sub-agent's request is queued instead of silently discarded.
	pendingPermissions []permission.PermissionRequest
	status             *Status

	// turnState holds what the turn status line reports; see turnstatus.go.
	turnState

	// sessionIsBranch memoizes whether the loaded session is a branch.
	// Resolving it reads config through the workspace, which the status
	// line renders too often to afford, so it is settled once per load.
	sessionIsBranch bool

	// bangState tracks the editor's bang (!) shell mode; see send.go.
	bangState

	header *header

	// sendProgressBar instructs the TUI to send progress bar updates to the
	// terminal.
	sendProgressBar    bool
	progressBarEnabled bool

	// caps hold different terminal capabilities that we query for.
	caps common.Capabilities

	// imageSupportReported tracks whether this session has already
	// told the workspace the client can render images (Kitty graphics
	// Unicode placeholder support confirmed via
	// [common.Capabilities.SupportsKittyPlaceholders]), so the
	// one-shot report fires at most once per session.
	imageSupportReported bool
	// placeholderMismatchLogged tracks whether the one-time Info log
	// explaining that Kitty graphics work but Unicode placeholders
	// don't has already fired, so it logs at most once per session.
	placeholderMismatchLogged bool

	// Editor components
	textarea textarea.Model

	// Active inline editor replaces the textarea when non-nil.
	activeInline dialog.InlineEditor
	// inlineCursor stores the cursor from the last inline editor
	// Draw call, used by the cursor positioning logic below.
	inlineCursor *tea.Cursor

	// Attachment list
	attachments *attachments.Attachments

	// completionPopup holds the "@" and "&" mention popup; see
	// completion_popup.go.
	completionPopup

	// Chat components
	chat *Chat

	// onboarding state
	onboarding struct {
		yesInitializeSelected bool

		// step is the stage of the first-run flow; provider is the one
		// picked in its first step, which the later steps act on.
		step     onboardingStep
		provider catwalk.Provider

		// catalog is the provider list the providers dialog had already
		// fetched when the user chose to add a custom provider instead
		// of a catalog entry. Carried along so the custom provider form
		// can check for id collisions without fetching it again.
		catalog []catwalk.Provider

		// model is the pick the configuration step edits, and
		// catwalkModel its catalog entry — zero for a hand-typed model
		// the catalog has never listed.
		model        config.SelectedModel
		catwalkModel config.ProviderModel
	}

	// lspCache memoizes the workspace LSP state; see lsp.go.
	lspCache

	// mcp
	mcpStates map[string]mcp.ClientInfo

	// skills
	skillStates []*skills.SkillState

	// Notification state
	notifyBackend       notification.Backend
	notifyWindowFocused bool
	// custom commands & mcp commands
	customCommands []commands.CustomCommand
	mcpPrompts     []commands.MCPPrompt
	// skillEntries is the full skill catalog (regardless of
	// UserInvocable), used to populate the "&" mention popup.
	skillEntries []skills.CatalogEntry

	// forceCompactMode tracks whether compact mode is forced by user toggle
	forceCompactMode bool

	// isCompact tracks whether we're currently in compact layout mode (either
	// by user toggle or auto-switch based on window size)
	isCompact bool

	// detailsOpen tracks whether the details panel is open.
	detailsOpen bool

	// stashedPrompts holds prompts taken back for a session that is no
	// longer on screen, restored when that session is loaded again.
	stashedPrompts map[string][]message.QueuedPrompt

	// workspaceProbes memoizes what the workspace reports; see
	// workspace_cache.go.
	workspaceProbes

	// mouse highlighting related state
	lastClickTime time.Time
	hoverX        int
	hoverY        int

	// Prompt history for up/down navigation through previous messages.
	promptHistory struct {
		messages []string
		index    int
		draft    string
	}
}

// New creates a new instance of the [UI] model.
func New(com *common.Common, initialSessionID string, continueLast bool) *UI {
	// Editor components
	ta := textarea.New()
	ta.SetStyles(com.Styles.Editor.Textarea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = TextareaMinHeight
	ta.MaxHeight = TextareaMaxHeight
	ta.Focus()

	scrollbarMode := config.ScrollbarDefault
	if cfg := com.Config(); cfg.Options.TUI != nil && cfg.Options.TUI.Scrollbar != "" {
		scrollbarMode = cfg.Options.TUI.Scrollbar
	}
	ch := NewChat(com, scrollbarMode)

	keyMap := DefaultKeyMap()

	// Completions component
	comp := completions.New(
		com.Styles.Completions.Normal,
		com.Styles.Completions.Focused,
		com.Styles.Completions.Match,
	)

	turnSpinner := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(com.Styles.TurnStatus.Spinner),
	)

	// Attachments component
	attachments := attachments.New(
		attachments.NewRenderer(
			com.Styles.Attachments.Normal,
			com.Styles.Attachments.Deleting,
			com.Styles.Attachments.Image,
			com.Styles.Attachments.Text,
			com.Styles.Attachments.Skill,
			com.Styles.Attachments.Remove,
		),
		attachments.Keymap{
			DeleteMode: keyMap.Editor.AttachmentDeleteMode,
			DeleteAll:  keyMap.Editor.DeleteAllAttachments,
			Escape:     keyMap.Editor.Escape,
		},
	)

	header := newHeader(com)

	ui := &UI{
		com:                 com,
		dialog:              dialog.NewOverlay(),
		keyMap:              keyMap,
		textarea:            ta,
		chat:                ch,
		header:              header,
		completionPopup:     completionPopup{completions: comp},
		attachments:         attachments,
		turnState:           turnState{turnSpinner: turnSpinner},
		lspCache:            lspCache{lspStates: make(map[string]workspace.LSPClientInfo)},
		mcpStates:           make(map[string]mcp.ClientInfo),
		notifyBackend:       notification.NoopBackend{},
		notifyWindowFocused: true,
		initialSessionID:    initialSessionID,
		continueLastSession: continueLast,
		skillStates:         skills.GetLatestStates(),
	}

	status := NewStatus(com, ui)

	// Seed the permission-mode cache once at construction; afterwards it
	// is kept fresh by write-through toggles and off-thread refreshes so
	// Update and View never probe the workspace synchronously.
	mode := com.Workspace.PermissionMode()
	ui.permissionModeCache.set(mode)

	// Seed the sandbox indicator the same way; see the sandboxActive
	// field doc for why no refresh loop is needed afterwards.
	ui.sandboxActive = com.Workspace.IsInSandbox()

	// Seed the memoized agent ready/active state the same way so the
	// first frame renders the model info; the busy probe keeps it fresh
	// afterwards. There is no session yet at construction, so this seeds
	// the configured default and stamps it for the empty session.
	if com.Workspace.AgentIsReady() {
		ui.agentReady = true
		active, err := com.Workspace.AgentActive(context.Background(), "")
		if err == nil {
			ui.agentActive = active
			// activeAgent() also requires the stamp, not just the
			// value: without it the seed above is never read and the
			// first frame falls back to "unknown". The empty session
			// ID is the right stamp because there is no session yet.
			ui.agentActiveKnown = true
			ui.agentActiveSession = ""
		}
	}
	ui.setEditorPrompt(mode)
	ui.textarea.Placeholder = ui.editorPlaceholder()
	ui.status = status

	// Initialize compact mode from config
	ui.forceCompactMode = com.Config().Options.TUI.CompactMode

	// set onboarding state defaults
	ui.onboarding.yesInitializeSelected = true

	desiredState := uiLanding
	desiredFocus := uiFocusEditor
	if !com.Config().IsConfigured() {
		desiredState = uiOnboarding
	} else if n, _ := com.Workspace.ProjectNeedsInitialization(); n {
		desiredState = uiInitialize
	}

	// set initial state
	ui.setState(desiredState, desiredFocus)

	opts := com.Config().Options

	// disable indeterminate progress bar
	ui.progressBarEnabled = opts.Progress == nil || *opts.Progress
	// enable transparent mode
	ui.isTransparent = opts.TUI.Transparent != nil && *opts.TUI.Transparent
	// --kitty-placeholders wins over options.tui.kitty_placeholders, which
	// wins over auto-detection.
	if com.KittyPlaceholders != nil {
		ui.caps.KittyPlaceholdersOverride = com.KittyPlaceholders
	} else {
		ui.caps.KittyPlaceholdersOverride = opts.TUI.KittyPlaceholders
	}

	return ui
}

// Init initializes the UI model.
func (m *UI) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.state == uiOnboarding {
		if cmd := m.openOnboardingStep(onboardingStepProvider); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// load the user commands async
	cmds = append(cmds, m.loadCustomCommands())
	// load prompt history async
	cmds = append(cmds, m.loadPromptHistory())
	// Prime the memoized LSP state off-thread.
	if cmd := m.requestLSPRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// load initial session if specified
	if cmd := m.loadInitialSession(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Prime the memoized busy/permission state off-thread.
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.checkPendingMCPAuth())
	// A forced-on --kitty-placeholders / options.tui.kitty_placeholders
	// never gets a terminal reply to trigger these, since the Kitty
	// graphics query may not be sent at all (e.g. over SSH with an
	// unrecognized TERM). Without an override both are no-ops here.
	if cmd := m.maybeReportImageSupport(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.applyImageCaps(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// loadInitialSession loads the initial session if one was specified on startup.
func (m *UI) loadInitialSession() tea.Cmd {
	switch {
	case m.state != uiLanding:
		// Only load if we're in landing state (i.e., fully configured)
		return nil
	case m.initialSessionID != "":
		return m.loadSession(m.initialSessionID)
	case m.continueLastSession:
		return func() tea.Msg {
			sessions, err := m.com.Workspace.ListSessions(context.Background())
			if err != nil || len(sessions) == 0 {
				return nil
			}
			return m.loadSession(sessions[0].ID)()
		}
	default:
		return nil
	}
}

// sendNotification returns a command that sends a notification if allowed by policy.
func (m *UI) sendNotification(n notification.Notification) tea.Cmd {
	if !m.shouldSendNotification() {
		return nil
	}

	return m.notifyBackend.Send(n)
}

// selectNotificationBackend chooses the appropriate notification backend based
// on terminal capabilities, environment, and user configuration. This is a pure
// function that should be called once during initialization or when capabilities
// change.
func selectNotificationBackend(caps common.Capabilities, cfg *config.Config) notification.Backend {
	// Check for explicit user preference first.
	if cfg != nil && cfg.Options != nil && cfg.Options.Notifications != "" {
		switch cfg.Options.Notifications {
		case "native":
			if !notification.NativeSupported {
				slog.Debug("Native notifications unavailable on this platform; using OSC backend", "osc99_supported", caps.OSC99Notifications)
				return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
			}
			slog.Debug("Using native backend (user preference)")
			return notification.NewNativeBackend(notification.Icon)
		case "osc":
			slog.Debug("Using OSC backend (user preference)", "osc99_supported", caps.OSC99Notifications)
			return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
		case "bell":
			slog.Debug("Using bell backend (user preference)")
			return notification.NewBellBackend()
		case "disabled":
			slog.Debug("Notifications disabled (user preference)")
			return notification.NoopBackend{}
		case "auto":
			// Fall through to auto-detection below.
		default:
			slog.Warn("Unknown notification style, using auto", "style", cfg.Options.Notifications)
		}
	}

	// Auto-detect based on environment and capabilities.
	_, isSSH := caps.Env.LookupEnv("SSH_TTY")

	// SSH sessions use terminal-based notifications (OSC 99 or 777).
	if isSSH {
		slog.Debug("Selected OSCBackend for SSH session", "osc99_supported", caps.OSC99Notifications)
		return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
	}

	// Local sessions: prefer OSC on macOS because the native backend (beeep)
	// uses terminal-notifier or AppleScript, which is slow and doesn't display
	// icons properly. Also prefer OSC where native notifications are unavailable
	// (illumos/solaris). OSC 99 provides a polished experience with icon support.
	if runtime.GOOS == "darwin" || !notification.NativeSupported {
		slog.Debug("Selected OSCBackend for local session", "osc99_supported", caps.OSC99Notifications, "native_supported", notification.NativeSupported)
		return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
	}

	// Non-macOS local sessions use native OS notifications if focus events are supported.
	// Without focus events, we can't suppress notifications when focused, so
	// we disable them entirely to avoid spamming the user.
	if caps.ReportFocusEvents {
		slog.Debug("Selected NativeBackend for local session")
		return notification.NewNativeBackend(notification.Icon)
	}

	slog.Debug("Selected NoopBackend (focus events not supported)")
	return notification.NoopBackend{}
}

func (m *UI) updateNotificationBackend() {
	cfg := m.com.Config()
	m.notifyBackend = selectNotificationBackend(m.caps, cfg)
}

// shouldSendNotification returns true if notifications should be sent based on
// current state. Focus reporting must be supported, window must not be
// focused, and notifications must not be disabled in config.
func (m *UI) shouldSendNotification() bool {
	cfg := m.com.Config()
	if cfg != nil && cfg.Options != nil && cfg.Options.Notifications == "disabled" {
		return false
	}
	return m.caps.ReportFocusEvents && !m.notifyWindowFocused
}

// setState changes the UI state and focus.
func (m *UI) setState(state uiState, focus uiFocusState) {
	if state == uiLanding {
		// Always turn off compact mode when going to landing
		m.isCompact = false
	}
	m.state = state
	m.focus = focus
	// Changing the state may change layout, so update it.
	m.updateLayoutAndSize()
}

// loadCustomCommands loads the custom commands asynchronously.
func (m *UI) loadCustomCommands() tea.Cmd {
	return func() tea.Msg {
		customCommands, err := commands.LoadCustomCommands(m.com.Config())
		if err != nil {
			slog.Error("Failed to load custom commands", "error", err)
		}
		// Append user-invocable skills as commands.
		skillEntries, err := m.com.Workspace.ListSkills(context.Background())
		if err != nil {
			slog.Error("Failed to load skill commands", "error", err)
		}
		customCommands = append(customCommands, commands.FromSkillCatalog(skillEntries)...)
		return userCommandsLoadedMsg{Commands: customCommands, Skills: skillEntries}
	}
}

// loadMCPrompts loads the MCP prompts asynchronously.
func (m *UI) loadMCPrompts() tea.Msg {
	prompts, err := m.com.Workspace.ListMCPPrompts(context.Background())
	if err != nil {
		slog.Error("Failed to load MCP prompts", "error", err)
	}
	if prompts == nil {
		// flag them as loaded even if there is none or an error
		prompts = []commands.MCPPrompt{}
	}
	return mcpPromptsLoadedMsg{Prompts: prompts}
}

// Update handles updates to the UI model.
func (m *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	// Update terminal capabilities
	m.caps.Update(msg)
	switch msg := msg.(type) {
	case tea.EnvMsg:
		// Is this Windows Terminal?
		if !m.sendProgressBar {
			m.sendProgressBar = slices.Contains(msg, "WT_SESSION")
		}
		cmds = append(cmds, common.QueryCmd(uv.Environ(msg)))
	case tea.ModeReportMsg:
		m.updateNotificationBackend()
	case uv.UnknownOscEvent:
		m.updateNotificationBackend()
	case tea.FocusMsg:
		m.notifyWindowFocused = true
	case tea.BlurMsg:
		m.notifyWindowFocused = false
	case pubsub.Event[notify.Notification]:
		if cmd := m.handleAgentNotification(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case busyStateMsg:
		cmds = append(cmds, m.applyBusyState(msg)...)
	case sandboxEnteredMsg:
		m.sandboxActive = true
		m.permissionModeCache.set(permission.ModeYolo)
		m.busyFetchGen++
		m.setEditorPrompt(permission.ModeYolo)
		cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Entered sandbox — permissions switched to yolo mode")))
	case branchStatusMsg:
		m.applyBranchStatus(msg)
	case promptQueueMsg:
		cmds = append(cmds, m.applyPromptQueue(msg)...)
	case lspStatesMsg:
		if cmd := m.applyLSPStates(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case agentModelChangedMsg:
		// The coordinator model changed (selection, thinking, reasoning):
		// re-fetch the memoized ready/model state off-thread.
		m.invalidateBusyCaches()
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case sessionMessagesMsg:
		cmds = append(cmds, m.handleSessionMessages(msg)...)
	case undoPreviewMsg:
		// Drop a preview for a session the user has already navigated
		// away from while the fetch was in flight.
		if msg.sessionID != m.currentSessionID() {
			break
		}
		m.dialog.OpenDialog(dialog.NewUndo(m.com, msg.sessionID, msg.preview))
	case exportImagesLoadedMsg:
		// Drop a result for a session the user has already navigated
		// away from while the fetch was in flight.
		if msg.sessionID != m.currentSessionID() {
			break
		}
		m.dialog.OpenDialog(dialog.NewImages(m.com, msg.images, msg.workingDir))
	case exportReportsLoadedMsg:
		// Drop a result for a session the user has already navigated
		// away from while the fetch was in flight.
		if msg.sessionID != m.currentSessionID() {
			break
		}
		m.dialog.OpenDialog(dialog.NewReports(m.com, msg.reports, msg.workingDir))
	case sessionsListedMsg:
		// A second request may have opened the dialog while this one was
		// in flight.
		if m.dialog.ContainsDialog(dialog.SessionsID) {
			break
		}
		m.dialog.OpenDialog(dialog.NewSessions(m.com, m.currentSessionID(), msg.sessions))
	case subSessionsLoadedMsg:
		// Drop a result for a session the user has already navigated
		// away from while the fetch was in flight, and one that arrives
		// after the list was opened by a second request.
		if msg.sessionID != m.currentSessionID() || m.dialog.ContainsDialog(dialog.SubSessionsID) {
			break
		}
		if len(msg.subs) == 0 {
			cmds = append(cmds, util.ReportInfo("No sub-sessions in this session."))
			break
		}
		m.dialog.OpenDialog(dialog.NewSubSessions(m.com, subSessionEntries(msg.subs)))
	case exportProfilesLoadedMsg:
		m.dialog.OpenDialog(dialog.NewProfiles(m.com, profiling.Profiles, msg.workingDir))
	case tpsComputedMsg:
		// Drop a result for a session the user has already navigated
		// away from while the fetch was in flight.
		if msg.sessionID != m.currentSessionID() {
			break
		}
		if cmd := m.appendTPSNotice(msg.dist, msg.ok, msg.avgTokens, msg.avgDurationMs); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case cacheComputedMsg:
		// Drop a result for a session the user has already navigated
		// away from while the fetch was in flight.
		if msg.sessionID != m.currentSessionID() {
			break
		}
		if cmd := m.appendCacheNotice(msg.dist, msg.ok, msg.readTokens, msg.creationTokens, msg.uncachedTokens); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case undoResultMsg:
		cmds = append(cmds, m.handleUndoResult(msg)...)
	case sideQuestionAnsweredMsg:
		cmds = append(cmds, m.handleSideQuestionAnswered(msg)...)
	case transparentToggledMsg:
		m.isTransparent = msg.on
		status := "disabled"
		if msg.on {
			status = "enabled"
		}
		cmds = append(cmds, util.ReportInfo("Transparent background "+status))
	case agentRunSubmittedMsg:
		// A prompt was just accepted (run started or enqueued): fetch the
		// authoritative busy/queue state to confirm the optimistic values
		// sendMessage wrote.
		m.invalidateBusyCaches()
		m.invalidatePromptQueue()
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case agentRunFailedMsg:
		// AgentRun blocks for the whole turn and reports a terminal failure
		// right here. A failure that happens outside a turn this UI
		// started arrives instead through handleAgentNotification's
		// TypeAgentError case. Both restore whatever this session still
		// has queued behind the failed turn.
		cmds = append(cmds, util.ReportError(msg.err))
		if cmd := m.restoreQueueOnAgentError(msg.sessionID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case queuedPromptsTakenMsg:
		if cmd := m.applyQueuedPromptsTaken(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case loadSessionMsg:
		cmds = append(cmds, m.handleLoadSession(msg)...)

	case sessionFilesUpdatesMsg:
		m.sessionFiles = msg.sessionFiles
		var paths []string
		for _, f := range msg.sessionFiles {
			paths = append(paths, f.LatestVersion.Path)
		}
		cmds = append(cmds, m.startLSPs(paths))

	case sendMessageMsg:
		cmds = append(cmds, m.sendMessage(msg.Content, msg.Attachments...))

	case userCommandsLoadedMsg:
		m.customCommands = msg.Commands
		m.skillEntries = msg.Skills
		dia := m.dialog.Dialog(dialog.CommandsID)
		if dia == nil {
			break
		}

		commands, ok := dia.(*dialog.Commands)
		if ok {
			commands.SetCustomCommands(m.customCommands)
		}

	case mcpStateChangedMsg:
		m.mcpStates = msg.states
		if dia := m.dialog.Dialog(dialog.MCPServersID); dia != nil {
			if mcpDialog, ok := dia.(*dialog.MCPServers); ok {
				mcpDialog.SetStates(m.mcpStates)
			}
		}
		// Auto-open the MCP auth dialog if any servers need authentication.
		if cmd := m.openMCPAuthDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case mcpPromptsLoadedMsg:
		m.mcpPrompts = msg.Prompts
		dia := m.dialog.Dialog(dialog.CommandsID)
		if dia == nil {
			break
		}

		commands, ok := dia.(*dialog.Commands)
		if ok {
			commands.SetMCPPrompts(m.mcpPrompts)
		}

	case promptHistoryLoadedMsg:
		m.promptHistory.messages = msg.messages
		m.promptHistory.index = -1
		m.promptHistory.draft = ""

	case closeDialogMsg:
		m.dialog.CloseFrontDialog()

	case pubsub.Event[session.Session]:
		if msg.Type == pubsub.DeletedEvent {
			if m.session != nil && m.session.ID == msg.Payload.ID {
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			break
		}
		if m.session != nil && msg.Payload.ID == m.session.ID {
			m.session = &msg.Payload
		}
	case pubsub.Event[message.Message]:
		cmds = append(cmds, m.handleMessageEvent(msg)...)
	case pubsub.Event[history.File]:
		cmds = append(cmds, m.handleFileEvent(msg.Payload))
	case pubsub.Event[app.LSPEvent]:
		// Refresh the memoized LSP state off-thread: LSPGetStates may block
		// and diagnostics events can arrive per edited file.
		if cmd := m.requestLSPRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[workspace.LSPEvent]:
		if cmd := m.requestLSPRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[skills.Event]:
		m.skillStates = msg.Payload.States
		// NOTE: Construction does not publish this event, so a reload is the
		// only way here, and the command and skill lists are stale after it.
		cmds = append(cmds, m.loadCustomCommands())
	case pubsub.Event[mcp.Event]:
		switch msg.Payload.Type {
		case mcp.EventStateChanged:
			return m, tea.Batch(
				m.handleStateChanged(),
				m.loadMCPrompts,
			)
		case mcp.EventPromptsListChanged:
			return m, handleMCPPromptsEvent(m.com.Workspace, msg.Payload.Name)
		case mcp.EventToolsListChanged:
			return m, handleMCPToolsEvent(m.com.Workspace, msg.Payload.Name)
		case mcp.EventResourcesListChanged:
			return m, handleMCPResourcesEvent(m.com.Workspace, msg.Payload.Name)
		}
	case pubsub.Event[permission.PermissionRequest]:
		if cmd := m.queuePermissionRequest(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("Permission required to execute \"%s\"", msg.Payload.ToolName),
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[permission.PermissionNotification]:
		if cmd := m.handlePermissionNotification(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[question.Request]:
		m.openBatchFormDialog(msg.Payload)
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("%d questions need your input", len(msg.Payload.Questions)),
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[question.Notification]:
		m.handleQuestionNotification(msg.Payload)
	case cancelTimerExpiredMsg:
		m.isCanceling = false
	case tea.TerminalVersionMsg:
		termVersion := strings.ToLower(msg.Name)
		// Only enable progress bar for the following terminals.
		if !m.sendProgressBar {
			m.sendProgressBar = xstrings.ContainsAnyOf(termVersion, "ghostty", "iterm2", "rio")
		}
		// XTVERSION can arrive after the Kitty graphics query reply,
		// so re-evaluate placeholder support now that the terminal
		// is identified.
		if cmd := m.maybeReportImageSupport(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.applyImageCaps(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Suppress the chat's full-height scan during the resize so a drag
		// only reflows visible items; it settles (and recomputes) shortly
		// after the last resize event.
		if m.state == uiChat {
			cmds = append(cmds, m.chat.BeginResize())
		}
		m.updateLayoutAndSize()
		if m.state == uiChat && m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if cmd := m.applyImageCaps(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.KeyboardEnhancementsMsg:
		m.keyenh = msg
		if msg.SupportsKeyDisambiguation() {
			m.keyMap.Models.SetHelp("ctrl+m", "models")
			m.keyMap.Editor.Newline.SetHelp("shift+enter", "newline")
		}
	case copyChatHighlightMsg:
		cmds = append(cmds, m.copyChatHighlight())
	case DelayedClickMsg:
		// Handle delayed single-click action (e.g., expansion, or
		// drilling into a sub-agent/branch session when the click
		// landed on an Agent tool call).
		if m.chat.HandleDelayedClick(msg) {
			if agentItem, ok := m.selectedAgentTool(); ok {
				cmds = append(cmds, m.enterSubSession(agentItem))
			}
		}
	case tea.MouseClickMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		// Route clicks to inline editors that support mouse interaction.
		// A collapsed question form (Tab'd away to the chat) only renders
		// a one-line summary; its component layout is stale, last built
		// by a full Draw. Skip hit-testing it and fall through instead:
		// handleClickFocus below re-expands it if the click landed in
		// the editor area, and the chat pane handles it otherwise.
		// Hit-testing it anyway risked matching stale coordinates and
		// swallowing clicks meant for the chat pane underneath, such as
		// the one that re-enters a parked branch session.
		qf, isQuestionForm := m.activeInline.(*dialog.QuestionForm)
		collapsed := isQuestionForm && m.shouldCollapseQuestion(qf)
		if m.activeInline != nil && !collapsed {
			if clickable, ok := m.activeInline.(dialog.MouseClickableEditor); ok {
				if done, handled := clickable.HandleMouseClick(msg.X, msg.Y); handled {
					if done {
						m.activeInline = nil
						m.textarea.Focus()
						m.updateLayoutAndSize()
					}
					return m, tea.Batch(cmds...)
				}
			}
		}

		// Route a click on the mention/file completions popup to the
		// same insert path Enter uses. The popup renders bottom-to-top
		// (completions.New sets SetReverse(true)), but
		// list.ItemIndexAtPosition walks items in un-reversed order, so
		// the clicked screen row has to be flipped before resolving
		// which item it landed on.
		if m.completionsOpen && m.completions.HasItems() && msg.Button == uv.MouseLeft {
			rect := m.completionsRect
			if msg.X >= rect.Min.X && msg.X < rect.Max.X && msg.Y >= rect.Min.Y && msg.Y < rect.Max.Y {
				yInList := (rect.Dy() - 1) - (msg.Y - rect.Min.Y)
				if cmd, keepOpen, handled := m.applyCompletionSelection(m.completions.SelectAt(yInList)); handled {
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					if !keepOpen {
						m.closeCompletions()
					}
				}
				return m, tea.Batch(cmds...)
			}
		}

		if cmd := m.handleClickFocus(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

		// Check if the click landed on an attachment's remove button.
		// The attachment chips are rendered on the first row of the
		// editor layout area, above the textarea.
		if m.activeInline == nil && msg.Button == uv.MouseLeft && len(m.attachments.List()) > 0 && msg.Y == m.layout.editor.Min.Y {
			relX := msg.X - m.layout.editor.Min.X
			if m.attachments.HandleClick(relX) {
				return m, tea.Batch(cmds...)
			}
		}

		// Check if the click landed on an ancestor crumb in the header
		// breadcrumb. The level in view is never a click target — there
		// is nowhere to jump to.
		if msg.Button == uv.MouseLeft && msg.Y == m.layout.header.Min.Y {
			relX := msg.X - m.layout.header.Min.X
			if idx, ok := m.header.HitTestBreadcrumb(relX); ok {
				if cmd := m.goToBreadcrumbLevel(idx); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
		}

		switch m.state {
		case uiChat:
			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			{
				if handled, cmd := m.chat.HandleMouseDown(x, y); handled {
					m.lastClickTime = time.Now()
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}

	case tea.MouseMotionMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		// Track hover position for inline editors.
		if m.activeInline != nil {
			if m.hoverX != msg.X || m.hoverY != msg.Y {
				m.hoverX = msg.X
				m.hoverY = msg.Y
				if clickable, ok := m.activeInline.(dialog.MouseClickableEditor); ok {
					clickable.SetHover(msg.X, msg.Y)
				}
			}
		}

		switch m.state {
		case uiChat:
			// Skip chat edge-scrolling when an inline editor is
			// active to prevent accidental scrolling while hovering
			// over question forms or other inline components.
			if m.activeInline != nil && m.focus == uiFocusEditor {
				break
			}
			if msg.Y <= 0 {
				if cmd := m.chat.ScrollByAndAnimate(-1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectPrev()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			} else if msg.Y >= m.chat.Height()-1 {
				if cmd := m.chat.ScrollByAndAnimate(1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectNext()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}

			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			m.chat.HandleMouseDrag(x, y)
		}

	case tea.MouseReleaseMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		switch m.state {
		case uiChat:
			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			if m.chat.HandleMouseUp(x, y) && m.chat.HasHighlight() {
				cmds = append(cmds, tea.Tick(doubleClickThreshold, func(t time.Time) tea.Msg {
					if time.Since(m.lastClickTime) >= doubleClickThreshold {
						return copyChatHighlightMsg{}
					}
					return nil
				}))
			}
		}
	case common.CoalescedWheelMsg:
		// Route wheel events to active inline editor only when the
		// mouse is over the editor area, so scrolling over the chat
		// still scrolls the chat.
		if m.activeInline != nil && image.Pt(msg.Mouse.X, msg.Mouse.Y).In(m.layout.editor) {
			if we, ok := m.activeInline.(common.WheelScrollable); ok {
				we.HandleWheel(msg.DeltaX, msg.DeltaY)
				return m, tea.Batch(cmds...)
			}
		}

		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		// Otherwise handle mouse wheel for chat. Use the coalesced delta
		// directly as the line count. Terminals like Ghostty send DeltaY=3
		// per physical wheel tick (matching their native scrollback), while
		// others send DeltaY=1.
		switch m.state {
		case uiChat:
			if msg.DeltaX != 0 {
				m.chat.ScrollSelectedShellHorizontal(int(msg.DeltaX))
			}
			lines := int(msg.DeltaY)
			if lines == 0 {
				break
			}
			if cmd := m.chat.ScrollByAndAnimate(lines); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if !m.chat.SelectedItemInView() {
				if lines > 0 && m.chat.AtBottom() {
					m.chat.SelectLast()
				} else {
					m.chat.SelectClosestInView()
				}
			}
		}
	case anim.StepMsg:
		if m.state == uiChat {
			if cmd := m.chat.Animate(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if m.chat.Follow() {
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		if cmd := m.status.Animate(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case scrollbarHideMsg:
		if m.state == uiChat {
			m.chat.HideScrollbar(msg.seq)
		}
	case chatWarmMsg:
		// A resize has settled; warm the message cache one batch at a time
		// so the scrollbar recompute never blocks the UI thread.
		if m.state == uiChat {
			cmd, done := m.chat.WarmStep(msg.seq)
			if cmd != nil {
				cmds = append(cmds, cmd)
			} else if done {
				// Heights are cached now, so the final layout pass (scrollbar
				// reservation) is cheap.
				m.updateLayoutAndSize()
			}
		}
	case spinner.TickMsg:
		if m.dialog.HasDialogs() {
			// route to dialog
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if m.state == uiChat && m.turnIsSpinning {
			var cmd tea.Cmd
			m.turnSpinner, cmd = m.turnSpinner.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case tea.KeyPressMsg:
		if cmd := m.handleKeyPressMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.PasteMsg:
		if m.activeInline != nil && m.focus == uiFocusEditor {
			if p, ok := m.activeInline.(dialog.PasteableEditor); ok {
				if cmd := p.HandlePaste(msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
		}
		if cmd := m.handlePasteMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case openEditorMsg:
		prevHeight := m.textarea.Height()
		m.textarea.SetValue(msg.Text)
		m.textarea.MoveToEnd()
		m.syncBangModeFromTextarea()
		cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
	case shellStreamMsg:
		cmds = append(cmds, m.handleShellStream(msg)...)
	case shellResultMsg:
		cmds = append(cmds, m.handleShellResult(msg)...)
	case dialog.ModelsCatalogMsg:
		// Routed here rather than through the overlay: the catalog can
		// land after another dialog has been stacked on top, and only
		// the models dialog knows what to do with it.
		if d, ok := m.dialog.Dialog(dialog.ModelsID).(*dialog.Models); ok {
			if cmd := d.SetProviders(msg.Providers); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case dialog.ProvidersCatalogMsg:
		// Routed here for the same reason as ModelsCatalogMsg: the
		// catalog can land after another dialog has been stacked on top.
		if d, ok := m.dialog.Dialog(dialog.ProvidersID).(*dialog.Providers); ok {
			d.SetProviders(msg.Providers)
		}
	case util.InfoMsg:
		cmds = append(cmds, m.handleInfoMsg(msg)...)
	case modelSwitchedMsg:
		cmds = append(cmds, util.CmdHandler(msg.toast))
		if len(msg.variants) > 0 {
			variantsDialog, err := dialog.NewVariants(m.com, msg.modelName, msg.variants, msg.current)
			if err != nil {
				cmds = append(cmds, util.ReportError(err))
				break
			}
			m.dialog.OpenDialog(variantsDialog)
		}
	case app.UpdateAvailableMsg:
		text := fmt.Sprintf("Angela update available: v%s → v%s.", msg.CurrentVersion, msg.LatestVersion)
		if msg.IsDevelopment {
			text = fmt.Sprintf("This is a development version of Angela. The latest version is v%s.", msg.LatestVersion)
		}
		ttl := 10 * time.Second
		m.status.SetInfoMsg(util.InfoMsg{
			Type: util.InfoTypeUpdate,
			Msg:  text,
			TTL:  ttl,
		})
		cmds = append(cmds, clearInfoMsgCmd(ttl))
	case util.ClearStatusMsg:
		m.status.ClearInfoMsg()
	case completions.CompletionItemsLoadedMsg:
		if m.completionsOpen {
			m.completions.SetItems(msg.Files, msg.Resources)
		}
	case uv.KittyGraphicsEvent:
		if !bytes.HasPrefix(msg.Payload, []byte("OK")) {
			slog.Warn("Unexpected Kitty graphics response",
				"response", string(msg.Payload),
				"options", msg.Options)
		}
		if cmd := m.maybeReportImageSupport(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.applyImageCaps(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case uv.PixelSizeEvent:
		if cmd := m.applyImageCaps(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case chat.ImageReadyMsg:
		m.chat.SetImageReady(msg.ItemID, msg.Key)
	case dialog.ActionMCPAuthStarted:
		cmds = append(cmds, m.authenticateMCP(msg.Ctx, msg.Name))
	case dialog.ActionMCPAuthComplete, dialog.ActionMCPAuthErrored:
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	default:
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// This logic gets triggered on any message type, but should it?
	switch m.focus {
	case uiFocusMain:
	case uiFocusEditor:
		m.textarea.Placeholder = m.editorPlaceholder()
	}

	// TTL backstop: schedule an off-thread re-probe for any memoized
	// workspace state that has gone stale. Never does IO on this
	// goroutine.
	cmds = append(cmds, m.staleWorkspaceRefreshCmds()...)

	// at this point this can only handle [message.Attachment] message, and we
	// should return all cmds anyway.
	_ = m.attachments.Update(msg)
	return m, tea.Batch(cmds...)
}

// sessionMessagesMsg carries a session's chat items, already built and
// with nested agent tool calls resolved, back to Update.
type sessionMessagesMsg struct {
	sessionID           string
	items               []chat.MessageItem
	lastUserMessageTime int64
}

// undoPreviewMsg carries the result of previewing an undo, fetched off
// the Update goroutine, so the confirmation dialog can be opened with
// it once it arrives.
type undoPreviewMsg struct {
	sessionID string
	preview   undo.Preview
}

// exportImagesLoadedMsg carries a session's generated images, fetched
// off the Update goroutine, so the "Export Image" picker dialog can
// be opened with them once they arrive.
type exportImagesLoadedMsg struct {
	sessionID  string
	images     []images.Image
	workingDir string
}

// exportReportsLoadedMsg carries a session's agent reports, collected
// off the Update goroutine, so the "Export Report" picker dialog can be
// opened with them once they arrive.
type exportReportsLoadedMsg struct {
	sessionID  string
	reports    []agenttools.Report
	workingDir string
}

// exportProfilesLoadedMsg carries the working directory the "Export Profile"
// picker defaults its output path to, resolved off the Update goroutine, so
// the picker can be opened once it arrives.
type exportProfilesLoadedMsg struct {
	workingDir string
}

// undoResultMsg carries the result of an executed undo, so its popped
// text can be restored to the editor and the outcome reported. The
// session is kept so the text is not dropped into another session's draft
// when the user navigated away while the undo ran.
type undoResultMsg struct {
	sessionID string
	result    undo.Result
}

func (m *UI) handleClickFocus(msg tea.MouseClickMsg) (cmd tea.Cmd) {
	switch {
	case m.state != uiChat:
		return nil
	case m.focus != uiFocusEditor && image.Pt(msg.X, msg.Y).In(m.layout.editor):
		m.focus = uiFocusEditor
		if m.activeInline != nil {
			m.activeInline.SetFocused(true)
		} else {
			cmd = m.textarea.Focus()
		}
		m.chat.Blur()
	case m.focus != uiFocusMain && image.Pt(msg.X, msg.Y).In(m.layout.main):
		m.focus = uiFocusMain
		m.textarea.Blur()
		m.chat.Focus()
	}
	return cmd
}

func (m *UI) handleDialogMsg(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	action := m.dialog.Update(msg)
	if action == nil {
		return tea.Batch(cmds...)
	}

	switch msg := action.(type) {
	// Generic dialog messages
	case dialog.ActionClose:
		return m.handleClose()
	case dialog.ActionCmd:
		if msg.Cmd != nil {
			cmds = append(cmds, msg.Cmd)
		}

	// Session dialog messages.
	case dialog.ActionSelectSession:
		m.dialog.CloseDialog(dialog.SessionsID)
		// A switcher pick is a sideways jump, not a level of the current
		// stack, so there is no saved draft to restore — clear the box
		// rather than carry text typed for the old session into the new
		// one.
		//
		// NOTE: Set before the load starts, so a load error replaces it.
		if cmd := m.showSessionLoading(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.loadSession(msg.Session.ID, loadSessionOpt{clearStack: true, draftAfter: &editorDraft{}}))

	case dialog.ActionEnterSubSession:
		m.dialog.CloseDialog(dialog.SubSessionsID)
		cmds = append(cmds, m.enterSubSessionByID(msg.SessionID))

	case dialog.ActionStopSubSession:
		// NOTE: The list stays open, with the row already showing the
		// stop, so several agents can be stopped in one visit.
		cmds = append(cmds, m.stopSubSessionCmd(msg.SessionID))

	case dialog.ActionAbortSubSession:
		// NOTE: The list stays open, with the row already showing the
		// abort, and the view stays on the parent the branch suspended.
		cmds = append(cmds, m.abortSubSessionCmd(msg.SessionID))

	// Open dialog message.
	case dialog.ActionOpenDialog:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.openDialog(msg.DialogID); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Command dialog messages.
	case dialog.ActionCyclePermissionMode:
		m.cyclePermissionMode()
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSelectNotificationStyle:
		return m.handleSelectNotificationStyle(msg)
	case dialog.ActionNewSession:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
			break
		}
		if cmd := m.newSession(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSummarize:
		return m.handleSummarize(msg)
	case dialog.ActionReload:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, func() tea.Msg {
			summary, err := m.com.Workspace.Reload(context.Background())
			if err != nil {
				return util.ReportError(err)()
			}
			return util.ReportInfo(summary)()
		})
	case dialog.ActionGenerateTitle:
		cmds = append(cmds, m.generateSessionTitle(msg.SessionID))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionCommit:
		return m.handleCommit(msg)
	case dialog.ActionExportSession:
		return m.handleExportSession(msg)
	case dialog.ActionExportImage:
		return m.handleExportImage(msg)
	case dialog.ActionSelectExportImage:
		return m.handleSelectExportImage(msg)
	case dialog.ActionExportReport:
		return m.handleExportReport(msg)
	case dialog.ActionSelectExportReport:
		return m.handleSelectExportReport(msg)
	case dialog.ActionImportReport:
		return m.handleImportReport(msg)
	case dialog.ActionExportProfile:
		return m.handleExportProfile(msg)
	case dialog.ActionSelectExportProfile:
		return m.handleSelectExportProfile(msg)
	case dialog.ActionUndo:
		return m.handleUndo(msg)
	case dialog.ActionUndoConfirmed:
		return m.handleUndoConfirmed(msg)
	case dialog.ActionAbortBranch:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.abortBranch(msg.SessionID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionGoToParent:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.leaveSubSession(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionScrollToTop:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.chat.ScrollToTopAndSelectFirst(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionScrollToBottom:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionScrollToLatestUser:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.chat.ScrollToLatestUserMessage(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionToggleHelp:
		m.status.ToggleHelp()
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionExternalEditor:
		return m.handleExternalEditor()
	case dialog.ActionToggleCompactMode:
		cmds = append(cmds, m.toggleCompactMode())
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleDetails:
		if m.hasSession() {
			m.detailsOpen = !m.detailsOpen
			m.updateLayoutAndSize()
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionShowTodos:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.showTodos(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionShowTPS:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.showTPS(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionShowCache:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.showCache(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSuspend:
		m.dialog.CloseDialog(dialog.CommandsID)
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait..."))
			break
		}
		cmds = append(cmds, tea.Suspend)
	case dialog.ActionToggleThinking:
		cmds = append(cmds, m.toggleThinkingCmd())
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleTransparentBackground:
		cmds = append(cmds, m.toggleTransparentCmd())
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionQuit:
		cmds = append(cmds, tea.Quit)
	case dialog.ActionEnableDockerMCP:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.enableDockerMCP)
	case dialog.ActionDisableDockerMCP:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.disableDockerMCP)
	case dialog.ActionEnableYoloSkipMerge:
		m.com.Workspace.PermissionSetYoloSkipMerge(true)
		cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Yolo mode will skip merge approval")))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionDisableYoloSkipMerge:
		m.com.Workspace.PermissionSetYoloSkipMerge(false)
		cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Yolo mode will always ask before merging")))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleMCPServer:
		// Left open deliberately: toggling should not close the dialog,
		// so the user can flip several servers in one visit.
		cmds = append(cmds, m.toggleMCPServer(msg.Name))
	case dialog.ActionEnterSandbox:
		m.dialog.CloseDialog(dialog.SandboxID)
		cfg := msg.Config
		cmds = append(cmds, func() tea.Msg { return m.enterSandbox(cfg) })
	case dialog.ActionInitializeProject:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, m.initializeProject())
		m.dialog.CloseDialog(dialog.CommandsID)

	case dialog.ActionSelectProvider:
		if cmd := m.handleSelectProvider(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionAddCustomProvider:
		if cmd := m.handleAddCustomProvider(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectModel:
		if cmd := m.handleSelectModel(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionConfigureModel:
		if cmd := m.handleConfigureModel(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectAgent:
		if cmd := m.handleSelectAgent(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectVariant:
		if cmd := m.handleSelectVariant(msg.Variant); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectAgentModelTarget:
		if cmd := m.handleSelectAgentModelTarget(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectAgentModel:
		if cmd := m.handleSelectAgentModel(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionSelectAgentModelVariant:
		m.dialog.CloseDialog(dialog.AgentModelVariantsID)
		cmds = append(cmds, m.applyAgentModelCmd(msg.AgentID, msg.Model, msg.Variant))
	case dialog.ActionClearAgentModelOverrides:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.clearAgentModelsCmd())
	case dialog.ActionPermissionResponse:
		return m.handlePermissionResponse(msg)

	case dialog.ActionFilePickerSelected:
		cmds = append(cmds, tea.Sequence(
			msg.Cmd(),
			func() tea.Msg {
				m.dialog.CloseDialog(dialog.FilePickerID)
				return nil
			},
			func() tea.Msg {
				fimage.ResetCache()
				return nil
			},
		))

	case dialog.ActionRunCustomCommand:
		return m.handleRunCustomCommand(msg)
	case dialog.ActionAttachSkill:
		m.dialog.CloseFrontDialog()
		cmds = append(cmds, m.attachSkill(msg.ID, msg.Name))
	case dialog.ActionAskSideQuestion:
		return m.handleAskSideQuestion(msg)
	case dialog.ActionRunMCPPrompt:
		return m.handleRunMCPPrompt(msg)
	default:
		cmds = append(cmds, util.CmdHandler(msg))
	}

	return tea.Batch(cmds...)
}

// substituteArgs replaces $ARG_NAME placeholders in content with actual values.
func substituteArgs(content string, args map[string]string) string {
	for name, value := range args {
		placeholder := "$" + name
		content = strings.ReplaceAll(content, placeholder, value)
	}
	return content
}

func (m *UI) handleKeyPressMsg(msg tea.KeyPressMsg) tea.Cmd {
	var cmds []tea.Cmd

	handleGlobalKeys := func(msg tea.KeyPressMsg) bool {
		switch {
		case key.Matches(msg, m.keyMap.Help):
			m.status.ToggleHelp()
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Commands):
			if cmd := m.openCommandsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Models):
			if cmd := m.openModelsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Sessions):
			if cmd := m.openSessionsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.CycleVariant):
			if cmd := m.cycleVariant(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Chat.Details) && m.hasSession():
			m.detailsOpen = !m.detailsOpen
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Chat.ToParent) && m.hasParentSession():
			if cmd := m.leaveSubSession(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Chat.EndFollow):
			if m.state == uiChat && m.hasSession() {
				if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Suspend):
			if m.isAgentBusy() {
				cmds = append(cmds, util.ReportWarn("Agent is busy, please wait..."))
				return true
			}
			cmds = append(cmds, tea.Suspend)
			return true
		case key.Matches(msg, m.keyMap.CyclePermissionMode):
			mode := m.cyclePermissionMode()
			cmds = append(cmds, util.ReportInfo("Permission mode: "+permissionModeLabel(mode)))
			return true
		}
		return false
	}

	if key.Matches(msg, m.keyMap.Quit) && !m.dialog.ContainsDialog(dialog.QuitID) {
		// Always handle quit keys first
		if cmd := m.openQuitDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}

		return tea.Batch(cmds...)
	}

	// Route all messages to dialog if one is open.
	if m.dialog.HasDialogs() {
		return m.handleDialogMsg(msg)
	}

	// Tab always toggles focus between editor and chat, even when
	// an inline editor is active. This lets users collapse the
	// question form to view chat.
	if m.activeInline != nil && key.Matches(msg, m.keyMap.Tab) {
		if m.focus == uiFocusEditor {
			m.focus = uiFocusMain
			m.activeInline.SetFocused(false)
			m.chat.Focus()
			m.chat.SetSelected(m.chat.Len() - 1)
		} else {
			m.focus = uiFocusEditor
			m.activeInline.SetFocused(true)
			m.chat.Blur()
		}
		m.updateLayoutAndSize()
		return tea.Batch(cmds...)
	}

	// Route keys to active inline editor if one is showing.
	if m.activeInline != nil && m.focus == uiFocusEditor {
		if done, cmd := m.activeInline.HandleKey(msg); done {
			m.activeInline = nil
			m.textarea.Focus()
			m.updateLayoutAndSize()
		} else {
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			if m.activeInline.HeightChanged() {
				m.updateLayoutAndSize()
			}
		}
		return tea.Batch(cmds...)
	}

	// Handle cancel key when the escape gesture means "stop" rather than
	// "go back".
	if key.Matches(msg, m.keyMap.Chat.Cancel) {
		if m.escapeCancels() {
			if cmd := m.cancelAgent(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return tea.Batch(cmds...)
		}
	}

	switch m.state {
	case uiOnboarding:
		return tea.Batch(cmds...)
	case uiInitialize:
		cmds = append(cmds, m.updateInitializeView(msg)...)
		return tea.Batch(cmds...)
	case uiChat, uiLanding:
		switch m.focus {
		case uiFocusEditor:
			// Handle completions if open.
			if m.completionsOpen {
				if msg, ok := m.completions.Update(msg); ok {
					if _, closed := msg.(completions.ClosedMsg); closed {
						m.completionsOpen = false
					} else if cmd, keepOpen, handled := m.applyCompletionSelection(msg); handled {
						if cmd != nil {
							cmds = append(cmds, cmd)
						}
						if !keepOpen {
							m.closeCompletions()
						}
					}
					return tea.Batch(cmds...)
				}
			}

			if ok := m.attachments.Update(msg); ok {
				return tea.Batch(cmds...)
			}

			switch {
			case key.Matches(msg, m.keyMap.Editor.AddImage):
				if !m.currentModelSupportsImages() {
					break
				}
				if cmd := m.openFilesDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}

			case key.Matches(msg, m.keyMap.Editor.PasteImage):
				if !m.currentModelSupportsImages() {
					break
				}
				cmds = append(cmds, m.pasteImageFromClipboard)

			case key.Matches(msg, m.keyMap.Editor.SendMessage):
				prevHeight := m.textarea.Height()
				value := m.textarea.Value()
				if before, ok := strings.CutSuffix(value, "\\"); ok {
					// If the last character is a backslash, remove it and add a newline.
					m.textarea.SetValue(before)
					if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
						cmds = append(cmds, cmd)
					}
					break
				}

				// Otherwise, send the message
				m.textarea.Reset()
				if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
					cmds = append(cmds, cmd)
				}

				value = strings.TrimSpace(value)
				if value == "exit" || value == "quit" {
					return m.openQuitDialog()
				}

				if m.bangMode && value != "" {
					m.bangMode = false
					m.setEditorPrompt(m.permissionModeCached())
					m.textarea.Placeholder = m.editorPlaceholder()
					m.historyReset()
					if !m.hasSession() {
						return util.ReportWarn("Start a session before running shell commands with !")
					}
					return tea.Batch(m.runShellCommand(value))
				}

				attachments := m.attachments.List()
				m.attachments.Reset()
				if len(value) == 0 && len(attachments) == 0 {
					return nil
				}

				m.historyReset()

				return tea.Batch(m.sendMessage(value, attachments...), m.loadPromptHistory())
			case key.Matches(msg, m.keyMap.Chat.NewSession):
				if !m.hasSession() {
					break
				}
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
					break
				}
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Tab):
				if m.state != uiLanding {
					m.setState(m.state, uiFocusMain)
					m.textarea.Blur()
					m.chat.Focus()
					m.chat.SetSelected(m.chat.Len() - 1)
				}
			case key.Matches(msg, m.keyMap.Editor.OpenEditor):
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is working, please wait..."))
					break
				}
				editorValue := m.textarea.Value()
				if m.bangMode {
					editorValue = "!" + editorValue
				}
				cmds = append(cmds, m.openEditor(editorValue))
			case key.Matches(msg, m.keyMap.Editor.Newline):
				prevHeight := m.textarea.Height()
				m.textarea.InsertRune('\n')
				m.closeCompletions()
				cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
			case key.Matches(msg, m.keyMap.Editor.HistoryPrev):
				cmd := m.handleHistoryUp(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.HistoryNext):
				cmd := m.handleHistoryDown(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.Escape):
				cmd := m.handleHistoryEscape(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.Commands) && m.textarea.Value() == "":
				if cmd := m.openCommandsDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			default:
				if handleGlobalKeys(msg) {
					// Handle global keys first before passing to textarea.
					break
				}

				// Bang mode: backspace on already-empty prompt exits.
				if m.bangMode && m.bangWasEmpty && msg.Code == tea.KeyBackspace {
					m.bangMode = false
					m.bangWasEmpty = false
					m.setEditorPrompt(m.permissionModeCached())
					break
				}

				// Check for a completion trigger before passing to textarea.
				curValue := m.textarea.Value()
				curIdx := len(curValue)

				// Every trigger only fires at the start of a word, so
				// that an address or a comment marker mid-sentence
				// stays text.
				justOpened := false
				if !m.completionsOpen && (curIdx == 0 || isWhitespace(curValue[curIdx-1])) {
					switch msg.String() {
					case "@":
						// An empty popup would swallow Enter without ever
						// inserting anything, stranding the message.
						if agents := m.mentionableAgents(); len(agents) > 0 {
							m.openCompletions("@", curIdx)
							m.completions.SetAgents(agents)
							justOpened = true
						}
					case "#":
						m.openCompletions("#", curIdx)
						depth, limit := m.com.Config().Options.TUI.Completions.Limits()
						cmds = append(cmds, m.completions.Open(depth, limit))
						justOpened = true
					case "&":
						// Same empty-popup guard as "@": nothing to
						// mention means Enter must reach the message.
						if sk := m.mentionableSkills(); len(sk) > 0 {
							m.openCompletions("&", curIdx)
							m.completions.SetSkills(sk)
							justOpened = true
						}
					}
				}

				// remove the details if they are open when user starts typing
				if m.detailsOpen {
					m.detailsOpen = false
					m.updateLayoutAndSize()
				}

				prevHeight := m.textarea.Height()
				cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))

				// Bang mode: enter when "!" is typed at the start of the
				// prompt, optionally preceded by whitespace (either on an
				// empty/whitespace-only prompt or prepended to existing text).
				// Exit on backspace clearing the last character.
				newVal := m.textarea.Value()
				trimmedNew := strings.TrimLeftFunc(newVal, unicode.IsSpace)
				trimmedCur := strings.TrimLeftFunc(curValue, unicode.IsSpace)
				if !m.bangMode && strings.HasPrefix(trimmedNew, "!") && !strings.HasPrefix(trimmedCur, "!") {
					m.bangMode = true
					m.bangWasEmpty = len(strings.TrimSpace(curValue)) == 0
					// Strip leading whitespace and the "!" from the textarea
					// while preserving the cursor position relative to the
					// command text.
					col := m.textarea.Column()
					line := m.textarea.Line()
					stripped := trimmedNew[1:]
					m.textarea.SetValue(stripped)
					m.textarea.SetCursorColumn(max(0, col-(len(newVal)-len(stripped))))
					_ = line // cursor line doesn't change; prefix removed
					m.setEditorPrompt(m.permissionModeCached())
				} else if m.bangMode && newVal == "" && curValue != "" {
					// Just cleared last character; mark empty, stay in bang mode.
					m.bangWasEmpty = true
				} else if m.bangMode && newVal != "" {
					m.bangWasEmpty = false
				}

				// Any text modification becomes the current draft.
				m.updateHistoryDraft(curValue)

				// After updating textarea, check if we need to filter completions.
				// The keystroke that opened the popup carries no query yet.
				if m.completionsOpen && !justOpened {
					newValue := m.textarea.Value()
					newIdx := len(newValue)

					// Close completions if cursor moved before start.
					if newIdx <= m.completionsStartIndex {
						m.closeCompletions()
					} else if msg.String() == "space" {
						// Close on space.
						m.closeCompletions()
					} else {
						// Extract current word and filter.
						word := m.textareaWord()
						if strings.HasPrefix(word, m.completionsTrigger) {
							m.completionsQuery = word[len(m.completionsTrigger):]
							m.completions.Filter(m.completionsQuery)
						} else if m.completionsOpen {
							m.closeCompletions()
						}
					}
				}
			}
		case uiFocusMain:
			switch {
			case key.Matches(msg, m.keyMap.Tab) && !m.viewingSubAgent():
				m.focus = uiFocusEditor
				cmds = append(cmds, m.textarea.Focus())
				m.chat.Blur()
			case key.Matches(msg, m.keyMap.Chat.NewSession):
				if !m.hasSession() {
					break
				}
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
					break
				}
				m.focus = uiFocusEditor
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.OpenSubSession):
				if agentItem, ok := m.selectedAgentTool(); ok {
					cmds = append(cmds, m.enterSubSession(agentItem))
				}
			case key.Matches(msg, m.keyMap.Chat.Expand):
				m.chat.ToggleExpandedSelectedItem()
			case key.Matches(msg, m.keyMap.Chat.Up):
				if cmd := m.chat.ScrollByAndAnimate(-1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectPrev()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			case key.Matches(msg, m.keyMap.Chat.Down):
				if cmd := m.chat.ScrollByAndAnimate(1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.chat.SelectedItemInView() {
					m.chat.SelectNext()
					if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			case key.Matches(msg, m.keyMap.Chat.UpOneItem):
				m.chat.SelectPrev()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.DownOneItem):
				m.chat.SelectNext()
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.HalfPageUp):
				if cmd := m.chat.ScrollByAndAnimate(-m.chat.Height() / 2); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectFirstInView()
			case key.Matches(msg, m.keyMap.Chat.HalfPageDown):
				if cmd := m.chat.ScrollByAndAnimate(m.chat.Height() / 2); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectLastInView()
			case key.Matches(msg, m.keyMap.Chat.PageUp):
				if cmd := m.chat.ScrollByAndAnimate(-m.chat.Height()); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectFirstInView()
			case key.Matches(msg, m.keyMap.Chat.PageDown):
				if cmd := m.chat.ScrollByAndAnimate(m.chat.Height()); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectLastInView()
			case key.Matches(msg, m.keyMap.Chat.Home):
				if cmd := m.chat.ScrollToTopAndSelectFirst(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.End):
				if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			default:
				if ok, cmd := m.chat.HandleKeyMsg(msg); ok {
					cmds = append(cmds, cmd)
				} else {
					handleGlobalKeys(msg)
				}
			}
		default:
			handleGlobalKeys(msg)
		}
	default:
		handleGlobalKeys(msg)
	}

	return tea.Sequence(cmds...)
}

// isAgentBusy returns true if the agent coordinator exists and is currently
// busy processing a request. It only reads the memoized state (it runs in
// per-message paths like the textarea placeholder, where a workspace probe
// would block on every keystroke); the
// value is refreshed off-thread, see workspace_cache.go.
func (m *UI) isAgentBusy() bool {
	if m.bangCancel != nil {
		return true
	}
	return m.agentBusyCache.val
}

// isSessionBusy reports whether the session on screen has a turn running.
// Unlike isAgentBusy it ignores every other session: a parent suspended on
// a branch stays busy for as long as the branch lives, and must not make the
// branch itself read as working once its own turn has stopped. Like
// isAgentBusy it only reads the memoized state.
func (m *UI) isSessionBusy() bool {
	if m.bangCancel != nil {
		return true
	}
	return m.sessionBusyCache.val
}

// hasSession returns true if there is an active session with a valid ID.
func (m *UI) hasSession() bool {
	return m.session != nil && m.session.ID != ""
}

// CurrentSession returns the active session, or nil when there is none.
// It is safe to call after the TUI has exited.
func (m *UI) CurrentSession() *session.Session {
	return m.session
}

// mimeOf detects the MIME type of the given content.
func mimeOf(content []byte) string {
	mimeBufferSize := min(512, len(content))
	return http.DetectContentType(content[:mimeBufferSize])
}

// editorPlaceholder returns the textarea placeholder for the current input
// permissionModeLabel renders a permission.PermissionMode for the toast
// shown when Shift+Tab cycles it, since the wire/config spelling
// ("auto_accept_edits") is not what a user should read.
func permissionModeLabel(mode permission.PermissionMode) string {
	switch mode {
	case permission.ModeYolo:
		return "Yolo"
	case permission.ModeAutoAcceptEdits:
		return "Auto-accept edits"
	default:
		return "Manual"
	}
}

// newSession clears the current session state and prepares for a new session.
// The actual session creation happens when the user sends their first message.
// Returns a command to reload prompt history.
func (m *UI) newSession() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	m.session = nil
	m.sessionFiles = nil
	m.sessionFileReads = nil
	m.sessionStack = nil
	// NOTE: The stale-transcript guard drops a pending load, so end it here.
	m.clearSessionLoading()
	m.setState(uiLanding, uiFocusEditor)
	m.textarea.Focus()
	m.chat.Blur()
	m.chat.ClearMessages()
	m.promptQueue = 0
	m.promptQueueItems = nil
	m.promptQueueCheckedAt = time.Now()
	m.invalidateBusyCaches()
	m.invalidatePromptQueue()
	m.historyReset()
	agenttools.ResetCache()
	return tea.Batch(
		func() tea.Msg {
			m.com.Workspace.LSPStopAll(context.Background())
			return nil
		},
		m.loadPromptHistory(),
		m.reportCurrentSession(""),
	)
}

// detailColumnWeights gives Todos twice the width of the other columns.
var detailColumnWeights = [...]int{2, 1, 1, 1, 1}

// detailColumnWidths splits width across the detail columns, reserving one
// cell of gap between each adjacent pair.
func detailColumnWidths(width int) [len(detailColumnWeights)]int {
	total := 0
	for _, w := range detailColumnWeights {
		total += w
	}

	avail := max(len(detailColumnWeights), width-(len(detailColumnWeights)-1))
	var out [len(detailColumnWeights)]int
	used := 0
	for i, w := range detailColumnWeights {
		out[i] = max(1, avail*w/total)
		used += out[i]
	}
	out[0] += max(0, avail-used) // rounding slack goes to the widest column
	return out
}

// drawSessionDetails draws the session details panel. It carries everything
// that isn't on screen permanently: cwd, model, todos, changed files, and the
// LSP/MCP/skill inventories.
func (m *UI) drawSessionDetails(scr uv.Screen, area uv.Rectangle) {
	if m.session == nil {
		return
	}

	s := m.com.Styles

	width := area.Dx() - s.CompactDetails.View.GetHorizontalFrameSize()
	height := area.Dy() - s.CompactDetails.View.GetVerticalFrameSize()

	detailsHeader := lipgloss.JoinVertical(
		lipgloss.Left,
		s.CompactDetails.Title.Width(width).MaxHeight(2).Render(m.session.Title),
		common.PrettyPath(s, m.com.Workspace.WorkingDir(), width),
		m.modelInfo(width),
		"",
	)

	version := s.CompactDetails.Version.Width(width).AlignHorizontal(lipgloss.Right).Render(version.Version)

	remainingHeight := height - lipgloss.Height(detailsHeader) - lipgloss.Height(version)
	maxItemsPerSection := max(1, remainingHeight-2) // section title and spacing

	cols := detailColumnWidths(width)
	sections := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.todosInfo(cols[0], maxItemsPerSection, false), " ",
		m.filesInfo(m.com.Workspace.WorkingDir(), cols[1], maxItemsPerSection, false), " ",
		m.lspInfo(cols[2], maxItemsPerSection, false), " ",
		m.mcpInfo(cols[3], maxItemsPerSection, false), " ",
		m.skillsInfo(cols[4], maxItemsPerSection, false),
	)

	uv.NewStyledString(
		s.CompactDetails.View.
			Width(area.Dx()).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Left,
					detailsHeader,
					sections,
					version,
				),
			),
	).Draw(scr, area)
}

func (m *UI) runMCPPrompt(clientID, promptID string, arguments map[string]string) tea.Cmd {
	load := func() tea.Msg {
		prompt, err := m.com.Workspace.GetMCPPrompt(clientID, promptID, arguments)
		if err != nil {
			// TODO: make this better
			return util.ReportError(err)()
		}

		if prompt == "" {
			return nil
		}
		return sendMessageMsg{
			Content: prompt,
		}
	}

	var cmds []tea.Cmd
	if cmd := m.dialog.StartLoading(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, load, func() tea.Msg {
		return closeDialogMsg{}
	})

	return tea.Sequence(cmds...)
}

func (m *UI) handleStateChanged() tea.Cmd {
	return m.refreshActiveAgentCmd(func() tea.Msg {
		m.com.Workspace.UpdateAgentModel(context.Background())
		return mcpStateChangedMsg{
			states: m.com.Workspace.MCPGetStates(),
		}
	})
}

func handleMCPPromptsEvent(ws workspace.Workspace, name string) tea.Cmd {
	return func() tea.Msg {
		ws.MCPRefreshPrompts(context.Background(), name)
		return nil
	}
}

func handleMCPToolsEvent(ws workspace.Workspace, name string) tea.Cmd {
	return func() tea.Msg {
		ws.RefreshMCPTools(context.Background(), name)
		return nil
	}
}

func handleMCPResourcesEvent(ws workspace.Workspace, name string) tea.Cmd {
	return func() tea.Msg {
		ws.MCPRefreshResources(context.Background(), name)
		return nil
	}
}

func (m *UI) copyChatHighlight() tea.Cmd {
	text := m.chat.HighlightContent()
	return common.CopyToClipboardWithCallback(
		text,
		"Selected text copied to clipboard",
		func() tea.Msg {
			m.chat.ClearMouse()
			return nil
		},
	)
}

func (m *UI) enableDockerMCP() tea.Msg {
	ctx := context.Background()
	if err := m.com.Workspace.EnableDockerMCP(ctx); err != nil {
		return util.ReportError(err)()
	}

	return util.NewInfoMsg("Docker MCP enabled and started successfully")
}

func (m *UI) disableDockerMCP() tea.Msg {
	if err := m.com.Workspace.DisableDockerMCP(); err != nil {
		return util.ReportError(err)()
	}

	return util.NewInfoMsg("Docker MCP disabled successfully")
}

// sandboxEnteredMsg reports that EnterSandbox succeeded and permissions
// were switched to yolo mode. A dedicated message (rather than reusing
// util.InfoMsg) is needed because, unlike a plain toast, this must also
// write through the sandbox/permission-mode caches on the main Update
// loop.
type sandboxEnteredMsg struct{}

// enterSandbox applies cfg's kernel-level restrictions to the workspace
// process, then switches permissions to yolo: the sandbox already
// enforces the boundaries yolo mode would otherwise skip past, so
// approval prompts stop adding friction without adding safety.
func (m *UI) enterSandbox(cfg sandbox.Config) tea.Msg {
	ctx := context.Background()
	if err := m.com.Workspace.EnterSandbox(ctx, cfg); err != nil {
		return util.ReportError(err)()
	}
	m.com.Workspace.PermissionSetMode(permission.ModeYolo)
	return sandboxEnteredMsg{}
}

// renderLogo renders the Angela letterform wall at the given width.
func renderLogo(t *styles.Styles, width int) string {
	return logo.Render(t.Logo.GradCanvas, version.Version, false, logo.Opts{
		TitleColorA:  t.Logo.TitleColorA,
		TitleColorB:  t.Logo.TitleColorB,
		VersionColor: t.Logo.VersionColor,
		Width:        width,
	})
}
