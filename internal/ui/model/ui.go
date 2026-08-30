package model

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math/rand"
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

	"charm.land/bubbles/v2/help"
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
	"github.com/NaturalSelect/angela/internal/clipboard"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/event"
	"github.com/NaturalSelect/angela/internal/fsext"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/home"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/stringext"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/attachments"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	fimage "github.com/NaturalSelect/angela/internal/ui/image"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/logo"
	"github.com/NaturalSelect/angela/internal/ui/notification"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/version"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/charmbracelet/ultraviolet/screen"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/editor"
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
	// jumpToBottomTimerExpiredMsg is sent when the jump-to-bottom timer
	// expires.
	jumpToBottomTimerExpiredMsg struct{}
	// userCommandsLoadedMsg is sent when user commands are loaded.
	userCommandsLoadedMsg struct {
		Commands []commands.CustomCommand
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
	status *Status

	// isCanceling tracks whether the user has pressed escape once to cancel.
	isCanceling bool

	// isJumpingToBottom tracks whether the user has pressed down once to
	// return to the end of the transcript.
	isJumpingToBottom bool

	// sessionIsBranch memoizes whether the loaded session is a branch.
	// Resolving it reads config through the workspace, which the status
	// line renders too often to afford, so it is settled once per load.
	sessionIsBranch bool

	// bangMode tracks whether the editor is in bang (!) shell mode.
	bangMode     bool
	bangWasEmpty bool // true when bang prompt became empty on last keystroke

	// pendingBangCommand holds a shell command that was issued before
	// the session finished loading. The loadSessionMsg handler creates
	// the pending UI item and starts execution once the chat list is
	// stable, eliminating races between session load and shell output.
	pendingBangCommand string

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

	// Chat components
	chat *Chat

	// onboarding state
	onboarding struct {
		yesInitializeSelected bool

		// step is the stage of the first-run flow; provider is the one
		// picked in its first step, which the later steps act on.
		step     onboardingStep
		provider catwalk.Provider

		// model is the pick the configuration step edits, and
		// catwalkModel its catalog entry — zero for a hand-typed model
		// the catalog has never listed.
		model        config.SelectedModel
		catwalkModel catwalk.Model
	}

	// lspStates / lspDiagnostics memoize the workspace LSP state and
	// per-server severity counts (each probe behind them is a synchronous
	// HTTP round-trip in client/server mode, and the sidebar, landing view,
	// and compact header render them every frame). LSP events refresh them
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
	promptQueueItems     []string
	promptQueueCheckedAt time.Time
	promptQueueInFlight  bool
	// promptQueueGen is bumped by every queue state transition; an
	// in-flight fetch captures it at dispatch and its result is discarded
	// if the generation has moved on (see workspace_cache.go).
	promptQueueGen uint64
	// agentBusyCache / yoloCache memoize the workspace busy and permission
	// probes (synchronous HTTP round-trips in client/server mode). Reads
	// never probe; refreshes happen off-thread (see workspace_cache.go).
	agentBusyCache    ttlCache
	yoloCache         ttlCache
	busyFetchInFlight bool
	// agentReady / agentActive memoize the coordinator readiness and the
	// agent the session runs on (AgentIsReady/AgentActive are
	// synchronous HTTP GETs in client/server mode, and modelInfo renders
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

	// Seed the yolo cache once at construction; afterwards it is kept
	// fresh by write-through toggles and off-thread refreshes so Update
	// and View never probe the workspace synchronously.
	yolo := com.Workspace.PermissionSkipRequests()
	ui.yoloCache.set(yolo)

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
	ui.setEditorPrompt(yolo)
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
		return userCommandsLoadedMsg{Commands: customCommands}
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
		m.lastUserMessageTime = msg.lastUserMessageTime
		if cmd := m.applySessionItems(msg.items); cmd != nil {
			cmds = append(cmds, cmd)
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
		if msg.clearStack {
			m.sessionStack = nil
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
		m.session = msg.session
		m.sessionIsBranch = isBranch
		m.sessionFiles = msg.files
		// Session switch: the memoized busy state and queued prompts
		// belong to the previous session. Drop them and re-fetch
		// off-thread so the queue pill and esc behavior track the new
		// session instead of a stale one.
		m.invalidateBusyCaches()
		m.invalidatePromptQueue()
		m.promptQueue = 0
		m.promptQueueItems = nil
		m.promptQueueCheckedAt = time.Time{}
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.startLSPs(msg.lspFilePaths()))
		cmds = append(cmds, m.loadSessionMessagesCmd(m.session.ID))
		// If a bang command was issued before the session finished
		// loading, start it now that the chat list is stable.
		if m.pendingBangCommand != "" {
			cmds = append(cmds, m.runShellCommandInternal(m.pendingBangCommand, true))
			m.pendingBangCommand = ""
		}
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
			m.chat.RemoveMessage(msg.Payload.ID)
		}
		// Follow the turn: spin while the agent works, stop when it stops.
		if cmd := m.syncTurnSpinner(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[history.File]:
		cmds = append(cmds, m.handleFileEvent(msg.Payload))
	case pubsub.Event[app.LSPEvent]:
		// Refresh the memoized LSP state off-thread: LSPGetStates is a
		// synchronous HTTP round-trip in client/server mode and diagnostics
		// events can arrive per edited file.
		if cmd := m.requestLSPRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[workspace.LSPEvent]:
		if cmd := m.requestLSPRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[skills.Event]:
		m.skillStates = msg.Payload.States
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
		if cmd := m.openPermissionsDialog(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Angela is waiting...",
			Message: fmt.Sprintf("Permission required to execute \"%s\"", msg.Payload.ToolName),
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[permission.PermissionNotification]:
		m.handlePermissionNotification(msg.Payload)
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
	case jumpToBottomTimerExpiredMsg:
		m.isJumpingToBottom = false
	case tea.TerminalVersionMsg:
		termVersion := strings.ToLower(msg.Name)
		// Only enable progress bar for the following terminals.
		if !m.sendProgressBar {
			m.sendProgressBar = xstrings.ContainsAnyOf(termVersion, "ghostty", "iterm2", "rio")
		}
		return m, nil
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
	case tea.KeyboardEnhancementsMsg:
		m.keyenh = msg
		if msg.SupportsKeyDisambiguation() {
			m.keyMap.Models.SetHelp("ctrl+m", "models")
			m.keyMap.Editor.Newline.SetHelp("shift+enter", "newline")
		}
	case copyChatHighlightMsg:
		cmds = append(cmds, m.copyChatHighlight())
	case DelayedClickMsg:
		// Handle delayed single-click action (e.g., expansion).
		m.chat.HandleDelayedClick(msg)
	case tea.MouseClickMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		// Route clicks to inline editors that support mouse interaction.
		if m.activeInline != nil {
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
			m.dialog.Update(msg)
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
			m.dialog.Update(msg)
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
			m.dialog.Update(msg)
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
				if lines < 0 {
					m.chat.SelectPrev()
				} else if m.chat.AtBottom() {
					m.chat.SelectLast()
				} else {
					m.chat.SelectNext()
				}
				if cmd := m.chat.ScrollToSelectedAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
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
		m.status.SetInfoMsg(msg)
		ttl := msg.TTL
		if ttl <= 0 {
			ttl = DefaultStatusTTL
		}
		cmds = append(cmds, clearInfoMsgCmd(ttl))
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
	case workspace.ConnectionEvent:
		cmds = append(cmds, m.handleConnectionEvent(msg)...)
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

// loadSessionMessagesCmd fetches a session's transcript and builds its
// chat items off the Update goroutine. Both halves belong here: the
// fetch is an HTTP round-trip in client/server mode, and resolving
// nested agent tool calls costs one more round-trip per nested tool, so
// doing it inline stalls the render loop for the whole tree.
func (m *UI) loadSessionMessagesCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
		if err != nil {
			return util.ReportError(err)()
		}
		items, lastUserMessageTime := m.buildSessionItems(sessionID, msgs)
		return sessionMessagesMsg{
			sessionID:           sessionID,
			items:               items,
			lastUserMessageTime: lastUserMessageTime,
		}
	}
}

// lastAssistantIndex reports the position of the final assistant message in
// a transcript, or -1 when there is none. Only that message can still be
// producing results: the tool calls in an earlier one were all answered
// before the next assistant message was written, so one left without a
// result was orphaned.
func lastAssistantIndex(msgs []*message.Message) int {
	last := -1
	for i, msg := range msgs {
		if msg.Role == message.Assistant {
			last = i
		}
	}
	return last
}

// hasOrphanedCall reports whether any tool call in the transcript is missing
// its result. Probing the agent for liveness costs a round trip in
// client/server mode, and a transcript with every result in place has
// nothing to decide.
func hasOrphanedCall(msgs []*message.Message, results map[string]message.ToolResult) bool {
	for _, msg := range msgs {
		if msg.Role != message.Assistant {
			continue
		}
		for _, tc := range msg.ToolCalls() {
			if _, ok := results[tc.ID]; !ok {
				return true
			}
		}
	}
	return false
}

// buildSessionItems turns a transcript into chat items and reports the
// timestamp of the last user message. It touches no UI state, so it is
// safe to run from a command.
func (m *UI) buildSessionItems(sessionID string, msgs []message.Message) ([]chat.MessageItem, int64) {
	msgPtrs := make([]*message.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolResultMap := chat.BuildToolResultMap(msgPtrs)

	// Asked per session, not through the busy cache: that cache answers
	// for the whole process and carries a TTL, so another session's run
	// would keep this one's orphans spinning.
	runActive := false
	if hasOrphanedCall(msgPtrs, toolResultMap) {
		runActive = m.com.Workspace.AgentIsSessionBusy(sessionID)
	}
	lastAssistant := lastAssistantIndex(msgPtrs)

	var lastUserMessageTime int64
	if len(msgPtrs) > 0 {
		lastUserMessageTime = msgPtrs[0].CreatedAt
	}

	items := make([]chat.MessageItem, 0, len(msgs)*2)
	for i, msg := range msgPtrs {
		msgRunActive := runActive && i == lastAssistant
		switch msg.Role {
		case message.User:
			lastUserMessageTime = msg.CreatedAt
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir(), msgRunActive)...)
		case message.Assistant:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir(), msgRunActive)...)
			if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn {
				infoItem := chat.NewAssistantInfoItem(m.com.Styles, msg, m.com.Config(), time.Unix(lastUserMessageTime, 0))
				items = append(items, infoItem)
			}
		default:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir(), msgRunActive)...)
		}
	}

	m.loadNestedToolCalls(items)
	return items, lastUserMessageTime
}

