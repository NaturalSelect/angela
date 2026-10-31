package model

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/clipboard"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/event"
	"github.com/NaturalSelect/angela/internal/fsext"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/lsp"
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
	"github.com/charmbracelet/x/ansi"
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

	// isCanceling tracks whether the user has pressed escape once to cancel.
	isCanceling bool

	// retryStatus is the in-progress provider retry to show on the turn
	// status line, if any. Nil when no retry has been reported for the
	// turn currently running.
	retryStatus *retryStatus

	// activeTool is the running time of the tool call last observed by
	// observeToolCall, used to flag a slow tool or MCP call on the turn
	// status line. Nil until this turn's first tool call is seen.
	activeTool *toolTiming

	// sessionIsBranch memoizes whether the loaded session is a branch.
	// Resolving it reads config through the workspace, which the status
	// line renders too often to afford, so it is settled once per load.
	sessionIsBranch bool

	// bangMode tracks whether the editor is in bang (!) shell mode.
	bangMode     bool
	bangWasEmpty bool // true when bang prompt became empty on last keystroke

	// bangCancel cancels a running bang-mode shell command. Nil when no
	// bang command is in progress. Set by runShellCommand, cleared by
	// shellResultMsg. Checked by isAgentBusy and cancelAgent so that
	// Escape works for bang commands the same way it does for agent runs.
	bangCancel context.CancelFunc

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

	// Completions state
	completions              *completions.Completions
	completionsOpen          bool
	completionsStartIndex    int
	completionsQuery         string
	completionsTrigger       string      // the character that opened the popup
	completionsPositionStart image.Point // x,y where user typed the trigger
	// completionsRect is the screen rectangle the popup was drawn into
	// on the last Draw call, in the same coordinate space as
	// tea.MouseClickMsg. It resets to the zero Rectangle whenever the
	// popup isn't drawn, so a stale click target can never linger past
	// the frame that closed it.
	completionsRect image.Rectangle

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

	// lspStates / lspDiagnostics memoize the workspace LSP state and
	// per-server severity counts (each probe behind them may block, and
	// the sidebar, landing view, and compact header render them every
	// frame). LSP events refresh them
	// off-thread with a TTL backstop; see lsp.go.
	lspStates        map[string]workspace.LSPClientInfo
	lspDiagnostics   map[string]lsp.DiagnosticCounts
	lspFetchInFlight bool
	// lspRefreshQueued records that an LSP event arrived while a fetch was
	// already in flight; applyLSPStates re-dispatches so the freshest state
	// still lands.
	lspRefreshQueued bool
	lspCheckedAt     time.Time

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

	// promptQueue / promptQueueItems mirror the session's queued prompts.
	// They are event-driven with a TTL backstop, fetched off-thread by
	// dispatchPromptQueueRefresh (see workspace_cache.go); promptQueue is
	// always len(promptQueueItems).
	promptQueue          int
	promptQueueItems     []message.QueuedPrompt
	promptQueueCheckedAt time.Time
	promptQueueInFlight  bool
	// promptQueueGen is bumped by every queue state transition; an
	// in-flight fetch captures it at dispatch and its result is discarded
	// if the generation has moved on (see workspace_cache.go).
	promptQueueGen uint64
	// stashedPrompts holds prompts taken back for a session that is no
	// longer on screen, restored when that session is loaded again.
	stashedPrompts map[string][]message.QueuedPrompt
	// agentBusyCache / permissionModeCache memoize the workspace busy and
	// permission probes (which may block). Reads never probe; refreshes
	// happen off-thread (see
	// workspace_cache.go). agentBusyCache covers every session in the
	// workspace; sessionBusyCache only the one on screen.
	agentBusyCache      ttlCache[bool]
	sessionBusyCache    ttlCache[bool]
	permissionModeCache ttlCache[permission.PermissionMode]
	// backgroundAgentsCache is how many background agents are running in
	// the session on screen or any session below it, as the workspace
	// counts them. It is probed with sessionBusyCache and scoped like it.
	backgroundAgentsCache ttlCache[int]
	busyFetchInFlight     bool
	// sandboxActive reports whether the workspace process is currently
	// confined by a sandbox. Landlock restriction is irreversible for the
	// life of the process and a Docker sandbox starts (and stays) active,
	// so this only ever flips false->true; it is seeded once at
	// construction and updated write-through by enterSandbox.
	sandboxActive bool
	// agentReady / agentActive memoize the coordinator readiness and the
	// agent the session runs on (AgentIsReady/AgentActive may block, and
	// modelInfo renders
	// them every frame). Seeded once at construction and refreshed by
	// the same off-thread probe as agentBusyCache.
	agentReady  bool
	agentActive workspace.ActiveAgent
	// agentActiveKnown reports that agentActive came from a probe that
	// resolved. A failed probe leaves it false so the agent reads as
	// unknown rather than as one running no model.
	agentActiveKnown bool
	// agentActiveSession is the session agentActive was probed for. The
	// agent belongs to a session, so the render path compares this
	// against the current session and shows nothing on a mismatch
	// instead of the previous session's agent.
	agentActiveSession string
	// pendingReAuth holds the provider from a re-authentication
	// notification that arrived while the session's agent was still
	// unknown. The notification is published once and never retried,
	// so without this the turn would sit blocked behind an auth
	// dialog that never opened.
	pendingReAuth string
	// busyFetchGen is bumped by every busy/permission state transition;
	// like promptQueueGen it lets a stale in-flight probe result be
	// discarded and re-fetched instead of clobbering newer state.
	busyFetchGen uint64

	// Turn status spinner, animated for as long as the agent is busy.
	turnSpinner    spinner.Model
	turnIsSpinning bool

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
		completions:         comp,
		attachments:         attachments,
		turnSpinner:         turnSpinner,
		lspStates:           make(map[string]workspace.LSPClientInfo),
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
		// Drop a load the user has already navigated away from,
		// otherwise the previous session's transcript lands in the
		// chat that replaced it.
		if msg.sessionID != m.currentSessionID() {
			break
		}
		m.clearSessionLoading()
		m.lastUserMessageTime = msg.lastUserMessageTime
		if cmd := m.applySessionItems(msg.items); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// NOTE: Without warming, the first scroll computes the total height
		// of the whole transcript in one blocking pass to size the
		// scrollbar. The layout runs first so the list takes the
		// scrollbar-adjusted width now; otherwise the layout pass at warm
		// completion changes the width and drops everything just warmed.
		m.updateLayoutAndSize()
		cmds = append(cmds, m.chat.BeginWarm())
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
		if msg.sessionID != m.currentSessionID() {
			// NOTE: The messages are already deleted, so hold the text and
			// files until that session is shown again.
			if msg.result.PoppedText != "" || len(msg.result.PoppedAttachments) > 0 {
				m.stashRestoredPrompts(msg.sessionID, []message.QueuedPrompt{{
					Prompt:      msg.result.PoppedText,
					Attachments: msg.result.PoppedAttachments,
				}})
			}
		} else {
			m.prependAttachments(msg.result.PoppedAttachments)
			if msg.result.PoppedText != "" {
				if cmd := m.prependToEditor(msg.result.PoppedText); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		switch {
		case len(msg.result.Skipped) > 0:
			cmds = append(cmds, util.ReportWarn(fmt.Sprintf("Undid last turn, but skipped %d file(s) modified outside the session", len(msg.result.Skipped))))
		case len(msg.result.Reverted) > 0 || len(msg.result.Deleted) > 0:
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf("Undid last turn: %d file(s) reverted, %d deleted", len(msg.result.Reverted), len(msg.result.Deleted))))
		default:
			cmds = append(cmds, util.ReportInfo("Undid last turn"))
		}
	case sideQuestionAnsweredMsg:
		completed := false
		if item := m.chat.MessageItem(msg.pendingID); item != nil {
			if sq, ok := item.(*chat.SideQuestionItem); ok {
				if msg.err != nil {
					sq.Fail(msg.err)
				} else {
					sq.Complete(msg.answer)
				}
				if m.chat.Follow() {
					if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
				completed = true
			}
		}
		if !completed {
			if msg.err != nil {
				cmds = append(cmds, util.ReportError(msg.err))
				break
			}
			if m.hasSession() && m.session.ID == msg.sessionID {
				item := chat.NewPendingSideQuestionItem(m.com.Styles, msg.question)
				item.Complete(msg.answer)
				m.chat.AppendMessages(item)
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
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
		// Sub-session navigation: push/pop deferred to here so a
		// failed load (which sends ReportError, not loadSessionMsg)
		// never touches the stack.
		if msg.enterFrame != nil {
			m.sessionStack = append(m.sessionStack, *msg.enterFrame)
		}
		if msg.leaveLevel && len(m.sessionStack) > 0 {
			m.sessionStack = m.sessionStack[:len(m.sessionStack)-1]
		}
		if msg.truncateStackTo != nil && *msg.truncateStackTo <= len(m.sessionStack) {
			m.sessionStack = m.sessionStack[:*msg.truncateStackTo]
		}
		if msg.clearStack {
			m.sessionStack = nil
		}
		if msg.draftAfter != nil {
			m.applyDraft(*msg.draftAfter)
		}
		if m.forceCompactMode {
			m.isCompact = true
		}
		focus := m.focus
		isBranch := msg.isBranch
		if msg.session.ParentSessionID != "" && !isBranch {
			// The editor is closed off down here, so leaving focus in it
			// would strand the user in a box that cannot take input. A
			// branch keeps its editor, so it keeps the focus too.
			focus = uiFocusMain
			m.textarea.Blur()
			m.chat.Focus()
		}
		m.setState(uiChat, focus)
		switched := !m.hasSession() || m.session.ID != msg.session.ID
		m.session = msg.session
		m.sessionIsBranch = isBranch
		m.sessionFiles = msg.files
		// Session switch: the memoized busy state and queued prompts
		// belong to the previous session. Drop them and re-fetch
		// off-thread so the queue pill and esc behavior track the new
		// session instead of a stale one.
		m.invalidateBusyCaches()
		if switched {
			// NOTE: Invalidating keeps the value, and the probe for the new
			// session may be a cycle away. Until it lands the old session's
			// answer would gate the new transcript's animations.
			m.sessionBusyCache = ttlCache[bool]{}
			m.backgroundAgentsCache = ttlCache[int]{}
			// NOTE: The previous transcript would otherwise stay on screen,
			// and be laid out at the new size, until the new one arrives.
			m.chat.ClearMessages()
		}
		m.invalidatePromptQueue()
		m.promptQueue = 0
		m.promptQueueItems = nil
		m.promptQueueCheckedAt = time.Time{}
		if cmd := m.restoreStashedPrompts(msg.session.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.startLSPs(msg.lspFilePaths()))
		cmds = append(cmds, m.loadSessionMessagesCmd(m.session.ID))
		if cmd := m.syncTurnSpinner(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Reload prompt history for the new session.
		m.historyReset()
		cmds = append(cmds, m.loadPromptHistory())
		m.updateLayoutAndSize()

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
		// Check if this is a child session message for an agent tool.
		if m.session == nil {
			break
		}
		if msg.Payload.SessionID != m.session.ID {
			// This might be a child session message from an agent tool.
			if cmd := m.handleChildSessionMessage(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			break
		}
		switch msg.Type {
		case pubsub.CreatedEvent:
			cmds = append(cmds, m.appendSessionMessage(msg.Payload))
			// A new message is a run boundary — a user prompt starting
			// a turn or the agent replying/dequeueing. Drop the
			// memoized busy state and re-fetch it and the queue
			// off-thread. Per-chunk UpdatedEvents deliberately do NOT
			// trigger this: during streaming that would put workspace
			// probes on every token.
			m.invalidateBusyCaches()
			m.invalidatePromptQueue()
			if cmd := m.dispatchBusyRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case pubsub.UpdatedEvent:
			cmds = append(cmds, m.updateSessionMessage(msg.Payload))
		case pubsub.DeletedEvent:
			m.chat.RemoveMessageWithToolCalls(msg.Payload.ID)
		}
		// Follow the turn: spin while the agent works, stop when it stops.
		if cmd := m.syncTurnSpinner(); cmd != nil {
			cmds = append(cmds, cmd)
		}
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
		if item := m.chat.MessageItem(msg.PendingID); item != nil {
			if shellItem, ok := item.(*chat.ShellItem); ok {
				shellItem.AppendOutput(msg.Chunk)
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		// Continue draining the stream channel.
		if msg.streamCh != nil {
			ch := msg.streamCh
			pid := msg.PendingID
			cmds = append(cmds, func() tea.Msg {
				chunk, ok := <-ch
				if !ok {
					return nil
				}
				return shellStreamMsg{PendingID: pid, Chunk: chunk, streamCh: ch}
			})
		}
	case shellResultMsg:
		// Clear the bang cancel func — command is done.
		if m.bangCancel != nil {
			m.bangCancel()
			m.bangCancel = nil
		}
		// Complete the pending shell item if it exists, otherwise create a new one.
		completed := false
		if msg.PendingID != "" {
			if item := m.chat.MessageItem(msg.PendingID); item != nil {
				if shellItem, ok := item.(*chat.ShellItem); ok {
					shellItem.Complete(msg.Output, msg.ExitCode)
					if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
					completed = true
				}
			}
		}
		if !completed {
			item := chat.NewShellItem(m.com.Styles, msg.Command, msg.Output, msg.ExitCode)
			m.chat.AppendMessages(item)
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		cmds = append(cmds, m.loadPromptHistory())
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
		if msg.Type == util.InfoTypeError {
			slog.Error("Error reported", "error", msg.Msg)
		}
		if cmd := m.status.SetInfoMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// An animated message marks an action still in progress, of
		// unknown duration: it must stay up until replaced by that
		// action's outcome, not vanish on a fixed timer while the
		// action itself may still be running (this is what let a
		// slow commit's "still working" toast disappear before git
		// actually finished).
		if !msg.Animated {
			ttl := msg.TTL
			if ttl <= 0 {
				ttl = DefaultStatusTTL
			}
			cmds = append(cmds, clearInfoMsgCmd(ttl))
		}
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

	isOnboarding := m.state == uiOnboarding

	switch msg := action.(type) {
	// Generic dialog messages
	case dialog.ActionClose:
		if isOnboarding {
			if cmd := m.closeOnboardingDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			break
		}

		if m.dialog.ContainsDialog(dialog.FilePickerID) {
			defer fimage.ResetCache()
		}

		m.dialog.CloseFrontDialog()

		if m.focus == uiFocusEditor {
			cmds = append(cmds, m.textarea.Focus())
		}
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
		// Session-scoped, not the busy cache: that cache answers for the
		// whole process, so another session's run would block this one's
		// compact even while it sits idle.
		if m.com.Workspace.AgentIsSessionBusy(msg.SessionID) {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, func() tea.Msg {
			err := m.com.Workspace.AgentSummarize(context.Background(), msg.SessionID)
			// NOTE: A cancelled compact is the user's choice, not a failure.
			if err != nil && !errors.Is(err, context.Canceled) {
				// Routed through agentRunFailedMsg (rather than a plain
				// ReportError) so restoreQueueOnAgentError also fires and
				// hands back whatever the user had queued behind this
				// compact.
				return agentRunFailedMsg{sessionID: msg.SessionID, err: err}
			}
			return nil
		})
		m.dialog.CloseDialog(dialog.CommandsID)
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
		// Session-scoped, matching ActionSummarize: committing while a
		// turn is running risks capturing a half-finished edit.
		// NOTE: With no session yet (empty ID) there is no turn to wait on.
		if msg.SessionID != "" && m.com.Workspace.AgentIsSessionBusy(msg.SessionID) {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before committing..."))
			break
		}
		cmds = append(cmds, m.commitStagedChanges(msg.SessionID))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionExportSession:
		m.dialog.CloseDialog(dialog.CommandsID)
		sessionID := msg.SessionID
		cmds = append(cmds, func() tea.Msg {
			ctx := context.Background()
			sess, err := m.com.Workspace.GetSession(ctx, sessionID)
			if err != nil {
				return util.ReportError(err)()
			}
			sessionMessages, err := m.com.Workspace.ListMessages(ctx, sessionID)
			if err != nil {
				return util.ReportError(err)()
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return util.ReportError(err)()
			}

			outPath, err := chat.UniqueExportPath(filepath.Join(workingDir, chat.SessionExportSlug(sess.Title, sess.ID)+".md"))
			if err != nil {
				return util.ReportError(err)()
			}

			doc, err := chat.SessionToMarkdown(sess, sessionMessages, chat.AssetsDirName(outPath))
			if err != nil {
				return util.ReportError(err)()
			}
			if err := os.WriteFile(outPath, []byte(doc), 0o644); err != nil {
				return util.ReportError(err)()
			}

			return util.ReportInfo("Exported session to " + outPath)()
		})
	case dialog.ActionExportImage:
		if strings.TrimSpace(msg.ImageID) == "" {
			m.dialog.CloseDialog(dialog.CommandsID)
			if m.session == nil {
				cmds = append(cmds, util.ReportWarn("Open a session before exporting an image."))
				break
			}
			sessionID := m.session.ID
			cmds = append(cmds, func() tea.Msg {
				ctx := context.Background()
				imgs, err := m.com.Workspace.ListSessionImages(ctx, sessionID)
				if err != nil {
					return util.ReportError(err)()
				}
				if len(imgs) == 0 {
					return util.ReportWarn("No generated images in this session")()
				}

				workingDir, err := os.Getwd()
				if err != nil {
					return util.ReportError(err)()
				}

				return exportImagesLoadedMsg{sessionID: sessionID, images: imgs, workingDir: workingDir}
			})
			break
		}
		m.dialog.CloseDialog(dialog.ArgumentsID)
		imageID := msg.ImageID
		output := msg.Output
		cmds = append(cmds, func() tea.Msg {
			ctx := context.Background()
			img, err := m.com.Workspace.GetGeneratedImage(ctx, imageID)
			if err != nil {
				return util.ReportError(err)()
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return util.ReportError(err)()
			}

			target := chat.ResolveExportTarget(output, imageID+chat.ExtensionForMIME(img.MIMEType), workingDir)

			outPath, err := chat.UniqueExportPath(target)
			if err != nil {
				return util.ReportError(err)()
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return util.ReportError(err)()
			}
			if err := os.WriteFile(outPath, img.Data, 0o644); err != nil {
				return util.ReportError(err)()
			}

			return util.ReportInfo("Exported image to " + outPath)()
		})
	case dialog.ActionSelectExportImage:
		m.dialog.CloseDialog(dialog.ImagesID)
		defaultPath := filepath.Join(msg.WorkingDir, msg.Image.ID+chat.ExtensionForMIME(msg.Image.MIMEType))
		description := "Writes the generated image's full-size original to a file on disk."
		if prompt := strings.Join(strings.Fields(msg.Image.Prompt), " "); prompt != "" {
			description = fmt.Sprintf("Output path for %q.", ansi.Truncate(prompt, 60, "…"))
		}
		argsDialog := dialog.NewArguments(
			m.com,
			"Export Image",
			description,
			[]commands.Argument{{ID: "OUTPUT", Title: "Output Path", Required: true}},
			dialog.ActionExportImage{ImageID: msg.Image.ID},
		)
		argsDialog.SetValue("OUTPUT", defaultPath)
		m.dialog.OpenDialog(argsDialog)
	case dialog.ActionExportReport:
		if msg.Report.ID == "" {
			m.dialog.CloseDialog(dialog.CommandsID)
			if m.session == nil {
				cmds = append(cmds, util.ReportWarn("Open a session before exporting a report."))
				break
			}
			sessionID := m.session.ID
			cmds = append(cmds, func() tea.Msg {
				msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
				if err != nil {
					return util.ReportError(err)()
				}
				reports := agenttools.ReportsFromMessages(m.com.Workspace, msgs)
				if len(reports) == 0 {
					return util.ReportWarn("No agent reports in this session")()
				}

				workingDir, err := os.Getwd()
				if err != nil {
					return util.ReportError(err)()
				}

				return exportReportsLoadedMsg{sessionID: sessionID, reports: reports, workingDir: workingDir}
			})
			break
		}
		m.dialog.CloseDialog(dialog.ArgumentsID)
		report := msg.Report
		output := msg.Output
		cmds = append(cmds, func() tea.Msg {
			workingDir, err := os.Getwd()
			if err != nil {
				return util.ReportError(err)()
			}

			target := chat.ResolveExportTarget(output, report.ID+".md", workingDir)

			outPath, err := chat.UniqueExportPath(target)
			if err != nil {
				return util.ReportError(err)()
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return util.ReportError(err)()
			}
			if err := os.WriteFile(outPath, []byte(chat.ReportToMarkdown(report)), 0o644); err != nil {
				return util.ReportError(err)()
			}

			return util.ReportInfo("Exported report to " + outPath)()
		})
	case dialog.ActionSelectExportReport:
		m.dialog.CloseDialog(dialog.ReportsID)
		defaultPath := filepath.Join(msg.WorkingDir, msg.Report.ID+".md")
		description := "Writes the agent report to a Markdown file on disk."
		if task := strings.Join(strings.Fields(msg.Report.Task), " "); task != "" {
			description = fmt.Sprintf("Output path for %q.", ansi.Truncate(task, 60, "…"))
		}
		argsDialog := dialog.NewArguments(
			m.com,
			"Export Report",
			description,
			[]commands.Argument{{ID: "OUTPUT", Title: "Output Path", Required: true}},
			dialog.ActionExportReport{Report: msg.Report},
		)
		argsDialog.SetValue("OUTPUT", defaultPath)
		m.dialog.OpenDialog(argsDialog)
	case dialog.ActionImportReport:
		if msg.Path == "" {
			m.dialog.CloseDialog(dialog.CommandsID)
			if m.session == nil {
				cmds = append(cmds, util.ReportWarn("Open a session before importing a report."))
				break
			}
			m.dialog.OpenDialog(dialog.NewArguments(
				m.com,
				"Import Report",
				"Reads an agent report from a file into this session so the agent can load it with LoadReport.",
				[]commands.Argument{{
					ID:          "PATH",
					Title:       "Report File",
					Description: "Path to an exported report file (absolute or relative)",
					Required:    true,
				}},
				dialog.ActionImportReport{},
			))
			break
		}
		m.dialog.CloseDialog(dialog.ArgumentsID)
		if m.session == nil {
			cmds = append(cmds, util.ReportWarn("Open a session before importing a report."))
			break
		}
		sessionID := m.session.ID
		// Session-scoped, like ActionUndo: the busy cache answers for the
		// whole process.
		if m.com.Workspace.AgentIsSessionBusy(sessionID) {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before importing a report..."))
			break
		}
		path := msg.Path
		cmds = append(cmds, func() tea.Msg {
			report, err := readReportFile(path)
			if err != nil {
				return util.ReportError(err)()
			}
			result, err := m.com.Workspace.ImportReport(context.Background(), sessionID, report)
			if err != nil {
				if errors.Is(err, agenttools.ErrEmptyReport) {
					return util.ReportWarn("Report file is empty")()
				}
				if errors.Is(err, agenttools.ErrReportTooLarge) {
					return util.ReportWarn(err.Error())()
				}
				if errors.Is(err, agent.ErrSessionBusy) {
					return util.ReportWarn("Agent is busy, please wait before importing a report...")()
				}
				return util.ReportError(err)()
			}
			return util.ReportInfo(importReportSummary(result))()
		})
	case dialog.ActionExportProfile:
		if msg.Profile.Name == "" {
			m.dialog.CloseDialog(dialog.CommandsID)
			cmds = append(cmds, func() tea.Msg {
				// Profiles describe this process, so they are written
				// through its own filesystem, never the workspace's.
				workingDir, err := os.Getwd()
				if err != nil {
					return util.ReportError(err)()
				}
				return exportProfilesLoadedMsg{workingDir: workingDir}
			})
			break
		}
		m.dialog.CloseDialog(dialog.ArgumentsID)
		cmds = append(cmds, exportProfile(msg))
	case dialog.ActionSelectExportProfile:
		m.dialog.CloseDialog(dialog.ProfilesID)
		profile := msg.Profile
		defaultName := profile.FileName(os.Getpid(), time.Now(), profile.Extension())
		arguments := []commands.Argument{{ID: "OUTPUT", Title: "Output Path", Required: true}}
		description := "Writes a snapshot of this process to a file for `go tool pprof`."
		if profile.Timed {
			arguments = append(arguments, commands.Argument{ID: "SECONDS", Title: "Seconds to Record", Required: true})
			description = "Records this process for a while, then writes the result to a file."
		}
		argsDialog := dialog.NewArguments(
			m.com,
			"Export Profile: "+profile.Name,
			description,
			arguments,
			dialog.ActionExportProfile{Profile: profile},
		)
		argsDialog.SetValue("OUTPUT", filepath.Join(msg.WorkingDir, defaultName))
		if profile.Timed {
			argsDialog.SetValue("SECONDS", strconv.Itoa(profiling.DefaultSeconds))
		}
		m.dialog.OpenDialog(argsDialog)
	case dialog.ActionUndo:
		// Session-scoped, matching ActionSummarize: the busy cache
		// answers for the whole process and would block this session's
		// undo even while it sits idle.
		if m.com.Workspace.AgentIsSessionBusy(msg.SessionID) {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before undoing..."))
			break
		}
		m.dialog.CloseDialog(dialog.CommandsID)
		sessionID := msg.SessionID
		cmds = append(cmds, func() tea.Msg {
			preview, err := m.com.Workspace.PreviewUndo(context.Background(), sessionID)
			if err != nil {
				if errors.Is(err, undo.ErrNothingToUndo) {
					return util.ReportInfo("Nothing to undo")()
				}
				if errors.Is(err, undo.ErrSessionBusy) {
					return util.ReportWarn("Agent is busy, please wait before undoing...")()
				}
				return util.ReportError(err)()
			}
			return undoPreviewMsg{sessionID: sessionID, preview: preview}
		})
	case dialog.ActionUndoConfirmed:
		m.dialog.CloseDialog(dialog.UndoID)
		if m.com.Workspace.AgentIsSessionBusy(msg.SessionID) {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before undoing..."))
			break
		}
		sessionID := msg.SessionID
		cutMessageID := msg.CutMessageID
		cmds = append(cmds, func() tea.Msg {
			result, err := m.com.Workspace.Undo(context.Background(), sessionID, cutMessageID)
			if err != nil {
				if errors.Is(err, undo.ErrSessionBusy) {
					return util.ReportWarn("Agent is busy, please wait before undoing...")()
				}
				return util.ReportError(err)()
			}
			return undoResultMsg{sessionID: sessionID, result: result}
		})
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
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is working, please wait..."))
			break
		}
		editorValue := m.textarea.Value()
		if m.bangMode {
			editorValue = "!" + editorValue
		}
		cmds = append(cmds, m.openEditor(editorValue))
		m.dialog.CloseDialog(dialog.CommandsID)
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
			break
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
	case dialog.ActionAttachSkill:
		m.dialog.CloseFrontDialog()
		cmds = append(cmds, m.attachSkill(msg.ID, msg.Name))
	case dialog.ActionAskSideQuestion:
		if strings.TrimSpace(msg.Question) == "" {
			m.dialog.CloseFrontDialog()
			argsDialog := dialog.NewArguments(
				m.com,
				"Side Question",
				"Answered from the current conversation only — no tools, and it never enters the session history.",
				[]commands.Argument{{ID: "QUESTION", Title: "Question", Required: true}},
				msg, // Pass the action as the result
			)
			m.dialog.OpenDialog(argsDialog)
			break
		}
		cmds = append(cmds, m.askSideQuestion(msg.SessionID, msg.Question))
		m.dialog.CloseFrontDialog()
	case dialog.ActionRunMCPPrompt:
		if len(msg.Arguments) > 0 && msg.Args == nil {
			m.dialog.CloseFrontDialog()
			title := cmp.Or(msg.Title, "MCP Prompt Arguments")
			argsDialog := dialog.NewArguments(
				m.com,
				title,
				msg.Description,
				msg.Arguments,
				msg, // Pass the action as the result
			)
			m.dialog.OpenDialog(argsDialog)
			break
		}
		cmds = append(cmds, m.runMCPPrompt(msg.ClientID, msg.PromptID, msg.Args))
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

// sendMessage sends a message with the given content and attachments.
func (m *UI) sendMessage(content string, attachments ...message.Attachment) tea.Cmd {
	if m.viewingSubAgent() {
		return util.ReportWarn("This is a sub-agent's transcript. Press ctrl+up to go back and reply there.")
	}
	if err := m.com.Workspace.AgentReadyErr(); err != nil {
		return util.ReportError(err)
	}

	// Start the turn timer.
	common.StartTurn()

	// Sending is an explicit "I am here now": drop whatever scrollback the
	// user was reading and follow the reply as it streams in.
	m.chat.ScrollToBottom()

	var cmds []tea.Cmd
	if !m.hasSession() {
		newSession, err := m.com.Workspace.CreateSession(context.Background(), "New Session")
		if err != nil {
			return util.ReportError(err)
		}
		if m.forceCompactMode {
			m.isCompact = true
		}
		if newSession.ID != "" {
			m.session = &newSession
			cmds = append(cmds, m.loadSession(newSession.ID))
		}
		m.setState(uiChat, m.focus)
	}

	ctx := context.Background()
	cmds = append(cmds, func() tea.Msg {
		for _, path := range m.sessionFileReads {
			m.com.Workspace.FileTrackerRecordRead(ctx, m.session.ID, path)
			m.com.Workspace.LSPStart(ctx, path)
		}
		return nil
	})

	// Capture session ID to avoid race with main goroutine updating m.session.
	sessionID := m.session.ID
	// Optimistically mark the agent busy: the prompt we are about to submit
	// either starts a run or is enqueued behind one. This keeps esc pressed
	// right after enter routing to cancelAgent instead of reading a stale
	// idle value; the authoritative state arrives via agentRunSubmittedMsg.
	// Bump the busy/queue generations so any probe started before this
	// optimistic write is discarded rather than reverting us to idle.
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)
	m.busyFetchGen++
	m.invalidatePromptQueue()
	cmds = append(cmds, func() tea.Msg {
		// AgentRun blocks for the whole turn and reports a terminal
		// failure right here. A failure outside a turn this UI started
		// arrives as a TypeAgentError notification
		// (handleAgentNotification). Either way a terminal failure must
		// restore whatever this session still has queued behind it, so
		// both paths funnel through restoreQueueOnAgentError.
		err := m.com.Workspace.AgentRun(context.Background(), sessionID, content, attachments...)
		if err != nil && !errors.Is(err, context.Canceled) {
			return agentRunFailedMsg{sessionID: sessionID, err: err}
		}
		return agentRunSubmittedMsg{}
	})
	return tea.Batch(cmds...)
}

// runShellCommand executes a shell command without triggering the LLM. The
// result is displayed as a tool-style item in the chat.
// Callers must ensure a session already exists.
func (m *UI) runShellCommand(command string) tea.Cmd {
	var cmds []tea.Cmd

	sessionID := m.session.ID
	contentWidth := min(m.layout.main.Dx()-2, 120)

	// Append a pending shell item immediately so the user sees feedback.
	pendingItem := chat.NewPendingShellItem(m.com.Styles, command)
	m.chat.AppendMessages(pendingItem)
	if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := pendingItem.StartAnimation(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Stream output via channel. The progress callback writes chunks
	// to streamCh; a reader cmd converts them to shellStreamMsg values.
	streamCh := make(chan string, 64)
	pendingID := pendingItem.ID()

	onProgress := func(chunk string) {
		select {
		case streamCh <- chunk:
		default:
			// Drop if UI can't keep up.
		}
	}

	// Reader cmd: drains streamCh into shellStreamMsg until closed.
	cmds = append(cmds, func() tea.Msg {
		chunk, ok := <-streamCh
		if !ok {
			return nil
		}
		return shellStreamMsg{PendingID: pendingID, Chunk: chunk, streamCh: streamCh}
	})

	ctx, cancel := context.WithCancel(context.Background())
	m.bangCancel = cancel

	cmds = append(cmds, func() tea.Msg {
		resp, err := m.com.Workspace.AgentRunShellCommand(ctx, sessionID, command, contentWidth, onProgress, false)
		close(streamCh)
		if err != nil && !errors.Is(err, context.Canceled) {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("shell: %v", err),
			}
		}
		exitCode := resp.ExitCode
		if errors.Is(err, context.Canceled) {
			exitCode = 130 // conventional SIGINT exit code
		}
		return shellResultMsg{
			PendingID: pendingID,
			Command:   command,
			Output:    resp.Output,
			ExitCode:  exitCode,
		}
	})
	return tea.Batch(cmds...)
}

const cancelTimerDuration = 2 * time.Second

// cancelTimerCmd creates a command that expires the cancel timer.
func cancelTimerCmd() tea.Cmd {
	return tea.Tick(cancelTimerDuration, func(time.Time) tea.Msg {
		return cancelTimerExpiredMsg{}
	})
}

// cancelAgent handles the cancel key press. The first press sets isCanceling to true
// and starts a timer. The second press (before the timer expires) actually
// cancels the agent.
func (m *UI) cancelAgent() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	// Gate on the memoized ready state: esc is a hot key and AgentIsReady
	// may block.
	if !m.agentReady {
		return nil
	}

	if m.isCanceling {
		// Second escape press — actually cancel.
		m.isCanceling = false

		// Cancel a running bang command if one is in progress.
		if m.bangCancel != nil {
			m.bangCancel()
			m.bangCancel = nil
		}

		// NOTE: AgentCancel drops the backend queue, so the take must run
		// before it, in the same command; the busy refresh waits for the
		// result so its probe cannot race the cancel.
		m.stopTurnSpinner()
		return m.takeQueuedPromptsToEditorCmd(m.session.ID, true)
	}

	// Queued prompts pending: esc pops them back into the editor instead
	// of discarding them. Decide from the cached count (event-driven)
	// instead of a synchronous workspace probe.
	if m.promptQueue > 0 {
		return m.takeQueuedPromptsToEditorCmd(m.session.ID, false)
	}

	// First escape press - set canceling state and start timer.
	m.isCanceling = true
	return cancelTimerCmd()
}

// takeQueuedPromptsToEditorCmd takes every user-queued prompt behind
// sessionID's turn off the Update goroutine and, when cancel is set,
// cancels the turn right after. The result lands as queuedPromptsTakenMsg.
func (m *UI) takeQueuedPromptsToEditorCmd(sessionID string, cancel bool) tea.Cmd {
	ws := m.com.Workspace
	localCopy := m.promptQueueItems
	return func() tea.Msg {
		msg := queuedPromptsTakenMsg{sessionID: sessionID, cancelled: cancel}
		prompts, err := ws.AgentTakeQueuedPrompts(sessionID)
		if err != nil {
			// NOTE: AgentCancel drops the backend queue, so fall back to
			// the local copy.
			slog.Warn("Failed to take queued prompts; using the local copy",
				"session", sessionID, "error", err)
			prompts, msg.usedLocalCopy = localCopy, true
			if len(localCopy) > 0 {
				ws.AgentClearQueue(sessionID)
			}
		}
		msg.prompts = prompts
		if cancel {
			ws.AgentCancel(sessionID)
		}
		return msg
	}
}

func (m *UI) restoreQueuedPrompts(items []message.QueuedPrompt) tea.Cmd {
	// The mirror can be stale (e.g. restoreQueueOnAgentError takes even
	// when it reads 0, since a refresh may not have landed yet), so
	// whether there is anything to sync visually is decided from
	// whichever of the mirror or the take result had something, not
	// from the take result alone.
	hadMirror := m.promptQueue > 0 || len(m.promptQueueItems) > 0
	m.promptQueue = 0
	m.promptQueueItems = nil
	m.promptQueueCheckedAt = time.Now()

	if hadMirror || len(items) > 0 {
		// Bump the queue generation so a fetch started before this take
		// cannot land and repopulate the pill we just emptied.
		m.invalidatePromptQueue()
		m.syncQueuedChatItems()
		m.updateLayoutAndSize()
	}

	if len(items) == 0 {
		return nil
	}

	texts := make([]string, len(items))
	var atts []message.Attachment
	for i, qp := range items {
		texts[i] = qp.Prompt
		atts = append(atts, qp.Attachments...)
	}
	m.prependAttachments(atts)

	// NOTE: A prompt that held only files has no text, and prepending an
	// empty string would still push the draft down by a blank line.
	text := strings.Join(texts, "\n\n")
	if text == "" {
		return nil
	}
	return m.prependToEditor(text)
}

// restoreQueueOnAgentError pops any prompts still queued behind
// sessionID's turn back into the editor when that turn ends in an
// error instead of finishing normally. A failed turn never drains its
// own queue (see drainQueueForStep), so without this the prompts would
// sit stranded on the backend — indistinguishable from ones an idle
// agent simply has not gotten to yet — until the user noticed and
// pressed esc. Scoped to the session currently on screen: an error on
// a session the user has since navigated away from must not reach
// into the visible editor. The take itself runs off the Update goroutine
// and is applied when queuedPromptsTakenMsg lands.
func (m *UI) restoreQueueOnAgentError(sessionID string) tea.Cmd {
	if sessionID == "" || sessionID != m.currentSessionID() {
		return nil
	}
	return m.takeQueuedPromptsCmd(sessionID)
}

func (m *UI) takeQueuedPromptsCmd(sessionID string) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		prompts, err := ws.AgentTakeQueuedPrompts(sessionID)
		return queuedPromptsTakenMsg{sessionID: sessionID, prompts: prompts, err: err}
	}
}

func (m *UI) applyQueuedPromptsTaken(msg queuedPromptsTakenMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.cancelled {
		// NOTE: A cancelled turn publishes no terminal notification, so
		// the retry banner would otherwise outlive it and show on the
		// next turn until its backoff delay elapses.
		m.clearRetryStatus(msg.sessionID)
		m.invalidateBusyCaches()
		cmds = append(cmds, m.dispatchBusyRefresh())
	}

	switch {
	case msg.err != nil:
		// NOTE: The queue is still on the backend, so leave the mirror alone.
		slog.Warn("Failed to take queued prompts after an agent error",
			"session", msg.sessionID, "error", msg.err)
		cmds = append(cmds, util.ReportWarn("Could not recover queued prompts; they are still queued."))
	case msg.sessionID != m.currentSessionID():
		// NOTE: The prompts already left the backend queue, so hold them
		// until that session is shown again.
		m.stashRestoredPrompts(msg.sessionID, msg.prompts)
	default:
		if msg.usedLocalCopy && len(msg.prompts) > 0 {
			cmds = append(cmds, util.ReportWarn("Could not take queued prompts; restored the local copy (attachments may be missing)."))
		}
		cmds = append(cmds, m.restoreQueuedPrompts(msg.prompts))
	}
	return tea.Batch(cmds...)
}

func (m *UI) stashRestoredPrompts(sessionID string, prompts []message.QueuedPrompt) {
	if len(prompts) == 0 {
		return
	}
	if m.stashedPrompts == nil {
		m.stashedPrompts = make(map[string][]message.QueuedPrompt)
	}
	m.stashedPrompts[sessionID] = append(m.stashedPrompts[sessionID], prompts...)
}

func (m *UI) restoreStashedPrompts(sessionID string) tea.Cmd {
	prompts, ok := m.stashedPrompts[sessionID]
	if !ok {
		return nil
	}
	delete(m.stashedPrompts, sessionID)
	return m.restoreQueuedPrompts(prompts)
}

// prependAttachments places atts ahead of the attachments already on the
// draft, mirroring how prependToEditor treats text.
func (m *UI) prependAttachments(atts []message.Attachment) {
	if len(atts) == 0 {
		return
	}
	draft := m.attachments.List()
	m.attachments.Reset()
	for _, a := range atts {
		m.attachments.Update(a)
	}
	for _, a := range draft {
		m.attachments.Update(a)
	}
}

// prependToEditor places text ahead of whatever draft is being composed,
// so a half-typed follow-up is never clobbered.
func (m *UI) prependToEditor(text string) tea.Cmd {
	prevHeight := m.textarea.Height()
	restored := text
	if draft := m.textarea.Value(); draft != "" {
		restored += "\n\n" + draft
	}
	m.textarea.SetValue(restored)
	m.textarea.MoveToEnd()
	m.syncBangModeFromTextarea()
	return m.updateTextareaWithPrevHeight(nil, prevHeight)
}

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

// handlePermissionNotification updates tool items when permission state changes.
func (m *UI) handlePermissionNotification(notification permission.PermissionNotification) tea.Cmd {
	if toolItem := m.chat.MessageItem(notification.ToolCallID); toolItem != nil {
		if permItem, ok := toolItem.(chat.ToolMessageItem); ok {
			if notification.Granted {
				permItem.SetStatus(chat.ToolStatusRunning)
			} else {
				permItem.SetStatus(chat.ToolStatusAwaitingPermission)
			}
		}
	}

	// If this notification reflects a final resolution (granted or denied),
	// dismiss any open permissions dialog whose tool call ID matches. This
	// covers the case where another client resolved the request remotely,
	// and also drops it from the pending queue if it was still sitting
	// there unseen (e.g. a sibling sub-agent swept by GrantPersistent) so
	// it never surfaces later as a stale, already-resolved prompt.
	if !notification.Granted && !notification.Denied {
		return nil
	}
	m.removeQueuedPermission(notification.ToolCallID)
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.ToolCallID() == notification.ToolCallID {
			m.dialog.CloseDialog(dialog.PermissionsID)
			return m.nextPermissionRequest()
		}
	}
	return nil
}

// removeQueuedPermission drops a request from the pending queue by tool
// call ID without ever displaying it.
func (m *UI) removeQueuedPermission(toolCallID string) {
	m.pendingPermissions = slices.DeleteFunc(m.pendingPermissions, func(p permission.PermissionRequest) bool {
		return p.ToolCallID == toolCallID
	})
}

// handleAgentNotification translates domain agent events into desktop
// notifications using the UI notification backend.
func (m *UI) handleAgentNotification(n notify.Notification) tea.Cmd {
	var cmds []tea.Cmd
	switch n.Type {
	case notify.TypeAgentFinished:
		common.StopTurn()
		cmds = append(cmds, m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("Agent's turn completed in \"%s\"", n.SessionTitle),
		}))
	case notify.TypeAgentError:
		// Terminal edge like TypeAgentFinished; fall through to the
		// busy/queue refresh below. A failed turn never runs whatever is
		// still queued behind it, so restore it to the editor the same
		// way cancelling does instead of leaving it stranded.
		if cmd := m.restoreQueueOnAgentError(n.SessionID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case notify.TypeAgentRetrying:
		m.setRetryStatus(n)
		return nil
	case notify.TypeReAuthenticate:
		return m.handleReAuthenticate(n.ProviderID)
	case notify.TypeAWSSSOAuth:
		return m.handleAWSSSOAuth(n.AWSSOCommand, n.AWSSOURL)
	case notify.TypeAWSSSOAuthResult:
		return m.handleAWSSSOAuthResult(n.Message)
	case notify.TypeBranchForked:
		return m.handleBranchForked(n)
	default:
		return nil
	}
	// TypeAgentFinished / TypeAgentError are the busy→idle edge: the agent
	// clears its active request before publishing precisely so observers
	// can re-probe. Drop the memoized busy state and re-fetch it and the
	// prompt queue off-thread.
	m.clearRetryStatus(n.SessionID)
	m.invalidateBusyCaches()
	m.invalidatePromptQueue()
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// The turn that just ended may have been the one merging the branch
	// on screen. Re-probe rather than assume: most turns on a branch are
	// ordinary drafting, not its last one.
	if n.SessionID == m.currentSessionID() && m.viewingBranch() {
		if cmd := m.dispatchBranchStatusRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// handleBranchForked tells the user a branch now exists and is waiting
// on them, unless the tool call that forked it is already part of the
// transcript on screen — its running/waiting state is visible there,
// so a toast would only repeat it. This is published for every branch,
// including one forked from the top-level session, so this is the one
// place that decides which of them are actually worth surfacing.
func (m *UI) handleBranchForked(n notify.Notification) tea.Cmd {
	if m.branchForkVisible(n.SessionID) {
		return nil
	}
	return tea.Batch(
		util.CmdHandler(util.NewInfoMsg(fmt.Sprintf("Branch %q is waiting for you", n.SessionTitle))),
		m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("Branch %q is waiting for your input", n.SessionTitle),
		}),
	)
}

// branchForkVisible reports whether the agent tool call that forked
// branchSessionID is part of the transcript currently on screen. A
// branch session's ID is the messageID$$toolCallID pair that spawned
// it (see session.Service.CreateAgentToolSessionID), so the lookup
// handleChildSessionMessage uses to find a running child's block finds
// it here too.
func (m *UI) branchForkVisible(branchSessionID string) bool {
	if m.chat == nil || m.com == nil || m.com.Workspace == nil {
		return false
	}
	_, toolCallID, ok := m.com.Workspace.ParseAgentToolSessionID(branchSessionID)
	if !ok {
		return false
	}
	return m.chat.MessageItem(toolCallID) != nil
}

// clearRetryStatus drops the retry banner when it belongs to sessionID,
// so a turn ending elsewhere cannot wipe out another session's banner.
func (m *UI) clearRetryStatus(sessionID string) {
	if m.retryStatus != nil && m.retryStatus.sessionID == sessionID {
		m.retryStatus = nil
	}
}

// setRetryStatus records an in-progress provider retry so the turn
// status line can show it instead of a silent pause. There is no
// explicit "resolved" counterpart: retryActivity stops reporting it
// once the announced delay elapses, since by then the retried request
// is indistinguishable from ordinary thinking time.
func (m *UI) setRetryStatus(n notify.Notification) {
	m.retryStatus = &retryStatus{
		sessionID:  n.SessionID,
		attempt:    n.RetryAttempt,
		maxAttempt: n.RetryMaxAttempts,
		reason:     n.Message,
		until:      time.Now().Add(n.RetryDelay),
	}
}

// handleReAuthenticate opens the auth dialog for a provider, seeded
// with the model the session is actually running so re-auth returns to
// that model rather than to whatever the global config names.
//
// When that model is not known yet the request is held rather than
// dropped: the agent probe is off-thread, and a notification that
// arrives inside that window is the only one there will ever be.
func (m *UI) handleReAuthenticate(providerID string) tea.Cmd {
	providerCfg, ok := m.reAuthProvider(providerID)
	if !ok {
		return nil
	}
	if cmd, opened := m.openReAuthDialog(providerCfg); opened {
		return cmd
	}
	m.pendingReAuth = providerID
	return m.dispatchBusyRefresh()
}

// drainPendingReAuth opens an authentication dialog that had to wait
// for the agent probe. A request that is still undecidable stays armed
// for the next probe rather than forcing one, so a probe that keeps
// failing costs nothing.
func (m *UI) drainPendingReAuth() tea.Cmd {
	if m.pendingReAuth == "" {
		return nil
	}
	providerCfg, ok := m.reAuthProvider(m.pendingReAuth)
	if !ok {
		m.pendingReAuth = ""
		return nil
	}
	cmd, opened := m.openReAuthDialog(providerCfg)
	if !opened {
		return nil
	}
	m.pendingReAuth = ""
	return cmd
}

// reAuthProvider resolves the provider a re-auth request names. A miss
// is permanent: there is no dialog to open for a provider the config
// does not describe.
func (m *UI) reAuthProvider(providerID string) (config.ProviderConfig, bool) {
	cfg := m.com.Config()
	if cfg == nil {
		return config.ProviderConfig{}, false
	}
	return cfg.Providers.Get(providerID)
}

// openReAuthDialog seeds the auth dialog with the session's own model.
// It reports false while that model is unknown, which is a "not yet"
// rather than a "no" — seeding a zero model would send the user back
// to nothing once they re-authenticate.
func (m *UI) openReAuthDialog(providerCfg config.ProviderConfig) (tea.Cmd, bool) {
	active := m.activeAgent()
	if active == nil {
		return nil, false
	}
	return m.openAuthenticationDialog(providerCfg.ToProvider(), active.ModelCfg, active.Slot), true
}

// handleAWSSSOAuth opens the AWS SSO progress dialog (or updates the SSO URL
// on an already-open one). The refresh command runs in the coordinator; this
// dialog is a display surface driven by agent notifications.
func (m *UI) handleAWSSSOAuth(command, url string) tea.Cmd {
	// Update the URL on an already-open dialog.
	if existing := m.dialog.Dialog(dialog.AWSSSOID); existing != nil {
		if awsDlg, ok := existing.(*dialog.AWSSSO); ok && url != "" {
			awsDlg.SetURL(url)
		}
		m.dialog.BringToFront(dialog.AWSSSOID)
		return nil
	}
	if command == "" {
		return nil
	}
	dlg, cmd := dialog.NewAWSSSO(m.com, command)
	if url != "" {
		dlg.SetURL(url)
	}
	m.dialog.OpenDialogWithGrace(dlg)
	return cmd
}

// handleAWSSSOAuthResult finishes the AWS SSO dialog once the refresh command
// exits: it closes on success or shows the error so the user can dismiss it.
func (m *UI) handleAWSSSOAuthResult(errMsg string) tea.Cmd {
	existing := m.dialog.Dialog(dialog.AWSSSOID)
	if existing == nil {
		return nil
	}
	awsDlg, ok := existing.(*dialog.AWSSSO)
	if !ok {
		return nil
	}
	if errMsg == "" {
		// Success: the turn retries transparently, so no need to linger.
		m.dialog.CloseDialog(dialog.AWSSSOID)
		return nil
	}
	awsDlg.Finish(errMsg)
	return nil
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

// checkBangModeAfterPaste engages bang mode when pasted text starts with
// optional whitespace followed by "!". It strips the prefix and adjusts
// the cursor, mirroring the keypress bang-mode entry logic.
func (m *UI) checkBangModeAfterPaste() {
	if m.bangMode {
		return
	}
	val := m.textarea.Value()
	trimmed := strings.TrimLeftFunc(val, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, "!") {
		return
	}
	m.bangMode = true
	m.bangWasEmpty = true
	stripped := trimmed[1:]
	m.textarea.SetValue(stripped)
	col := m.textarea.Column()
	m.textarea.SetCursorColumn(max(0, col-(len(val)-len(stripped))))
	m.setEditorPrompt(m.permissionModeCached())
}

// handlePasteMsg handles a paste message.
func (m *UI) handlePasteMsg(msg tea.PasteMsg) tea.Cmd {
	// Normalize \r\n before the textarea sanitizer sees it.
	msg.Content = strings.ReplaceAll(msg.Content, "\r\n", "\n")

	if m.dialog.HasDialogs() {
		return m.handleDialogMsg(msg)
	}

	if m.focus != uiFocusEditor {
		return nil
	}

	// Terminals typically intercept the paste gesture themselves and send
	// the result as this bracketed paste, rather than forwarding the raw
	// PasteImage key chord. An image-only clipboard has no text form, so
	// it arrives here as empty content; fall back to reading the image
	// directly instead of silently doing nothing.
	if strings.TrimSpace(msg.Content) == "" && m.currentModelSupportsImages() {
		return m.pasteImageFromClipboard
	}

	if hasPasteExceededThreshold(msg) {
		return func() tea.Msg {
			content := []byte(msg.Content)
			if int64(len(content)) > common.MaxAttachmentSize {
				return util.ReportWarn("Paste is too big (>5mb)")
			}
			name := fmt.Sprintf("paste_%d.txt", m.pasteIdx())
			mimeBufferSize := min(512, len(content))
			mimeType := http.DetectContentType(content[:mimeBufferSize])
			return message.Attachment{
				FileName: name,
				FilePath: name,
				MimeType: mimeType,
				Content:  content,
			}
		}
	}

	// Attempt to parse pasted content as file paths. If possible to parse,
	// all files exist and are valid, add as attachments.
	// Otherwise, paste as text.
	paths := fsext.ParsePastedFiles(msg.Content)
	allExistsAndValid := func() bool {
		if len(paths) == 0 {
			return false
		}
		for _, path := range paths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return false
			}

			lowerPath := strings.ToLower(path)
			isValid := false
			for _, ext := range common.AllowedImageTypes {
				if strings.HasSuffix(lowerPath, ext) {
					isValid = true
					break
				}
			}
			if !isValid {
				return false
			}
		}
		return true
	}
	if !allExistsAndValid() {
		prevHeight := m.textarea.Height()
		cmd := m.updateTextareaWithPrevHeight(msg, prevHeight)
		m.checkBangModeAfterPaste()
		return cmd
	}

	var cmds []tea.Cmd
	for _, path := range paths {
		cmds = append(cmds, m.handleFilePathPaste(path))
	}
	return tea.Batch(cmds...)
}

func hasPasteExceededThreshold(msg tea.PasteMsg) bool {
	var (
		lineCount = 0
		colCount  = 0
	)
	for line := range strings.SplitSeq(msg.Content, "\n") {
		lineCount++
		colCount = max(colCount, len(line))

		if lineCount > pasteLinesThreshold || colCount > pasteColsThreshold {
			return true
		}
	}
	return false
}

// handleFilePathPaste handles a pasted file path.
func (m *UI) handleFilePathPaste(path string) tea.Cmd {
	return func() tea.Msg {
		fileInfo, err := os.Stat(path)
		if err != nil {
			return util.ReportError(err)
		}
		if fileInfo.IsDir() {
			return util.ReportWarn("Cannot attach a directory")
		}
		if fileInfo.Size() > common.MaxAttachmentSize {
			return util.ReportWarn("File is too big (>5mb)")
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return util.ReportError(err)
		}

		mimeBufferSize := min(512, len(content))
		mimeType := http.DetectContentType(content[:mimeBufferSize])
		fileName := filepath.Base(path)
		return message.Attachment{
			FilePath: path,
			FileName: fileName,
			MimeType: mimeType,
			Content:  content,
		}
	}
}

// pasteImageFromClipboard reads image data from the system clipboard and
// creates an attachment. If no image data is found, it falls back to
// interpreting clipboard text as a file path.
func (m *UI) pasteImageFromClipboard() tea.Msg {
	imageData, err := clipboard.Read(clipboard.FormatImage)
	if int64(len(imageData)) > common.MaxAttachmentSize {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  "File too large, max 5MB",
		}
	}
	name := fmt.Sprintf("paste_%d.png", m.pasteIdx())
	if err == nil {
		return message.Attachment{
			FilePath: name,
			FileName: name,
			MimeType: mimeOf(imageData),
			Content:  imageData,
		}
	}

	textData, textErr := clipboard.Read(clipboard.FormatText)
	if textErr != nil || len(textData) == 0 {
		return nil // Clipboard is empty or does not contain an image
	}

	path := strings.TrimSpace(string(textData))
	path = strings.ReplaceAll(path, "\\ ", " ")
	if _, statErr := os.Stat(path); statErr != nil {
		return nil // Clipboard does not contain an image or valid file path
	}

	lowerPath := strings.ToLower(path)
	isAllowed := false
	for _, ext := range common.AllowedImageTypes {
		if strings.HasSuffix(lowerPath, ext) {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return util.NewInfoMsg("File type is not a supported image format")
	}

	fileInfo, statErr := os.Stat(path)
	if statErr != nil {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  fmt.Sprintf("Unable to read file: %v", statErr),
		}
	}
	if fileInfo.Size() > common.MaxAttachmentSize {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  "File too large, max 5MB",
		}
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  fmt.Sprintf("Unable to read file: %v", readErr),
		}
	}

	return message.Attachment{
		FilePath: path,
		FileName: filepath.Base(path),
		MimeType: mimeOf(content),
		Content:  content,
	}
}

var pasteRE = regexp.MustCompile(`^paste_(\d+)\.\w+$`)

func (m *UI) pasteIdx() int {
	result := 0
	for _, at := range m.attachments.List() {
		found := pasteRE.FindStringSubmatch(at.FileName)
		if len(found) == 0 {
			continue
		}
		idx, err := strconv.Atoi(found[1])
		if err == nil {
			result = max(result, idx)
		}
	}
	return result + 1
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