// applySessionItems installs prebuilt chat items as the current
// session's transcript.
func (m *UI) applySessionItems(items []chat.MessageItem) tea.Cmd {
	var cmds []tea.Cmd

	// If the user switches between sessions while the agent is working we
	// want to make sure the animations are shown. Gate on the agent actually
	// being busy: a session that was killed mid-generation can persist an
	// assistant message with no Finish part, which still reports isSpinning()
	// even though nothing is running. Starting animations for it here would
	// leave a ghost "working" spinner (and a second one alongside any tool
	// spinner) after the session is reloaded.
	if m.isAgentBusy() {
		for _, item := range items {
			if animatable, ok := item.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	}

	if cmd := m.chat.SetMessages(items...); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.chat.RestartPausedVisibleAnimations(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.chat.SelectLast()
	return tea.Sequence(cmds...)
}

// handleConnectionEvent reports the health of the client-server link and,
// once it recovers, reloads the open session. A reload is always needed
// after a degraded episode: events published while the stream was down are
// gone, and if the workspace itself was re-created any run died with it.
func (m *UI) handleConnectionEvent(msg workspace.ConnectionEvent) []tea.Cmd {
	info := util.InfoMsg{
		Type: util.InfoTypeWarn,
		Msg:  "Lost connection to the Angela server — reconnecting…",
		TTL:  30 * time.Second,
	}
	switch msg.State {
	case workspace.ConnectionDegraded:
		slog.Warn("Server connection degraded", "error", msg.Err, "stuck", msg.Stuck)
		if msg.Stuck {
			info.Type = util.InfoTypeError
			info.Msg = "Can't restore the connection to the Angela server. Restart Angela to recover."
			info.TTL = time.Minute
		}
	case workspace.ConnectionRecovered:
		info = util.InfoMsg{
			Type: util.InfoTypeSuccess,
			Msg:  "Reconnected to the Angela server.",
			TTL:  DefaultStatusTTL,
		}
	}
	m.status.SetInfoMsg(info)
	cmds := []tea.Cmd{clearInfoMsgCmd(info.TTL)}
	if msg.State == workspace.ConnectionRecovered && m.session != nil {
		cmds = append(cmds, m.loadSession(m.session.ID))
	}
	return cmds
}

// loadNestedToolCalls recursively loads nested tool calls for the agent tool.
func (m *UI) loadNestedToolCalls(items []chat.MessageItem) {
	for _, item := range items {
		nestedContainer, ok := item.(chat.NestedToolContainer)
		if !ok {
			continue
		}
		toolItem, ok := item.(chat.ToolMessageItem)
		if !ok {
			continue
		}

		tc := toolItem.ToolCall()
		messageID := toolItem.MessageID()

		// Get the agent tool session ID.
		agentSessionID := m.com.Workspace.CreateAgentToolSessionID(messageID, tc.ID)

		// Fetch nested messages.
		nestedMsgs, err := m.com.Workspace.ListMessages(context.Background(), agentSessionID)
		if err != nil || len(nestedMsgs) == 0 {
			continue
		}

		// Build tool result map for nested messages.
		nestedMsgPtrs := make([]*message.Message, len(nestedMsgs))
		for i := range nestedMsgs {
			nestedMsgPtrs[i] = &nestedMsgs[i]
		}
		nestedToolResultMap := chat.BuildToolResultMap(nestedMsgPtrs)

		// Same predicate as the top level, asked about the sub-session:
		// executorForSession answers false for a child session this
		// process never dispatched, which is exactly the question.
		nestedRunActive := false
		if hasOrphanedCall(nestedMsgPtrs, nestedToolResultMap) {
			nestedRunActive = m.com.Workspace.AgentIsSessionBusy(agentSessionID)
		}
		nestedLastAssistant := lastAssistantIndex(nestedMsgPtrs)

		// Extract nested tool items.
		var nestedTools []chat.ToolMessageItem
		for i, nestedMsg := range nestedMsgPtrs {
			nestedItems := chat.ExtractMessageItems(m.com.Styles, nestedMsg, nestedToolResultMap, m.com.Workspace.WorkingDir(), nestedRunActive && i == nestedLastAssistant)
			for _, nestedItem := range nestedItems {
				if nestedToolItem, ok := nestedItem.(chat.ToolMessageItem); ok {
					// Mark nested tools as simple (compact) rendering.
					if simplifiable, ok := nestedToolItem.(chat.Compactable); ok {
						simplifiable.SetCompact(true)
					}
					nestedTools = append(nestedTools, nestedToolItem)
				}
			}
		}

		// Recursively load nested tool calls for any agent tools within.
		nestedMessageItems := make([]chat.MessageItem, len(nestedTools))
		for i, nt := range nestedTools {
			nestedMessageItems[i] = nt
		}
		m.loadNestedToolCalls(nestedMessageItems)

		// Set nested tools on the parent.
		nestedContainer.SetNestedTools(nestedTools)
		nestedContainer.SetTiming(nestedMsgs[0].CreatedAt, nestedMsgs[len(nestedMsgs)-1].CreatedAt)
	}
}

// appendSessionMessage appends a new message to the current session in the chat
// if the message is a tool result it will update the corresponding tool call message
//
// Items built here always count as active: this path only runs in the
// process carrying the run, driven by its event stream, which delivers the
// results as they land.
func (m *UI) appendSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd

	existing := m.chat.MessageItem(msg.ID)
	if existing != nil {
		// message already exists, skip
		return nil
	}

	switch msg.Role {
	case message.User:
		// Shell commands are rendered live via shellResultMsg; skip
		// the persisted duplicate.
		hasShellCmd := false
		for _, part := range msg.Parts {
			if _, ok := part.(message.ShellCommand); ok {
				hasShellCmd = true
				break
			}
		}
		if hasShellCmd {
			return nil
		}
		m.lastUserMessageTime = msg.CreatedAt
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir(), true)
		for _, item := range items {
			if animatable, ok := item.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		m.chat.AppendMessages(items...)
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case message.Assistant:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir(), true)
		for _, item := range items {
			if animatable, ok := item.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		m.chat.AppendMessages(items...)
		if m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn {
			infoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(infoItem)
			if m.chat.Follow() {
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	case message.System:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir(), true)
		if len(items) == 0 {
			break
		}
		m.chat.AppendMessages(items...)
		if m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case message.Tool:
		for _, tr := range msg.ToolResults() {
			toolItem := m.chat.MessageItem(tr.ToolCallID)
			if toolItem == nil {
				// we should have an item!
				continue
			}
			if toolMsgItem, ok := toolItem.(chat.ToolMessageItem); ok {
				toolMsgItem.SetResult(&tr)
				if m.chat.Follow() {
					if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}
	}
	return tea.Sequence(cmds...)
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

// updateSessionMessage updates an existing message in the current session in
// the chat when an assistant message is updated it may include updated tool
// calls as well that is why we need to handle creating/updating each tool call
// message too.
func (m *UI) updateSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd
	existingItem := m.chat.MessageItem(msg.ID)

	if existingItem != nil {
		if assistantItem, ok := existingItem.(*chat.AssistantMessageItem); ok {
			// SetMessage returns a StartAnimation Cmd when the message
			// transitions back to spinning (e.g. its streamed content was
			// reset for a retry). Propagate it so the spinner re-arms
			// instead of freezing.
			if cmd := assistantItem.SetMessage(&msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	shouldRenderAssistant := chat.ShouldRenderAssistantMessage(&msg)
	isEndTurn := msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn
	// If the message of the assistant does not have any response just tool
	// calls we need to remove it, but keep the info item for end-of-turn
	// renders so the footer (model/provider/duration) remains visible when,
	// for example, a hook halts the turn.
	if !shouldRenderAssistant && len(msg.ToolCalls()) > 0 && existingItem != nil {
		m.chat.RemoveMessage(msg.ID)
		if !isEndTurn {
			if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); infoItem != nil {
				m.chat.RemoveMessage(chat.AssistantInfoID(msg.ID))
			}
		}
	}

	if isEndTurn {
		if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); infoItem == nil {
			newInfoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(newInfoItem)
		}
	}

	var items []chat.MessageItem
	for _, tc := range msg.ToolCalls() {
		existingToolItem := m.chat.MessageItem(tc.ID)
		if toolItem, ok := existingToolItem.(chat.ToolMessageItem); ok {
			existingToolCall := toolItem.ToolCall()
			// only update if finished state changed or input changed
			// to avoid clearing the cache
			if (tc.Finished && !existingToolCall.Finished) || tc.Input != existingToolCall.Input {
				toolItem.SetToolCall(tc)
			}
		}
		if existingToolItem == nil {
			items = append(items, chat.NewToolMessageItem(m.com.Styles, msg.ID, tc, nil, false, m.com.Workspace.WorkingDir()))
		}
	}

	for _, item := range items {
		if animatable, ok := item.(chat.Animatable); ok {
			if cmd := animatable.StartAnimation(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	m.chat.AppendMessages(items...)
	if m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return tea.Sequence(cmds...)
}

// handleChildSessionMessage handles messages from child sessions (agent tools).
func (m *UI) handleChildSessionMessage(event pubsub.Event[message.Message]) tea.Cmd {
	var cmds []tea.Cmd

	// Only process messages with tool calls or results.
	if len(event.Payload.ToolCalls()) == 0 && len(event.Payload.ToolResults()) == 0 {
		return nil
	}

	// Check if this is an agent tool session and parse it.
	childSessionID := event.Payload.SessionID
	_, toolCallID, ok := m.com.Workspace.ParseAgentToolSessionID(childSessionID)
	if !ok {
		return nil
	}

	// The agent tool's call ID keys the chat's index map directly, so one
	// lookup finds the block that owns this child session.
	item := m.chat.MessageItem(toolCallID)
	if item == nil {
		return nil
	}
	agentItem, ok := item.(chat.NestedToolContainer)
	if !ok {
		return nil
	}

	// Child messages are the only clock this run has: neither ToolCall nor
	// ToolResult carries a timestamp of its own.
	agentItem.MarkActivity(event.Payload.CreatedAt)

	// Get existing nested tools.
	nestedTools := agentItem.NestedTools()

	// Update or create nested tool calls.
	for _, tc := range event.Payload.ToolCalls() {
		found := false
		for _, existingTool := range nestedTools {
			if existingTool.ToolCall().ID == tc.ID {
				existingTool.SetToolCall(tc)
				found = true
				break
			}
		}
		if !found {
			// Create a new nested tool item.
			nestedItem := chat.NewToolMessageItem(m.com.Styles, event.Payload.ID, tc, nil, false, m.com.Workspace.WorkingDir())
			if simplifiable, ok := nestedItem.(chat.Compactable); ok {
				simplifiable.SetCompact(true)
			}
			if animatable, ok := nestedItem.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			nestedTools = append(nestedTools, nestedItem)
		}
	}

	// Update nested tool results.
	for _, tr := range event.Payload.ToolResults() {
		for _, nestedTool := range nestedTools {
			if nestedTool.ToolCall().ID == tr.ToolCallID {
				nestedTool.SetResult(&tr)
				break
			}
		}
	}

	// Update the agent item with the new nested tools.
	agentItem.SetNestedTools(nestedTools)

	// Update the chat so it updates the index map for animations to work as expected
	m.chat.UpdateNestedToolIDs(toolCallID)

	if m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return tea.Sequence(cmds...)
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
		cmds = append(cmds, m.loadSession(msg.Session.ID, loadSessionOpt{clearStack: true}))

	// Open dialog message.
	case dialog.ActionOpenDialog:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.openDialog(msg.DialogID); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Command dialog messages.
	case dialog.ActionToggleYoloMode:
		m.toggleYoloMode()
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
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, func() tea.Msg {
			err := m.com.Workspace.AgentSummarize(context.Background(), msg.SessionID)
			if err != nil {
				return util.ReportError(err)()
			}
			return nil
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionAbortBranch:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.abortBranch(msg.SessionID); cmd != nil {
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

// modelPickTarget says where picking a model for a slot lands.
type modelPickTarget int

const (
	// modelPickGlobal edits the global default: there is no session, or
	// the slot is one the session's agent does not run on (the chore
	// model, say).
	modelPickGlobal modelPickTarget = iota
	// modelPickSession edits the session's own agent instance.
	modelPickSession
	// modelPickUnknown means the session's agent has not been probed
	// yet, so the other two cannot be told apart. Falling back to
	// global here would rewrite the default for every future session
	// on the strength of a probe that simply had not landed.
	modelPickUnknown
)

// modelPickScope reports where picking a model for this slot should
// land. Only the slot the session's agent actually runs on is
// session-scoped.
func (m *UI) modelPickScope(slot config.ModelConfigName) modelPickTarget {
	if m.currentSessionID() == "" {
		return modelPickGlobal
	}
	active := m.activeAgent()
	if active == nil {
		return modelPickUnknown
	}
	if active.ModelName == slot {
		return modelPickSession
	}
	return modelPickGlobal
}

// transparentToggledMsg carries the persisted transparency setting back
// to Update. The command must not apply it directly: Draw reads
// isTransparent on every frame, so writing it from a command races the
// render loop.
type transparentToggledMsg struct{ on bool }

// toggleTransparentCmd flips the global transparent-background option
// and persists it, reporting the new value for Update to apply.
func (m *UI) toggleTransparentCmd() tea.Cmd {
	return func() tea.Msg {
		cfg := m.com.Config()
		if cfg == nil {
			return util.ReportError(errors.New("configuration not found"))()
		}
		isTransparent := cfg.Options != nil && cfg.Options.TUI.Transparent != nil && *cfg.Options.TUI.Transparent
		newValue := !isTransparent
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.transparent", newValue); err != nil {
			return util.ReportError(err)()
		}
		return transparentToggledMsg{on: newValue}
	}
}

// toggleThinkingCmd flips the thinking flag on the session's agent
// instance. The flag lives on the session, so it takes effect from the
// next turn and leaves every other session alone.
func (m *UI) toggleThinkingCmd() tea.Cmd {
	sessionID := m.currentSessionID()
	if sessionID == "" {
		return util.ReportWarn("Start a session before toggling thinking mode.")
	}

	return m.refreshActiveAgentCmd(func() tea.Msg {
		// The flip happens under the session's lock rather than here:
		// two clients toggling from the same cached value would both
		// write the same absolute result, and the second flip would
		// not cancel the first.
		edit := config.ActiveAgentEdit{ToggleThink: true}
		active, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit)
		if err != nil {
			return util.ReportError(err)()
		}
		status := "disabled"
		if active.ModelCfg.Think {
			status = "enabled"
		}
		return util.NewInfoMsg("Thinking mode " + status)
	})
}

// handleSelectModel performs the model selection after any provider
// pre-checks have completed.
// handleSelectAgent points the current session at the chosen primary
// agent. The switch lands on the session's agent instance, so it takes
// effect from the next turn; a turn already streaming keeps the agent it
// started on.
func (m *UI) handleSelectAgent(msg dialog.ActionSelectAgent) tea.Cmd {
	m.dialog.CloseDialog(dialog.AgentsID)
	sessionID := m.currentSessionID()
	if sessionID == "" {
		return util.ReportWarn("Start a session before switching agents.")
	}
	if active := m.activeAgent(); active != nil && active.AgentID == msg.AgentID {
		return nil
	}

	agentID := msg.AgentID
	return m.refreshActiveAgentCmd(func() tea.Msg {
		edit := config.ActiveAgentEdit{Agent: agentID}
		if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
			return util.ReportError(err)()
		}
		return nil
	})
}

// handleSelectVariant points the session's model at a preset. The
// variant lives on the session's agent instance, so it takes effect from
// the next turn and leaves the global config untouched.
func (m *UI) handleSelectVariant(variant string) tea.Cmd {
	m.dialog.CloseDialog(dialog.VariantsID)
	sessionID := m.currentSessionID()
	if sessionID == "" {
		return util.ReportWarn("Start a session before switching variants.")
	}
	if active := m.activeAgent(); active != nil && active.Variant == variant {
		return nil
	}

	return m.refreshActiveAgentCmd(func() tea.Msg {
		edit := config.ActiveAgentEdit{Variant: &variant}
		if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
			return util.ReportError(err)()
		}
		return util.NewInfoMsg(variantSetMessage(variant))
	})
}

// variantSetMessage names what the user just selected. The baseline has
// no name of its own, so it is described rather than quoted.
func variantSetMessage(variant string) string {
	if variant == "" {
		return "Using the model's baseline parameters"
	}
	return "Variant set to " + variant
}

// cycleVariant steps to the preset after the one in effect, wrapping
// through the baseline. Cycling is what makes a variant cheap to reach
// mid-task, so it deliberately skips the dialog.
func (m *UI) cycleVariant() tea.Cmd {
	if m.session == nil {
		return util.ReportWarn("Start a session before switching variants.")
	}
	active := m.activeAgent()
	if active == nil {
		return util.ReportWarn("The agent is still starting up.")
	}
	choices := append([]string{""}, active.ModelCfg.VariantNames(&active.CatwalkCfg)...)
	if len(choices) < 2 {
		return util.ReportWarn("This model offers no variants.")
	}
	current := slices.Index(choices, active.Variant)
	if current < 0 {
		current = 0
	}
	return m.handleSelectVariant(choices[(current+1)%len(choices)])
}

// openVariantsDialog opens the preset picker for the session's model.
// There is nothing to switch without a session: the variant is recorded
// on the session, not globally.
func (m *UI) openVariantsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.VariantsID) {
		m.dialog.BringToFront(dialog.VariantsID)
		return nil
	}
	if m.session == nil {
		return util.ReportWarn("Start a session before switching variants.")
	}
	active := m.activeAgent()
	if active == nil {
		return util.ReportWarn("The agent is still starting up.")
	}
	variants := active.ModelCfg.VariantNames(&active.CatwalkCfg)
	if len(variants) == 0 {
		return util.ReportWarn("This model offers no variants.")
	}

	variantsDialog, err := dialog.NewVariants(m.com,
		active.CatwalkCfg.Name, variants, active.Variant)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(variantsDialog)
	return nil
}

func (m *UI) handleSelectModel(msg dialog.ActionSelectModel) tea.Cmd {
	var cmds []tea.Cmd

	// The onboarding credential step carries no model — the provider
	// dialog had none to hand it. Arriving here from that step means
	// auth just succeeded, so move on to the model list rather than
	// persisting a blank pick and starting the agent on it.
	if m.state == uiOnboarding && m.onboarding.step == onboardingStepAuth {
		return m.openOnboardingStep(onboardingStepModel)
	}

	// we ignore dialogs with the oauth id as they need to be able to be dismissed
	if m.isAgentBusy() && !m.dialog.ContainsDialog(dialog.OAuthID) {
		return util.ReportWarn("Agent is busy, please wait...")
	}

	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}

	var (
		providerID   = msg.Model.Provider
		isCopilot    = providerID == string(catwalk.InferenceProviderCopilot)
		isConfigured = func() bool { _, ok := cfg.Providers.Get(providerID); return ok }
		isOnboarding = m.state == uiOnboarding
	)

	// Attempt to import GitHub Copilot tokens from VSCode if available.
	if isCopilot && !isConfigured() && !msg.ReAuthenticate {
		m.com.Workspace.ImportCopilot()
	}

	if !isConfigured() || msg.ReAuthenticate {
		m.dialog.CloseDialog(dialog.ModelsID)
		if cmd := m.openAuthenticationDialog(msg.Provider, msg.Model, msg.ModelType); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...)
	}

	// The onboarding pick is not final: its parameters, and for a
	// hand-typed model its very existence in the catalog, are settled by
	// the configuration step. This sits after the credential branch so
	// an unconfigured provider still authenticates first and re-enters
	// here once it succeeds.
	if isOnboarding && m.onboarding.step == onboardingStepModel {
		m.onboarding.model = msg.Model
		m.onboarding.catwalkModel = catwalk.Model{}
		if catwalkModel := cfg.GetModel(msg.Model.Provider, msg.Model.Model); catwalkModel != nil {
			m.onboarding.catwalkModel = *catwalkModel
		}
		return m.openOnboardingStep(onboardingStepModelConfig)
	}

	// Picking a model for the role the session's agent runs on edits
	// that session's instance and leaves the global default alone.
	// Picking one for any other slot (the chore model, say), or picking
	// during onboarding when no session exists yet, is a global
	// preference.
	sessionID := m.currentSessionID()
	scope := modelPickGlobal
	if !isOnboarding {
		scope = m.modelPickScope(msg.ModelType)
	}
	if scope == modelPickUnknown {
		// Which of the two this is depends on an agent probe that has
		// not landed. Writing the global default on a guess would
		// change the model for every future session, so the pick waits
		// for the probe instead.
		m.dialog.CloseDialog(dialog.ModelsID)
		cmds = append(cmds,
			util.ReportWarn("The agent is still starting up — pick again in a moment."),
			agentModelChangedCmd,
		)
		return tea.Batch(cmds...)
	}

	modelChangedMsg := func() tea.Msg {
		var (
			modelType = stringext.Capitalize(string(msg.ModelType))
			modelName = msg.Model.Model
		)
		if catwalkModel := cfg.GetModel(msg.Model.Provider, msg.Model.Model); catwalkModel != nil && catwalkModel.Name != "" {
			modelName = catwalkModel.Name
		}
		return util.NewInfoMsg(fmt.Sprintf("%s model changed to %s", modelType, modelName))
	}

	if scope == modelPickSession {
		editCmd := func() tea.Msg {
			edit := config.ActiveAgentEdit{ModelName: msg.ModelType, Model: &msg.Model}
			if _, err := m.com.Workspace.AgentEditActive(context.Background(), sessionID, edit); err != nil {
				return util.ReportError(err)()
			}
			return modelChangedMsg()
		}
		cmds = append(cmds, m.refreshActiveAgentCmd(tea.Sequence(
			m.recordRecentModelCmd(msg.ModelType, msg.Model),
			editCmd,
		)))
	} else {
		cmds = append(cmds, m.refreshActiveAgentCmd(
			m.applyGlobalModelCmd(msg.ModelType, msg.Model, isOnboarding, modelChangedMsg),
		))
	}

	m.dialog.CloseDialog(dialog.APIKeyInputID)
	m.dialog.CloseDialog(dialog.OAuthID)
	m.dialog.CloseDialog(dialog.ModelsID)

	if isOnboarding {
		m.setState(uiLanding, uiFocusEditor)
	}

	return tea.Batch(cmds...)
}

// handleConfigureModel finishes onboarding once the model's parameters
// are settled: it registers the model, persists the pick, and starts the
// agent on it.
func (m *UI) handleConfigureModel(msg dialog.ActionConfigureModel) tea.Cmd {
	m.closeOnboardingDialogs()
	m.setState(uiLanding, uiFocusEditor)

	done := func() tea.Msg {
		name := cmp.Or(msg.Catwalk.Name, msg.Model.Model)
		return util.NewInfoMsg(fmt.Sprintf("%s model changed to %s",
			stringext.Capitalize(string(msg.ModelType)), name))
	}
	return m.refreshActiveAgentCmd(m.applyOnboardingModelCmd(msg, done))
}

// applyOnboardingModelCmd registers the model under its provider, saves
// it as the preference, and starts the agent.
//
// The steps live in one command because each depends on the one before
// having succeeded, and their order is load-bearing: a model absent from
// the provider's list does not resolve, so registering it last would
// leave both the preference write and the agent start falling back to
// the default model.
func (m *UI) applyOnboardingModelCmd(msg dialog.ActionConfigureModel, done func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.UpsertProviderModel(config.ScopeGlobal, msg.Model.Provider, msg.Catwalk); err != nil {
			return util.ReportError(err)()
		}
		if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, msg.ModelType, msg.Model); err != nil {
			return util.ReportError(err)()
		}
		if err := m.com.Workspace.InitCoderAgent(context.TODO()); err != nil {
			return util.ReportError(err)()
		}
		return done()
	}
}

// applyGlobalModelCmd persists a global model pick and brings the agent
// in line with it. The steps live in one command because each depends on
// the one before having succeeded: tea.Sequence would run them all even
// after a failure, and tea.Batch would run them concurrently.
//
// During onboarding there is no coordinator yet, so starting one is what
// applies the model; afterwards the existing one is reconciled instead.
func (m *UI) applyGlobalModelCmd(
	name config.ModelConfigName,
	model config.SelectedModel,
	startAgent bool,
	done func() tea.Msg,
) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, name, model); err != nil {
			return util.ReportError(err)()
		}
		if startAgent {
			if err := m.com.Workspace.InitCoderAgent(context.TODO()); err != nil {
				return util.ReportError(err)()
			}
			return done()
		}
		if err := m.com.Workspace.UpdateAgentModel(context.TODO()); err != nil {
			return util.ReportError(err)()
		}
		return done()
	}
}

// recordRecentModelCmd records a model in the global "recently used"
// list. The pick itself may be session-scoped, but recents feed the
// dialog for every session: recording changes no session's resolution,
// and omitting it would hide the model the user just picked.
func (m *UI) recordRecentModelCmd(name config.ModelConfigName, model config.SelectedModel) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.RecordRecentModel(config.ScopeGlobal, name, model); err != nil {
			return util.ReportError(err)()
		}
		return nil
	}
}

func (m *UI) openAuthenticationDialog(provider catwalk.Provider, model config.SelectedModel, modelType config.ModelConfigName) tea.Cmd {
	var (
		dlg dialog.Dialog
		cmd tea.Cmd

		isOnboarding = m.state == uiOnboarding
	)

	switch provider.ID {
	case catwalk.InferenceProviderCopilot:
		dlg, cmd = dialog.NewOAuthCopilot(m.com, isOnboarding, provider, model, modelType)
	default:
		dlg, cmd = dialog.NewAPIKeyInput(m.com, isOnboarding, provider, model, modelType)
	}

	if m.dialog.ContainsDialog(dlg.ID()) {
		m.dialog.BringToFront(dlg.ID())
		return nil
	}

	m.dialog.OpenDialogWithGrace(dlg)
	return cmd
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
		case key.Matches(msg, m.keyMap.ToggleYolo):
			yolo := m.toggleYoloMode()
			status := "disabled"
			if yolo {
				status = "enabled"
			}
			cmds = append(cmds, util.ReportInfo("Yolo mode "+status))
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

	// Escape leaves a sub-session. It sits after the busy check so a running
	// turn is stopped first, and ahead of the focus switch so it also works
	// from the editor — but an open completion popup owns Escape while it is
	// up, or dismissing it would throw the user out of the session instead.
	if key.Matches(msg, m.keyMap.Chat.Back) && m.inSubSession() && !m.completionsOpen {
		return tea.Batch(append(cmds, m.leaveSubSession())...)
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
					switch msg := msg.(type) {
					case completions.SelectionMsg[completions.FileCompletionValue]:
						cmds = append(cmds, m.insertFileCompletion(msg.Value.Path))
						if !msg.KeepOpen {
							m.closeCompletions()
						}
					case completions.SelectionMsg[completions.ResourceCompletionValue]:
						cmds = append(cmds, m.insertMCPResourceCompletion(msg.Value))
						if !msg.KeepOpen {
							m.closeCompletions()
						}
					case completions.SelectionMsg[completions.AgentCompletionValue]:
						cmds = append(cmds, m.insertAgentCompletion(msg.Value.ID))
						if !msg.KeepOpen {
							m.closeCompletions()
						}
					case completions.ClosedMsg:
						m.completionsOpen = false
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
					m.setEditorPrompt(m.yoloModeCached())
					m.textarea.Placeholder = m.editorPlaceholder()
					m.historyReset()
					return tea.Batch(m.runShellCommand(value))
				}

				attachments := m.attachments.List()
				m.attachments.Reset()
				if len(value) == 0 && !message.ContainsTextAttachment(attachments) {
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
					m.setEditorPrompt(m.yoloModeCached())
					break
				}

				// Check for a completion trigger before passing to textarea.
				curValue := m.textarea.Value()
				curIdx := len(curValue)

				// Both triggers only fire at the start of a word, so that
				// an address or a comment marker mid-sentence stays text.
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
					m.setEditorPrompt(m.yoloModeCached())
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
			case key.Matches(msg, m.keyMap.Tab):
				if m.viewingSubAgent() {
					break
				}
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
				if cmd := m.chat.ScrollToTopAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				m.chat.SelectFirst()
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

// drawHeader draws the header section of the UI.
func (m *UI) drawHeader(scr uv.Screen, area uv.Rectangle) {
	m.header.drawHeader(scr, area, m.sessionTrail(), area.Dx(), m.lspErrorCount())
}

// drawEditor draws whatever currently owns the editor rect: an inline dialog
// (question form, etc.) when one is active, otherwise the prompt textarea.
func (m *UI) drawEditor(scr uv.Screen, layout uiLayout) {
	if m.activeInline == nil {
		m.drawPromptBox(scr, layout.editor)
		m.inlineCursor = nil
		return
	}

	m.activeInline.SetFocused(m.focus == uiFocusEditor)
	if qf, ok := m.activeInline.(*dialog.QuestionForm); ok &&
		m.focus != uiFocusEditor && m.shouldCollapseQuestion(qf) {
		qf.DrawCollapsed(scr, layout.editor)
		m.inlineCursor = nil
		return
	}
	m.inlineCursor = m.activeInline.Draw(scr, layout.editor)
}

// Draw implements [uv.Drawable] and draws the UI model.
func (m *UI) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	layout := m.generateLayout(area.Dx(), area.Dy())

	if m.layout != layout {
		m.layout = layout
		m.updateSize()
	}

	// Clear the screen first
	screen.Clear(scr)

	switch m.state {
	case uiOnboarding:
		m.drawHeader(scr, layout.header)

		// NOTE: Onboarding flow will be rendered as dialogs below, but
		// positioned at the bottom left of the screen.

	case uiInitialize:
		m.drawHeader(scr, layout.header)

		main := uv.NewStyledString(m.initializeView())
		main.Draw(scr, layout.main)

	case uiLanding:
		m.drawHeader(scr, layout.header)
		main := uv.NewStyledString(m.landingView())
		main.Draw(scr, layout.main)

		m.drawEditor(scr, layout)

	case uiChat:
		m.drawHeader(scr, layout.header)
		m.chat.Draw(scr, layout.main)
		m.drawEditor(scr, layout)

		uv.NewStyledString(m.renderTurnStatus(layout.turnStatus.Dx())).
			Draw(scr, layout.turnStatus)

		// Draw details overlay when open
		if m.detailsOpen {
			m.drawSessionDetails(scr, layout.sessionDetails)
		}
	}

	isOnboarding := m.state == uiOnboarding

	// Add status and help layer
	m.status.SetHideHelp(isOnboarding)
	m.status.Draw(scr, layout.status)

	// Draw completions popup if open
	if !isOnboarding && m.completionsOpen && m.completions.HasItems() {
		w, h := m.completions.Size()
		x := m.completionsPositionStart.X
		y := m.completionsPositionStart.Y - h

		screenW := area.Dx()
		if x+w > screenW {
			x = screenW - w
		}
		x = max(0, x)
		y = max(0, y+1) // Offset for attachments row

		completionsView := uv.NewStyledString(m.completions.Render())
		completionsView.Draw(scr, image.Rectangle{
			Min: image.Pt(x, y),
			Max: image.Pt(x+w, y+h),
		})
	}

	// Debugging rendering (visually see when the tui rerenders)
	if os.Getenv("ANGELA_UI_DEBUG") == "true" {
		debugView := lipgloss.NewStyle().Background(lipgloss.ANSIColor(rand.Intn(256))).Width(4).Height(2)
		debug := uv.NewStyledString(debugView.String())
		debug.Draw(scr, image.Rectangle{
			Min: image.Pt(4, 1),
			Max: image.Pt(8, 3),
		})
	}

	// This needs to come last to overlay on top of everything. We always pass
	// the full screen bounds because the dialogs will position themselves
	// accordingly.
	if m.dialog.HasDialogs() {
		return m.dialog.Draw(scr, scr.Bounds())
	}

	switch m.focus {
	case uiFocusEditor:
		if m.layout.editor.Dy() <= 0 {
			// Don't show cursor if editor is not visible
			return nil
		}
		if m.detailsOpen {
			// Don't show cursor if details overlay is open
			return nil
		}

		if m.activeInline != nil {
			if cur := m.inlineCursor; cur != nil {
				cur.X += m.layout.editor.Min.X // Adjust for app margins
				cur.Y += m.layout.editor.Min.Y // Inline editor draws from area top
				return cur
			}
			return nil
		}

		if m.textarea.Focused() && !m.viewingSubAgent() {
			cur := m.textarea.Cursor()
			// App margin plus the box's left border.
			cur.X += m.layout.editor.Min.X + 1
			// Attachments row plus the box's top border.
			cur.Y += m.layout.editor.Min.Y + 2
			return cur
		}
	}
	return nil
}

// View renders the UI model's view.
func (m *UI) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if !m.isTransparent {
		v.BackgroundColor = m.com.Styles.Background
	}
	if m.activeInline != nil {
		v.MouseMode = tea.MouseModeAllMotion
	} else {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.ReportFocus = m.caps.ReportFocusEvents
	v.WindowTitle = "angela " + home.Short(m.com.Workspace.WorkingDir())

	canvas := uv.NewScreenBuffer(m.width, m.height)
	v.Cursor = m.Draw(canvas, canvas.Bounds())

	content := strings.ReplaceAll(canvas.Render(), "\r\n", "\n") // normalize newlines
	contentLines := strings.Split(content, "\n")
	for i, line := range contentLines {
		// Trim trailing spaces for concise rendering
		contentLines[i] = strings.TrimRight(line, " ")
	}

	content = strings.Join(contentLines, "\n")

	v.Content = content
	if m.progressBarEnabled && m.sendProgressBar && m.isAgentBusy() {
		// HACK: use a random percentage to prevent ghostty from hiding it
		// after a timeout.
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, rand.Intn(100))
	}

	return v
}

// cancelHint returns the esc binding with the wording the current turn state
// calls for: clearing a queue, confirming a second press, or plain cancel.
func (m *UI) cancelHint() key.Binding {
	b := m.keyMap.Chat.Cancel
	switch {
	case m.isCanceling:
		b.SetHelp("esc", "press again to cancel")
	case m.promptQueue > 0:
		b.SetHelp("esc", "clear queue")
	}
	return b
}

// commandsHint returns the command-palette binding, advertising the bare "/"
// shortcut only where typing it would actually open the palette.
func (m *UI) commandsHint() key.Binding {
	b := m.keyMap.Commands
	if m.focus == uiFocusEditor && m.textarea.Value() == "" {
		b.SetHelp("/ or ctrl+p", "commands")
	}
	return b
}

// tabHint returns the focus-switch binding labeled with where it goes.
func (m *UI) tabHint() key.Binding {
	b := m.keyMap.Tab
	if m.focus == uiFocusEditor {
		b.SetHelp("tab", "focus chat")
	} else {
		b.SetHelp("tab", "focus editor")
	}
	return b
}

// shortHelpLimit caps the help line so it stays one row on a narrow terminal.
// Everything cut from here is still reachable through ctrl+p and ctrl+g.
const shortHelpLimit = 4

// shortHelpWidth measures the line the way [help.Model] renders it: each hint
// is "key desc", hints joined by a three-cell separator.
func shortHelpWidth(binds []key.Binding) int {
	var w, n int
	for _, b := range binds {
		if !b.Enabled() {
			continue
		}
		if n > 0 {
			w += 3
		}
		h := b.Help()
		w += ansi.StringWidth(h.Key) + 1 + ansi.StringWidth(h.Desc)
		n++
	}
	return w
}

// helpWidth is the room the status bar actually gives the help line.
func (m *UI) helpWidth() int {
	w := m.layout.status.Dx()
	if w <= 0 {
		w = m.width
	}
	st := m.com.Styles.Status.Help
	return w - st.GetPaddingLeft() - st.GetPaddingRight()
}

// fitShortHelp drops optional hints until the line fits one row. The help
// component truncates unpredictably once the budget runs out — at 80 columns
// it can emit a 106-cell line — so the line has to arrive already short
// enough. The trailing two hints go last: quit and more are what a stuck user
// reaches for.
func fitShortHelp(binds []key.Binding, width int) []key.Binding {
	if width <= 0 {
		return binds
	}
	for len(binds) > 2 && shortHelpWidth(binds) > width {
		binds = slices.Delete(binds, len(binds)-3, len(binds)-2)
	}
	return binds
}

// ShortHelp implements [help.KeyMap].
func (m *UI) ShortHelp() []key.Binding {
	// When an inline editor is active, show its help.
	if m.activeInline != nil {
		return m.activeInline.ShortHelp()
	}

	k := &m.keyMap
	if m.state == uiInitialize {
		return []key.Binding{k.Quit}
	}

	var binds []key.Binding
	if m.state == uiChat && m.isAgentBusy() {
		binds = append(binds, m.cancelHint())
	}

	// The way out of a sub-session leads the standing hints: it is the only
	// one that is not discoverable anywhere else, so it must survive the
	// trim on a narrow terminal.
	if m.inSubSession() {
		binds = append(binds, k.Chat.Back)
	}

	binds = append(binds, m.tabHint(), m.commandsHint())

	switch m.focus {
	case uiFocusEditor:
		binds = append(binds, k.Editor.Newline)
	case uiFocusMain:
		binds = append(binds, k.Chat.UpDown, k.Chat.Copy)
		if _, ok := m.selectedAgentTool(); ok {
			binds = append(binds, k.Chat.OpenSubSession)
		}
	}

	if m.hasSession() {
		details := k.Chat.Details
		details.SetHelp("ctrl+d", "details")
		binds = append(binds, details)
	}
	binds = append(binds, k.Quit, k.Help)

	if len(binds) > shortHelpLimit {
		binds = append(binds[:shortHelpLimit-2:shortHelpLimit-2], k.Quit, k.Help)
	}
	return fitShortHelp(binds, m.helpWidth())
}

// FullHelp implements [help.KeyMap]. Four fixed groups so the layout does not
// reshuffle as session state changes.
func (m *UI) FullHelp() [][]key.Binding {
	// When an inline editor is active, show its help.
	if m.activeInline != nil {
		return [][]key.Binding{m.activeInline.ShortHelp()}
	}

	k := &m.keyMap
	if m.state == uiInitialize {
		return [][]key.Binding{{k.Quit}}
	}

	less := k.Help
	less.SetHelp("ctrl+g", "less")

	navigation := []key.Binding{
		k.Chat.UpDown,
		k.Chat.UpDownOneItem,
		k.Chat.PageUp,
		k.Chat.PageDown,
		k.Chat.Copy,
		k.Chat.Expand,
	}

	editor := []key.Binding{
		m.tabHint(),
		k.Editor.Newline,
		k.Editor.MentionAgent,
		k.Editor.MentionFile,
		k.Editor.OpenEditor,
	}
	if m.currentModelSupportsImages() {
		editor = append(editor, k.Editor.AddImage, k.Editor.PasteImage)
	}
	if len(m.attachments.List()) > 0 {
		editor = append(editor, k.Editor.AttachmentDeleteMode)
	}

	session := []key.Binding{
		m.commandsHint(),
		k.Chat.NewSession,
		k.Chat.Details,
		k.Sessions,
		k.Models,
		k.CycleVariant,
	}

	app := []key.Binding{k.ToggleYolo, k.Suspend, less, k.Quit}
	if m.state == uiChat && m.isAgentBusy() {
		app = append([]key.Binding{m.cancelHint()}, app...)
	}

	return [][]key.Binding{navigation, editor, session, app}
}

// currentModelSupportsImages reports whether the model the session is
// actually running accepts image attachments. It reads the memoized
// active agent rather than the global config: another session may be on
// a different model, and the file picker must offer what this one can
// take.
func (m *UI) currentModelSupportsImages() bool {
	active := m.activeAgent()
	return active != nil && active.CatwalkCfg.SupportsImages
}

// toggleCompactMode toggles compact mode between uiChat and uiChatCompact states.
func (m *UI) toggleCompactMode() tea.Cmd {
	m.forceCompactMode = !m.forceCompactMode

	err := m.com.Workspace.SetCompactMode(config.ScopeGlobal, m.forceCompactMode)
	if err != nil {
		return util.ReportError(err)
	}

	m.updateLayoutAndSize()

	return nil
}

// updateLayoutAndSize updates the layout and sizes of UI components.
func (m *UI) updateLayoutAndSize() {
	// Compact mode is now purely vertical: the layout no longer has a
	// horizontal branch, so only short terminals (or an explicit toggle)
	// need to drop the gaps between bands.
	if m.state == uiChat {
		m.isCompact = m.forceCompactMode || m.height < compactModeHeightBreakpoint
	}

	// First pass sizes components from the current textarea height.
	m.layout = m.generateLayout(m.width, m.height)
	prevHeight := m.textarea.Height()
	m.updateSize()

	// SetWidth can change textarea height due to soft-wrap recalculation.
	// If that happens, run one reconciliation pass with the new height.
	if m.textarea.Height() != prevHeight {
		m.layout = m.generateLayout(m.width, m.height)
		m.updateSize()
	}
}

// handleTextareaHeightChange checks whether the textarea height changed and,
// if so, recalculates the layout. When the chat is in follow mode it keeps
// the view scrolled to the bottom. The returned command, if non-nil, must be
// batched by the caller.
func (m *UI) handleTextareaHeightChange(prevHeight int) tea.Cmd {
	if m.textarea.Height() == prevHeight {
		return nil
	}
	m.updateLayoutAndSize()
	if m.state == uiChat && m.chat.Follow() {
		return m.chat.ScrollToBottomAndAnimate()
	}
	return nil
}

// updateTextarea updates the textarea for msg and then reconciles layout if
// the textarea height changed as a result.
func (m *UI) updateTextarea(msg tea.Msg) tea.Cmd {
	return m.updateTextareaWithPrevHeight(msg, m.textarea.Height())
}

// updateTextareaWithPrevHeight is for cases when the height of the layout may
// have changed.
//
// Particularly, it's for cases where the textarea changes before
// textarea.Update is called (for example, SetValue, Reset, and InsertRune). We
// pass the height from before those changes took place so we can compare
// "before" vs "after" sizing and recalculate the layout if the textarea grew
// or shrank.
func (m *UI) updateTextareaWithPrevHeight(msg tea.Msg, prevHeight int) tea.Cmd {
	ta, cmd := m.textarea.Update(msg)
	m.textarea = ta
	return tea.Batch(cmd, m.handleTextareaHeightChange(prevHeight))
}

// updateSize updates the sizes of UI components based on the current layout.
func (m *UI) updateSize() {
	// Set status width
	m.status.SetWidth(m.layout.status.Dx())

	m.chat.SetSize(m.layout.main.Dx(), m.layout.main.Dy())
	m.textarea.MaxHeight = TextareaMaxHeight
	m.textarea.SetWidth(m.layout.editor.Dx() - editorBoxBorders)
}

// Layout constants for the single unified skeleton.
const (
	headerHeight     = 1
	headerGapHeight  = 1
	mainBottomGap    = 1
	turnStatusHeight = 1
	// editorGapHeight separates the editor from the turn-status row.
	// Without it the bottom bands read as one crammed block.
	editorGapHeight = 1
	// appMarginX insets the content from each terminal edge so it sits on a
	// page instead of running into the frame.
	appMarginX = 2
	// logoWallHeight is the row count of the letterform wall: three rows of
	// letterforms plus the version line.
	logoWallHeight = 4
)

// generateLayout calculates the layout rectangles for all UI components. Every
// state shares one vertical stack; states differ only in which bands they use.
//
//	header
//	(gap)
//	main
//	(gap)
//	editor
//	turn status
//	help
func (m *UI) generateLayout(w, h int) uiLayout {
	area := image.Rect(0, 0, w, h)

	helpHeight := 1
	var helpKeyMap help.KeyMap = m
	if m.status != nil && m.status.ShowingAll() {
		for _, row := range helpKeyMap.FullHelp() {
			helpHeight = max(helpHeight, len(row))
		}
	}

	// App margins: one row top and bottom, one column each side.
	var appRect, helpRect image.Rectangle
	layout.Vertical(
		layout.Len(area.Dy()-helpHeight),
		layout.Fill(1),
	).Split(area).Assign(&appRect, &helpRect)
	appRect.Min.Y += 1
	appRect.Max.Y -= 1
	helpRect.Min.Y -= 1
	appRect.Min.X += appMarginX
	appRect.Max.X -= appMarginX

	l := uiLayout{area: area, status: helpRect}

	// Header band. Every state gets the one-line instrument bar. The
	// letterform wall belongs to the landing body, where it can be
	// composed with the menu and dropped on short terminals.
	header := headerHeight
	gap := headerGapHeight
	if m.isCompact {
		gap = 0
	}

	var headerRect, bodyRect image.Rectangle
	layout.Vertical(
		layout.Len(header),
		layout.Fill(1),
	).Split(appRect).Assign(&headerRect, &bodyRect)
	l.header = headerRect
	bodyRect.Min.Y += gap

	// Onboarding draws its flow as dialogs and owns no body bands.
	if m.state == uiOnboarding {
		return l
	}

	// Initialize has a main pane but no editor or turn status.
	if m.state == uiInitialize {
		l.main = bodyRect
		return l
	}

	editorHeight := m.editorHeight()

	// Chat reserves a turn-status row directly under the editor; landing
	// has no turn to report on.
	statusRows := 0
	if m.state == uiChat {
		statusRows = turnStatusHeight
	}

	// A blank row between the editor and the turn status, unless the
	// terminal is too short to spend one.
	editorGap := editorGapHeight
	if m.isCompact || statusRows == 0 {
		editorGap = 0
	}

	var mainRect, editorRect, turnStatusRect image.Rectangle
	layout.Vertical(
		layout.Len(max(0, bodyRect.Dy()-editorHeight-statusRows-editorGap)),
		layout.Len(editorHeight),
		layout.Fill(1),
	).Split(bodyRect).Assign(&mainRect, &editorRect, &turnStatusRect)
	turnStatusRect.Min.Y += editorGap

	// Main keeps a blank row above the editor so text never touches it.
	mainRect.Max.Y -= mainBottomGap

	l.main = mainRect
	l.editor = editorRect
	if statusRows > 0 {
		l.turnStatus = turnStatusRect
		l.sessionDetails = common.CenterRect(
			appRect,
			min(appRect.Dx(), sessionDetailsMaxWidth),
			min(sessionDetailsMaxHeight, appRect.Dy()),
		)
	}

	return l
}

// editorHeight is the number of rows the editor band needs: the textarea plus
// its margin, or the active inline dialog's own height.
func (m *UI) editorHeight() int {
	if m.activeInline == nil {
		return m.textarea.Height() + editorHeightMargin
	}

	// The editor content width depends only on terminal width and layout
	// (not on editor height), so passing the current frame's width keeps
	// layout in sync with the width Draw will use, preventing flicker
	// during fast resize.
	width := m.editorContentWidth()
	if qf, ok := m.activeInline.(*dialog.QuestionForm); ok &&
		m.focus != uiFocusEditor && m.shouldCollapseQuestion(qf) {
		return qf.CollapsedHeight() + 1
	}
	return m.activeInline.Height(width)
}

// uiLayout defines the positioning of UI elements.
type uiLayout struct {
	// area is the overall available area.
	area uv.Rectangle

	// header is the wordmark band: the full letterform wall on landing
	// and setup, a single line in chat.
	header uv.Rectangle

	// main is the area for the main pane. (e.x chat, configure, landing)
	main uv.Rectangle

	// editor is the area for the editor pane.
	editor uv.Rectangle

	// turnStatus is the single row under the editor reporting what the
	// current turn is doing. Empty outside chat.
	turnStatus uv.Rectangle

	// status is the area for the status view.
	status uv.Rectangle

	// sessionDetails is the area for the session details overlay.
	sessionDetails uv.Rectangle
}

func (m *UI) openEditor(value string) tea.Cmd {
	tmpfile, err := os.CreateTemp("", "msg_*.md")
	if err != nil {
		return util.ReportError(err)
	}
	tmpPath := tmpfile.Name()
	defer tmpfile.Close() //nolint:errcheck
	if _, err := tmpfile.WriteString(value); err != nil {
		return util.ReportError(err)
	}
	cmd, err := editor.Command(
		"angela",
		tmpPath,
		editor.AtPosition(
			m.textarea.Line()+1,
			m.textarea.Column()+1,
		),
	)
	if err != nil {
		return util.ReportError(err)
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer func() {
			_ = os.Remove(tmpPath)
		}()

		if err != nil {
			return util.ReportError(err)
		}
		content, err := os.ReadFile(tmpPath)
		if err != nil {
			return util.ReportError(err)
		}
		if len(content) == 0 {
			return util.ReportWarn("Message is empty")
		}
		return openEditorMsg{
			Text: strings.TrimSpace(string(content)),
		}
	})
}

// setEditorPrompt configures the textarea prompt function. The prompt is a
// two-column gutter: marker, gap.
func (m *UI) setEditorPrompt(yolo bool) {
	m.textarea.SetPromptFunc(editorPromptWidth, m.editorPromptFunc(yolo))
}

// editorPromptFunc draws the editor gutter for one visual line. The marker
// only appears on the first line, and carries the input mode in its color —
// as does the box border, so no separate accent rail is needed.
func (m *UI) editorPromptFunc(yolo bool) func(textarea.PromptInfo) string {
	t := m.com.Styles
	mode := t.Editor.Rail
	switch {
	case m.bangMode:
		mode = t.Editor.RailBang
	case yolo:
		mode = t.Editor.RailYolo
	}

	return func(info textarea.PromptInfo) string {
		if info.LineNumber != 0 {
			return "  "
		}
		if !info.Focused {
			return t.Editor.PromptMarkerBlurred.Render(editorPromptGlyph)
		}
		return mode.Render(editorPromptGlyph)
	}
}

// closeCompletions closes the completions popup and resets state.
func (m *UI) closeCompletions() {
	m.completionsOpen = false
	m.completionsQuery = ""
	m.completionsStartIndex = 0
	m.completionsTrigger = ""
	m.completions.Close()
}

// openCompletions arms the popup state for a trigger character typed at
// startIndex. The caller supplies the items separately, because agents come
// from config in hand while files have to be walked first.
func (m *UI) openCompletions(trigger string, startIndex int) {
	m.completionsOpen = true
	m.completionsQuery = ""
	m.completionsTrigger = trigger
	m.completionsStartIndex = startIndex
	m.completionsPositionStart = m.completionsPosition()
}

// mentionableAgents returns the agents the coder can be asked to dispatch,
// sorted by ID so the popup is stable across openings. Mentioning the
// primary agent would name the one already reading the message, and hidden
// agents back Angela's own internal calls rather than delegation. Disabled
// agents never reach the resolved map at all.
func (m *UI) mentionableAgents() []completions.AgentCompletionValue {
	cfg := m.com.Config()
	if cfg == nil {
		return nil
	}

	var agents []completions.AgentCompletionValue
	for _, agentCfg := range cfg.Agents {
		if agentCfg.Mode == config.AgentModePrimary || agentCfg.IsHidden() {
			continue
		}
		agents = append(agents, completions.AgentCompletionValue{ID: agentCfg.ID})
	}
	slices.SortFunc(agents, func(a, b completions.AgentCompletionValue) int {
		return strings.Compare(a.ID, b.ID)
	})
	return agents
}

// insertCompletionText replaces the trigger word in the textarea with the
// given text. The trigger character is part of what gets replaced, so a
// caller that wants to keep it has to include it in text.
func (m *UI) insertCompletionText(text string) bool {
	value := m.textarea.Value()
	if m.completionsStartIndex > len(value) {
		return false
	}

	word := m.textareaWord()
	endIdx := min(m.completionsStartIndex+len(word), len(value))
	newValue := value[:m.completionsStartIndex] + text + value[endIdx:]
	m.textarea.SetValue(newValue)
	m.textarea.MoveToEnd()
	m.textarea.InsertRune(' ')
	return true
}

// insertAgentCompletion writes the mention into the textarea. The "@" is
// kept: unlike a file, whose contents ride along as an attachment, the
// mention is only ever the text the coder reads.
func (m *UI) insertAgentCompletion(id string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText("@" + id) {
		return nil
	}
	return m.handleTextareaHeightChange(prevHeight)
}

// insertFileCompletion inserts the selected file path into the textarea,
// replacing the trigger word, and adds the file as an attachment.
func (m *UI) insertFileCompletion(path string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(path) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	fileCmd := func() tea.Msg {
		absPath, _ := filepath.Abs(path)

		if m.hasSession() {
			// Skip attachment if file was already read and hasn't been modified.
			lastRead := m.com.Workspace.FileTrackerLastReadTime(context.Background(), m.session.ID, absPath)
			if !lastRead.IsZero() {
				if info, err := os.Stat(path); err == nil && !info.ModTime().After(lastRead) {
					return nil
				}
			}
		} else if slices.Contains(m.sessionFileReads, absPath) {
			return nil
		}

		m.sessionFileReads = append(m.sessionFileReads, absPath)

		// Add file as attachment.
		content, err := os.ReadFile(path)
		if err != nil {
			// If it fails, let the LLM handle it later.
			return nil
		}

		return message.Attachment{
			FilePath: path,
			FileName: filepath.Base(path),
			MimeType: mimeOf(content),
			Content:  content,
		}
	}
	return tea.Batch(heightCmd, fileCmd)
}

// insertMCPResourceCompletion inserts the selected resource into the textarea,
// replacing the @query, and adds the resource as an attachment.
func (m *UI) insertMCPResourceCompletion(item completions.ResourceCompletionValue) tea.Cmd {
	displayText := cmp.Or(item.Title, item.URI)

	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(displayText) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	resourceCmd := func() tea.Msg {
		contents, err := m.com.Workspace.ReadMCPResource(
			context.Background(),
			item.MCPName,
			item.URI,
		)
		if err != nil {
			slog.Warn("Failed to read MCP resource", "uri", item.URI, "error", err)
			return nil
		}
		if len(contents) == 0 {
			return nil
		}

		content := contents[0]
		var data []byte
		if content.Text != "" {
			data = []byte(content.Text)
		} else if len(content.Blob) > 0 {
			data = content.Blob
		}
		if len(data) == 0 {
			return nil
		}

		mimeType := item.MIMEType
		if mimeType == "" && content.MIMEType != "" {
			mimeType = content.MIMEType
		}
		if mimeType == "" {
			mimeType = "text/plain"
		}

		return message.Attachment{
			FilePath: item.URI,
			FileName: displayText,
			MimeType: mimeType,
			Content:  data,
		}
	}
	return tea.Batch(heightCmd, resourceCmd)
}

// completionsPosition returns the X and Y position for the completions popup.
func (m *UI) completionsPosition() image.Point {
	cur := m.textarea.Cursor()
	if cur == nil {
		return image.Point{
			X: m.layout.editor.Min.X,
			Y: m.layout.editor.Min.Y,
		}
	}
	return image.Point{
		X: cur.X + m.layout.editor.Min.X,
		Y: m.layout.editor.Min.Y + cur.Y,
	}
}

// textareaWord returns the current word at the cursor position.
func (m *UI) textareaWord() string {
	return m.textarea.Word()
}

// isWhitespace returns true if the byte is a whitespace character.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// isAgentBusy returns true if the agent coordinator exists and is currently
// busy processing a request. It only reads the memoized state (it runs in
// per-message paths like the textarea placeholder, where a workspace probe
// would be an HTTP round-trip per keystroke in client/server mode); the
// value is refreshed off-thread, see workspace_cache.go.
func (m *UI) isAgentBusy() bool {
	if m.bangCancel != nil {
		return true
	}
	return m.agentBusyCache.val
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
// mode. Narrow terminals get the bare prompt without the hint tail.
func (m *UI) editorPlaceholder() string {
	// A pending jump is transient and directly actionable, so it speaks
	// over the mode prompts for the couple of seconds it lasts.
	if m.isJumpingToBottom {
		return "Press ↓ again to jump to the latest message"
	}
	if m.bangMode {
		return "Run a shell command"
	}
	if m.yoloModeCached() {
		return "Yolo mode — permissions are skipped"
	}
	if m.width < narrowWidthBreakpoint {
		return "Ask anything…"
	}
	return "Ask anything — / for commands, @ for agents, # for files, ↓↓ for latest"
}

// editorCaption returns the one-line run context: which agent and model the
// next message will go to, plus the input mode when it is not the ordinary
// one. The text is unstyled; the caller owns how it is drawn.
func (m *UI) editorCaption(width int) string {
	if width <= 0 {
		return ""
	}

	var modelName, agentName string
	if active := m.activeAgent(); active != nil {
		modelName = active.CatwalkCfg.Name
		agentName = active.AgentName
	}

	var mode string
	switch {
	case m.bangMode:
		mode = "shell"
	case m.yoloModeCached():
		mode = "yolo"
	}

	// Narrow terminals keep only what identifies the run: the model, and
	// the mode when it is escalated.
	fields := []string{agentName, modelName, mode}
	if width < narrowWidthBreakpoint {
		fields = []string{modelName, mode}
	}

	var parts []string
	for _, f := range fields {
		if f != "" {
			parts = append(parts, f)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ansi.Truncate(strings.Join(parts, " · "), width, "…")
}

// editorBorderStyle is the box border color. It carries the input mode, which
// is why the gutter no longer needs an accent rail.
func (m *UI) editorBorderStyle() lipgloss.Style {
	t := m.com.Styles
	switch {
	case m.bangMode:
		return t.Editor.RailBang
	case m.yoloModeCached():
		return t.Editor.RailYolo
	case m.focus == uiFocusEditor:
		return t.Editor.BorderFocused
	default:
		return t.Editor.Border
	}
}

// drawPromptBox draws the input as a bordered box with the attachments strip
// above it and the run context inset into its bottom border.
func (m *UI) drawPromptBox(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() <= editorBoxBorders || area.Dy() <= editorBoxBorders {
		return
	}

	if len(m.attachments.List()) > 0 {
		uv.NewStyledString(m.attachments.Render(area.Dx())).Draw(scr, image.Rect(
			area.Min.X, area.Min.Y, area.Max.X, area.Min.Y+1,
		))
	}

	// The box occupies the band below the attachments row.
	box := image.Rect(area.Min.X, area.Min.Y+1, area.Max.X, area.Max.Y)
	top, bottom, right := box.Min.Y, box.Max.Y-1, box.Max.X-1
	span := strings.Repeat("─", box.Dx()-editorBoxBorders)
	border := list.ToStyle(m.editorBorderStyle())

	common.SetSpan(scr, box.Min.X, top, border, "╭"+span+"╮")
	common.SetSpan(scr, box.Min.X, bottom, border, "╰"+span+"╯")
	for y := top + 1; y < bottom; y++ {
		common.SetSpan(scr, box.Min.X, y, border, "│")
		common.SetSpan(scr, right, y, border, "│")
	}

	content := m.textarea.View()
	if m.viewingSubAgent() {
		content = m.subAgentNotice(box.Dx() - editorBoxBorders)
	}
	uv.NewStyledString(content).Draw(scr, image.Rect(
		box.Min.X+1, top+1, right, bottom,
	))

	m.drawPromptLabel(scr, box, bottom)
}

// subAgentNotice replaces the prompt while a sub-agent's transcript is on
// screen, so the box reads as a closed door rather than an empty one.
func (m *UI) subAgentNotice(width int) string {
	notice := "Sub-agent transcript · read only · esc to go back"
	return m.com.Styles.Editor.Caption.Render(ansi.Truncate(notice, width, "…"))
}

// drawPromptLabel writes the run context onto the bottom border row. The
// spaces the label pads itself with overwrite the border glyphs, so it reads
// as inset into the border rather than printed over it.
func (m *UI) drawPromptLabel(scr uv.Screen, box uv.Rectangle, row int) {
	// Two columns of border plus the two spaces the label pads itself with.
	caption := m.editorCaption(box.Dx() - 4)
	if caption == "" {
		return
	}
	label := " " + caption + " "
	x := box.Max.X - 1 - ansi.StringWidth(label)
	if x <= box.Min.X {
		return
	}
	common.SetSpan(scr, x, row, list.ToStyle(m.com.Styles.Editor.BottomLabel), label)
}

// attachSkill reads a skill's content by ID and returns it as a markdown
// attachment to be added to the attachment toolbar. The user can then
// compose a message and send it with the skill attached.
// The name parameter is used as a fallback when the server does not
// return one.
func (m *UI) attachSkill(skillID, name string) tea.Cmd {
	return func() tea.Msg {
		content, result, err := m.com.Workspace.ReadSkill(context.Background(), skillID)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		fileName := result.Name
		if fileName == "" {
			fileName = name
		}
		return message.Attachment{
			FilePath: fileName,
			FileName: fileName,
			MimeType: "text/markdown",
			Content:  content,
		}
	}
}

// sendMessage sends a message with the given content and attachments.
func (m *UI) sendMessage(content string, attachments ...message.Attachment) tea.Cmd {
	if m.viewingSubAgent() {
		return util.ReportWarn("This is a sub-agent's transcript. Press esc to go back and reply there.")
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
	m.busyFetchGen++
	m.invalidatePromptQueue()
	cmds = append(cmds, func() tea.Msg {
		// AgentRun is fire-and-forget: it returns once the prompt has
		// been accepted (HTTP 202) or synchronously with a validation
		// or transport error. Run failures and cancellation surface
		// through SSE-derived events, not this return value.
		err := m.com.Workspace.AgentRun(context.Background(), sessionID, content, attachments...)
		if err != nil && !errors.Is(err, context.Canceled) {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("%v", err),
			}
		}
		return agentRunSubmittedMsg{}
	})
	return tea.Batch(cmds...)
}

// runShellCommand executes a shell command server-side without triggering
// the LLM. The result is displayed as a tool-style item in the chat.
func (m *UI) runShellCommand(command string) tea.Cmd {
	return m.runShellCommandInternal(command, false)
}

// runShellCommandInternal is the shared implementation for bang-mode shell
// execution. isFirstMessage indicates the command is the first user message
// in a newly created session, which triggers title generation.
func (m *UI) runShellCommandInternal(command string, isFirstMessage bool) tea.Cmd {
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
		// Defer shell execution until loadSessionMsg fires so the chat
		// list is stable before we add items or start streaming.
		m.pendingBangCommand = command
		return tea.Batch(cmds...)
	}

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
		resp, err := m.com.Workspace.AgentRunShellCommand(ctx, sessionID, command, contentWidth, onProgress, isFirstMessage)
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

const jumpToBottomTimerDuration = 2 * time.Second

// jumpToBottomTimerCmd creates a command that expires the jump-to-bottom
// timer.
func jumpToBottomTimerCmd() tea.Cmd {
	return tea.Tick(jumpToBottomTimerDuration, func(time.Time) tea.Msg {
		return jumpToBottomTimerExpiredMsg{}
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
	// is a synchronous HTTP round-trip in client/server mode.
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

		// An idle branch is abandoned by this press rather than merely
		// interrupted, so the view follows it back to the parent that was
		// waiting on it.
		leaving := m.cancelLeavesBranch()

		m.com.Workspace.AgentCancel(m.session.ID)
		// Stop the spinning turn indicator and drop the memoized busy
		// state the cancel just changed; the turn status re-renders from
		// last-known state and again when the off-thread refresh (and
		// the agent's own events) land.
		m.turnIsSpinning = false
		m.invalidateBusyCaches()
		if leaving {
			return tea.Batch(m.leaveSubSession(), m.dispatchBusyRefresh())
		}
		return m.dispatchBusyRefresh()
	}

	// Queued prompts pending: esc clears the queue. Decide from the cached
	// count (event-driven) instead of a synchronous workspace probe.
	if m.promptQueue > 0 {
		m.com.Workspace.AgentClearQueue(m.session.ID)
		m.promptQueue = 0
		m.promptQueueItems = nil
		m.promptQueueCheckedAt = time.Now()
		// Bump the queue generation so a fetch started before this clear
		// cannot land and repopulate the pill we just emptied.
		m.invalidatePromptQueue()
		m.updateLayoutAndSize()
		return nil
	}

	// First escape press - set canceling state and start timer.
	m.isCanceling = true
	return cancelTimerCmd()
}

// openDialog opens a dialog by its ID.
func (m *UI) openDialog(id string) tea.Cmd {
	var cmds []tea.Cmd
	switch id {
	case dialog.SessionsID:
		if cmd := m.openSessionsDialog(); cmd != nil {
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

	commands, err := dialog.NewCommands(m.com, sessionID, hasSession, hasTodos, hasQueue, m.viewingBranch(), m.activeAgent(), m.customCommands, m.mcpPrompts)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(commands)

	return commands.InitialCmd()
}

// openAgentsDialog opens the primary-agent picker for the current
// session. There is nothing to switch without a session: the agent
// belongs to the session, not to the global config.
func (m *UI) openAgentsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.AgentsID) {
		m.dialog.BringToFront(dialog.AgentsID)
		return nil
	}
	if m.session == nil {
		return util.ReportWarn("Start a session before switching agents.")
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

// openSessionsDialog opens the sessions dialog. If the dialog is already open,
// it brings it to the front. Otherwise, it will list all the sessions and open
// the dialog.
func (m *UI) openSessionsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SessionsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.SessionsID)
		return nil
	}

	selectedSessionID := ""
	if m.session != nil {
		selectedSessionID = m.session.ID
	}

	dialog, err := dialog.NewSessions(m.com, selectedSessionID)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(dialog)
	return nil
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
func (m *UI) handlePermissionNotification(notification permission.PermissionNotification) {
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
	// covers the case where another client resolved the request remotely.
	if !notification.Granted && !notification.Denied {
		return
	}
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.ToolCallID() == notification.ToolCallID {
			m.dialog.CloseDialog(dialog.PermissionsID)
		}
	}
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
		// busy/queue refresh below.
	case notify.TypeReAuthenticate:
		return m.handleReAuthenticate(n.ProviderID)
	case notify.TypeAWSSSOAuth:
		return m.handleAWSSSOAuth(n.AWSSOCommand, n.AWSSOURL)
	case notify.TypeAWSSSOAuthResult:
		return m.handleAWSSSOAuthResult(n.Message)
	default:
		return nil
	}
	// TypeAgentFinished / TypeAgentError are the busy→idle edge: the agent
	// clears its active request before publishing precisely so observers
	// can re-probe. Drop the memoized busy state and re-fetch it and the
	// prompt queue off-thread.
	m.invalidateBusyCaches()
	m.invalidatePromptQueue()
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
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
	return m.openAuthenticationDialog(providerCfg.ToProvider(), active.ModelCfg, active.ModelName), true
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
	m.setEditorPrompt(m.yoloModeCached())
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

var pasteRE = regexp.MustCompile(`paste_(\d+).txt`)

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

// renderLogo renders the Angela letterform wall at the given width.
func renderLogo(t *styles.Styles, width int) string {
	return logo.Render(t.Logo.GradCanvas, version.Version, false, logo.Opts{
		TitleColorA:  t.Logo.TitleColorA,
		TitleColorB:  t.Logo.TitleColorB,
		VersionColor: t.Logo.VersionColor,
		Width:        width,
	})
}
