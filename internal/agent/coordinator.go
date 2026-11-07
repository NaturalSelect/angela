package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/browserhttp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// Coordinator errors.
//
// The exported ones are what a caller can get wrong: they name an
// agent or a preset that does not fit, ask for a model change a session
// may not make, or mix edits that land in different places. Transports
// answer them as a bad request, so they have to be nameable from
// outside this package. The rest describe a broken configuration or a
// broken process, which is nobody's request to fix.
var (
	ErrAgentNotAvailable   = errors.New("agent not available")
	ErrVariantNotAvailable = errors.New("model variant not available")

	// ErrSubAgentModelLocked reports a session-level model edit aimed at a
	// sub-agent's session. Its model follows the session tree, so the
	// change belongs on the agent, not on this session.
	ErrSubAgentModelLocked = errors.New("a sub-agent's model follows its session tree; switch the agent's model instead")

	// ErrMixedActiveAgentEdit reports an edit that changes the session
	// tree's picks and the session's own state at once. The two land on
	// different sessions, so one request cannot be atomic across them.
	ErrMixedActiveAgentEdit = errors.New("an edit cannot change both the session tree's agent picks and the session's own model")

	errCoderAgentNotConfigured    = errors.New("coder agent not configured")
	errModelProviderNotConfigured = errors.New("model provider not configured")
	errModelNotSelected           = errors.New("model config not selected")
	errModelNotFound              = errors.New("model not found in provider config")
)

type Coordinator interface {
	// INFO: (kujtim) this is not used yet we will use this when we have multiple agents
	// SetMainAgent(string)
	Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	// RunAccepted runs a call that was already accepted via
	// BeginAccepted. The handle is the only carrier of accept-state
	// across the Coordinator / sessionAgent.Run layers: it reaches
	// sessionAgent.Run as SessionAgentCall.Accepted, where it is
	// consumed under dispatchMu once the accepted -> (cancel-on-entry |
	// queued | active) transition is chosen.
	RunAccepted(ctx context.Context, accept *AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	// BeginAccepted reserves the accept slot the handle above carries.
	// For a child session this process has not dispatched itself it
	// also rebuilds the session's route, which costs one database read
	// and one agent resolution, once per child session per process.
	BeginAccepted(ctx context.Context, sessionID string) *AcceptedRun
	// LockSession resolves sessionID's executor (rebuilding a child
	// session's route from the database if this process has not
	// dispatched on it itself yet, the same as BeginAccepted) and
	// blocks until it can guarantee the session is not about to
	// transition to a new active turn, returning a release func the
	// caller must call exactly once. ok is false when the session has
	// no resolvable executor, meaning nothing runs there and there is
	// nothing to guard against.
	LockSession(ctx context.Context, sessionID string) (release func(), ok bool)
	// Cancel interrupts a running turn. On a parent suspended waiting on
	// one or more branches it reaches through and interrupts those too,
	// since the parent has nothing of its own running — but it never
	// abandons a branch, on the branch itself or through its parent.
	// AbandonBranch is the only call that does that.
	Cancel(sessionID string)
	// AbandonBranch gives a branch up whether or not a turn is running,
	// releasing the parent call suspended on it. This is the only call
	// that ever abandons a branch — Cancel never does, on the branch or
	// on the parent it suspends — so a user always reaches this outcome
	// by naming it outright.
	AbandonBranch(sessionID string) bool
	CancelAll()
	IsSessionBusy(sessionID string) bool
	// IsSessionBranch reports whether a session is a branch this
	// process still has a parent tool call suspended on. It asks the
	// rendezvous, not the config: an agent configured in branch mode
	// says nothing about whether this process is holding a turn open
	// for it, and a restart leaves the config behind while the
	// suspended call it described is gone.
	IsSessionBranch(sessionID string) bool
	// BackgroundAgentsRunning reports how many background agents are
	// running in a session or in any session below it. A background agent
	// outlives the turn that launched it, so this is what tells an idle
	// session from one that is still working.
	BackgroundAgentsRunning(sessionID string) int
	// SubSessions lists the direct children of a session in dispatch
	// order. Children that an earlier process dispatched are read from the
	// database the first time a session is asked about, and reported as
	// ended, because how they finished was not kept.
	SubSessions(ctx context.Context, parentSessionID string) ([]tools.SubSession, error)
	IsBusy() bool
	QueuedPrompts(sessionID string) int
	QueuedPromptsList(sessionID string) []message.QueuedPrompt
	// TakeQueuedPrompts atomically removes and returns every
	// user-queued prompt for sessionID, complete with attachment
	// bytes. See SessionAgent.TakeQueuedPrompts.
	TakeQueuedPrompts(sessionID string) []message.QueuedPrompt
	ClearQueue(sessionID string)
	Summarize(ctx context.Context, sessionID string) error
	// AskSideQuestion answers a one-off question from a session's
	// existing context, concurrently with any turn already running on
	// it, without adding the question or its answer to the session's
	// message history.
	AskSideQuestion(ctx context.Context, sessionID, question string) (string, error)
	// GenerateCommitMessage writes a commit message describing diff
	// (the output of `git diff --cached`), using a tool-free internal
	// agent. It never reads or writes sessionID's message history.
	GenerateCommitMessage(ctx context.Context, sessionID, diff string) (string, error)

	// DefaultModel reports what a brand new session would run on.
	// Callers asking what a specific session runs want ActiveAgent.
	DefaultModel() Model

	// ActiveAgent reports the agent instance a session owns, together
	// with the model resolved from it. An empty sessionID answers with
	// the configured default.
	ActiveAgent(ctx context.Context, sessionID string) (config.ActiveAgent, Model, error)

	UpdateModels(ctx context.Context) error
	GenerateTitle(ctx context.Context, sessionID, prompt string)
	// RegenerateTitle names sessionID again from the prompt that opened
	// it, for when the automatic attempt failed. Unlike GenerateTitle it
	// reports failure and keeps the current title when it does. It
	// returns ErrSessionBusy while the session runs a turn or is being
	// titled, since a compaction would otherwise save over the new title.
	RegenerateTitle(ctx context.Context, sessionID string) error
	GenerateAgent(ctx context.Context, description string) (config.Agent, string, error)

	// SwitchAgent points the session at a different primary agent from
	// the next turn on, and records the switch in the transcript.
	SwitchAgent(ctx context.Context, sessionID, agentID string) error
	SwitchVariant(ctx context.Context, sessionID, variant string) error

	// EditActiveAgent is the general form of the two above: it moves
	// any combination of the session's agent, model, preset and
	// thinking flag in one atomic edit, and reports the instance the
	// session ends up on.
	EditActiveAgent(ctx context.Context, sessionID string, edit config.ActiveAgentEdit) (config.ActiveAgent, error)

	// AdoptDraft copies the landing page's draft pick onto sessionID,
	// a session that was just created, so a model or preset chosen
	// before any session existed carries into the first one instead
	// of being silently discarded. A no-op when the draft was never
	// touched.
	AdoptDraft(ctx context.Context, sessionID string) error
}

type coordinator struct {
	cfg         *config.ConfigStore
	sessions    session.Service
	messages    message.Service
	permissions permission.Service
	questions   question.Service
	history     history.Service
	filetracker filetracker.Service
	lspManager  *lsp.Manager
	notify      pubsub.Publisher[notify.Notification]
	runComplete pubsub.Publisher[notify.RunComplete]
	interactive bool
	// noBranchAgents refuses branch forks and background agents even in an
	// interactive session, for front ends that cannot hand a conversation
	// to a sub-session.
	noBranchAgents bool

	// rootExecutor runs every top-level session. Which agent a turn uses is
	// decided per session by its ActiveAgent, not by this field.
	rootExecutor SessionAgent
	subagents    *subagentRegistry

	// subagentRoutes maps a child session ID to the executor that owns it.
	subagentRoutes *csync.Map[string, subagentRoute]

	// branches holds the rendezvous for every dispatch currently
	// suspended on a branch session.
	branches *branchController

	// background tracks the agents launched with run_in_background and
	// notices holds what they have to tell the session that launched
	// them. Both are nil on a coordinator built without them, which
	// then does not offer background agents (see
	// backgroundAgentsAvailable); every use outside the launch path is
	// nil-safe.
	background *backgroundAgents
	notices    *noticeInbox

	// subSessions records every sub-session this process dispatched, whether
	// it runs in the foreground, in the background or as a branch. It is nil
	// on a hand-built coordinator, and every use is nil-safe.
	subSessions *subSessionTree

	// lifetime outlives any single turn. A wake runs on it, because the
	// turn that happens to be finishing is not the one that owns the
	// work. Nil on a hand-built coordinator, which never wakes.
	lifetime context.Context

	// proposals holds the document each branch is drafting, which is
	// what its merge hands back.
	proposals *tools.ProposalStore

	// active holds each session's own agent instance. Everything a
	// user changes at runtime lands here, never in cfg. The zero value
	// is ready to use.
	active activeAgentStore

	// titles serializes the automatic and the manual title writers per
	// session. The zero value is ready to use.
	titles titleClaims

	// NOTE: Nil only in test struct literals; NewCoordinator always sets it.
	skills       *skills.Manager
	skillTracker *skills.Tracker

	// images stores generated images for the built-in ImageGenerate
	// and ImageEdit tools; imageSupport reports whether the connected
	// client has ever proven it can render them. Both are nil-safe: a
	// nil images, or a nil or false-returning imageSupport, simply
	// keeps buildTools from registering the two tools. See
	// imageToolsAvailable.
	images       images.Service
	imageSupport func() bool

	// reload backs the AngelaReload tool. Nil leaves the tool unregistered.
	reload func(context.Context) (string, error)
}

// CoordinatorOptions holds the dependencies for NewCoordinator. Using a
// struct keeps the constructor self-documenting and avoids a long
// positional parameter list.
type CoordinatorOptions struct {
	Config       *config.ConfigStore
	Sessions     session.Service
	Messages     message.Service
	Permissions  permission.Service
	Questions    question.Service
	History      history.Service
	FileTracker  filetracker.Service
	LSPManager   *lsp.Manager
	Notify       pubsub.Publisher[notify.Notification]
	RunComplete  pubsub.Publisher[notify.RunComplete]
	Skills       *skills.Manager
	Images       images.Service
	ImageSupport func() bool
	// Reload backs the AngelaReload tool. Nil leaves the tool unregistered.
	Reload      func(context.Context) (string, error)
	Interactive bool
	// NoBranchAgents disables branch agents and background agents while
	// leaving the other interactive tools, such as question, available.
	NoBranchAgents bool
}

func NewCoordinator(ctx context.Context, opts CoordinatorOptions) (Coordinator, error) {
	// Skills are pre-discovered by the caller (see app.New) and passed in
	// via the manager. If no manager was provided (legacy callers), fall
	// back to an in-line discovery so the coordinator still works.
	skillsMgr := opts.Skills
	if skillsMgr == nil {
		allSkills, activeSkills := discoverSkills(opts.Config)
		skillsMgr = skills.NewManager(allSkills, activeSkills, nil)
	}
	skillTracker := skills.NewTracker(skillsMgr.ActiveSkills())

	c := &coordinator{
		cfg:            opts.Config,
		sessions:       opts.Sessions,
		messages:       opts.Messages,
		permissions:    opts.Permissions,
		questions:      opts.Questions,
		history:        opts.History,
		filetracker:    opts.FileTracker,
		lspManager:     opts.LSPManager,
		notify:         opts.Notify,
		runComplete:    opts.RunComplete,
		subagents:      newSubagentRegistry(),
		subagentRoutes: csync.NewMap[string, subagentRoute](),
		branches:       newBranchController(),
		background:     newBackgroundAgents(),
		subSessions:    newSubSessionTree(),
		notices:        newNoticeInbox(),
		lifetime:       ctx,
		proposals:      tools.NewProposalStore(),
		skills:         skillsMgr,
		skillTracker:   skillTracker,
		images:         opts.Images,
		imageSupport:   opts.ImageSupport,
		reload:         opts.Reload,
		interactive:    opts.Interactive,
		noBranchAgents: opts.NoBranchAgents,
	}

	// No agent is bound here. Which agent drives a turn is read from
	// the session's own ActiveAgent on every turn, so a session
	// switched to another primary takes effect without rebuilding
	// anything. The coder is checked only because it is the fallback
	// every session lands on when its own agent is unset or has since
	// disappeared from config.
	coderCfg, ok := opts.Config.Config().Agents[config.AgentCoder]
	if !ok {
		return nil, errCoderAgentNotConfigured
	}
	if coderCfg.Mode != config.AgentModePrimary {
		slog.Warn("Coder agent is not in primary mode; sessions falling back to it will run it as primary anyway",
			"mode", coderCfg.Mode)
	}

	// Populate the dispatch table before the first turn resolves its
	// tool list, which reaches agentTool and reads this table.
	// Reconcile only snapshots config, so the subagents themselves are
	// still built lazily on first dispatch.
	c.reconcileSubagents()

	c.rootExecutor = c.buildAgent(config.AgentCoder, false)
	go c.forgetDeletedSessions(ctx, opts.Sessions.Subscribe(ctx))
	return c, nil
}

// forgetDeletedSessions evicts a session's cached agent and routing entry
// as the session goes away. Deletion runs through several entry points
// (CLI and workspace), so the stores follow the event instead of
// asking each of them to remember; a surviving entry would keep answering
// for a session that no longer exists.
func (c *coordinator) forgetDeletedSessions(ctx context.Context, events <-chan pubsub.Event[session.Session]) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == pubsub.DeletedEvent {
				c.active.forget(ev.Payload.ID)
				c.forgetSubagentRoute(ev.Payload.ID)
				c.background.sessionDeleted(ev.Payload.ID)
				c.subSessions.sessionDeleted(ev.Payload.ID)
				c.notices.Close(ev.Payload.ID)
			}
		}
	}
}

// Run implements Coordinator.
func (c *coordinator) Run(ctx context.Context, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return c.run(ctx, nil, sessionID, prompt, attachments...)
}

// RunAccepted implements Coordinator.
func (c *coordinator) RunAccepted(ctx context.Context, accept *AcceptedRun, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return c.run(ctx, accept, sessionID, prompt, attachments...)
}

// run is the shared implementation behind Run and RunAccepted. When
// accept is non-nil it is threaded onto the SessionAgentCall as
// Accepted so sessionAgent.Run can consume the accept reservation under
// dispatchMu; when nil no accept tracking applies.
func (c *coordinator) run(ctx context.Context, accept *AcceptedRun, sessionID string, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	// MCP servers connect asynchronously (see mcp.Initialize).
	//
	// Interactive runs never wait for that to finish: the tool list below
	// is built from whatever is registered right now, servers still
	// connecting are simply absent from this run's palette, and they are
	// picked up by later runs once they register and publish
	// EventToolsListChanged. Blocking here froze the TUI for the duration
	// of the slowest server's connect timeout whenever a prompt was sent
	// before initialization finished — most visibly on the first message.
	//
	// Non-interactive runs get a single shot at the tool palette, so they
	// do wait for initialization to settle. The wait is bounded by each
	// server's own connect timeout, so a hung server cannot stall the run
	// indefinitely.
	if !c.interactive {
		if err := mcp.WaitForInit(ctx); err != nil {
			return nil, fmt.Errorf("failed to wait for MCP initialization: %w", err)
		}
	}

	// Bring the dispatch table in line with the config as it stands now.
	// An agent whose permissions changed is replaced wholesale, so the
	// next dispatch rebuilds it against the new config rather than
	// reusing a cached agent that still holds revoked tools.
	c.reconcileSubagents()

	target, err := c.turnExecutorFor(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	resolved := target.resolved

	model := resolved.Model
	maxTokens := resolved.MaxTokens

	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return nil, errModelProviderNotConfigured
	}

	mergedOptions, temp, topP, topK, freqPenalty, presPenalty := mergeCallOptions(model, providerCfg, buildPromptCacheKey(sessionID, resolved.ID))
	compact := c.compactFor(ctx, sessionID, resolved.Host)

	if err := c.refreshTokenIfExpired(ctx, providerCfg); err != nil {
		// NOTE(@andreynering): We don't return here because the event handling to ask the user to reauthenticate
		// depends on the flow below. If refresh fails, proceed with the token we have.
		slog.Error("Failed to refresh OAuth2 token. Proceeding with existing token.", "error", err)
	}

	// Coalesce per-attempt RunComplete payloads so only the final
	// outcome reaches subscribers. Without this, the first attempt's
	// failed RunComplete (unauthorized) would race ahead of the
	// retry's success, and `angela run` would exit on the stale error
	// before ever seeing the retry result. Each attempt's
	// SessionAgentCall.OnComplete hook overwrites latest; we publish
	// exactly once after retries resolve, via PublishMustDeliver, so
	// a momentarily-full subscriber buffer can't silently drop the
	// terminal event.
	var (
		latest    notify.RunComplete
		hasLatest bool
	)
	onComplete := func(rc notify.RunComplete) {
		latest = rc
		hasLatest = true
	}
	// Propagate the caller-supplied RunID (set via agent.WithRunID on
	// ctx) onto the SessionAgentCall so the terminal RunComplete event
	// echoes it back. Both attempts in the retry chain reuse the same
	// RunID; the coalesce closure publishes the final outcome under that
	// same correlator.
	runID := RunIDFromContext(ctx)
	run := func() (*fantasy.AgentResult, error) {
		return target.executor.Run(ctx, SessionAgentCall{
			SessionID:                sessionID,
			Agent:                    resolved,
			RunID:                    runID,
			Prompt:                   prompt,
			Attachments:              attachments,
			MaxOutputTokens:          maxTokens,
			ProviderOptions:          mergedOptions,
			SummarizeProviderOptions: compact.options,
			Compact:                  compact.agent,
			SummarizeOnAuthRefresh:   compact.onAuthRefresh,
			Temperature:              temp,
			TopP:                     topP,
			TopK:                     topK,
			FrequencyPenalty:         freqPenalty,
			PresencePenalty:          presPenalty,
			OnComplete:               onComplete,
			Accepted:                 accept,
			OnAuthRefresh:            c.makeAuthRefreshCallback(providerCfg),
			Resolve:                  target.resolve,
			noticeWake:               noticeWakeFromContext(ctx),
		})
	}
	if target.child {
		run = c.rollingUpCost(ctx, sessionID, run)
	}
	beforeLoaded := c.skillTracker.LoadedNames()
	result, originalErr := run()
	logTurnSkillUsage(sessionID, prompt, c.activeSkills(), c.skillTracker, beforeLoaded)

	if hasLatest && c.runComplete != nil {
		c.runComplete.PublishMustDeliver(ctx, pubsub.UpdatedEvent, latest)
		// Mark the run as having published its authoritative terminal
		// RunComplete (see RunCompletePublished), so a caller does not
		// publish a duplicate fallback for the error it is about to
		// receive.
		MarkRunCompletePublished(ctx)
	}
	return result, originalErr
}

// compactCall bundles the compact agent with everything its own
// stream needs from its provider. Compaction can run on a different
// provider than the turn that triggers it, so deriving these from the
// running model would hand one provider's options to another and, on
// a 401, refresh the wrong credentials while the real failure stands.
//
// provider, options and onAuthRefresh are only meaningful when ready
// is true. A compact agent that did not resolve, or whose provider is
// not configured, yields the rest zero: a turn still runs, and only
// compaction itself fails, which is what the previous shape did too.
type compactCall struct {
	agent         resolvedAgent
	provider      config.ProviderConfig
	options       fantasy.ProviderOptions
	onAuthRefresh func(context.Context, *fantasy.ProviderError) error
	ready         bool
}

// compactFor resolves the compact agent for a session and derives the
// provider settings that belong to it.
func (c *coordinator) compactFor(ctx context.Context, sessionID string, host config.ActiveAgent) compactCall {
	call := compactCall{agent: c.buildCompactAgent(ctx, host)}
	if !call.agent.Available() {
		return call
	}
	providerCfg, ok := c.cfg.Config().Providers.Get(call.agent.Model.ModelCfg.Provider)
	if !ok {
		return call
	}
	call.provider = providerCfg
	// The prompt cache key uses the resolved compact agent's own ID
	// rather than the running agent's name: summarize sends a
	// different system prompt than a normal turn, and two different
	// compact agents (a custom one and the built-in one) send
	// different prompts from each other too, so neither may share a
	// cache route with the other.
	call.options = getProviderOptions(call.agent.Model, providerCfg, buildPromptCacheKey(sessionID, call.agent.ID))
	call.onAuthRefresh = c.makeAuthRefreshCallback(providerCfg)
	call.ready = true
	return call
}

// buildAgent creates the executor an agent runs on. The model, tools
// and system prompt are deliberately absent: they are resolved per turn
// and arrive on the SessionAgentCall, so nothing here can be rewritten
// underneath a turn already in flight. Only the ID is taken, and it is
// a label — the primary executor serves whichever agent the session is
// currently pointed at.
func (c *coordinator) buildAgent(agentID string, isSubAgent bool) SessionAgent {
	return NewSessionAgent(SessionAgentOptions{
		IsSubAgent:    isSubAgent,
		AgentID:       agentID,
		Compaction:    c.cfg.Config().Options.Compaction,
		IsYolo:        c.permissions.Mode() == permission.ModeYolo,
		Sessions:      c.sessions,
		Messages:      c.messages,
		Notify:        c.notify,
		RunComplete:   c.runComplete,
		GenerateTitle: c.generateSessionTitle,
		SkillTracker:  c.skillTracker,
		Reminders:     c.cfg.Config().Options.Reminders,

		Notices:        c.notices,
		WakeForNotices: c.wakeForNotices,
		RunningBackgroundAgents: func(sessionID string) []backgroundAgentInfo {
			return c.background.running(sessionID)
		},
	})
}

// buildTools assembles the tool set for a turn. modelName is the model
// the turn actually resolved to; it reaches the bash tool's commit
// attribution, which must name the model that did the work rather than
// whatever the global slot happens to point at.
//
// parent is this turn's own instance, passed to the agent tool so a
// further dispatch whose slot is "inherited" can inherit this turn's
// live model rather than the coder's.
//
// depth is this turn's dispatch depth (0 for a primary turn, 1+ for a
// subagent). It gates both the question tool and, against
// Options.SubagentMaxDepth, the agent tool: a turn only holds it while
// it still has delegation budget left, which keeps the dispatch chain
// from growing past the configured limit.
func (c *coordinator) buildTools(agent config.Agent, parent config.ActiveAgent, modelName string, depth int) ([]fantasy.AgentTool, error) {
	var allTools []fantasy.AgentTool
	isSubAgent := depth > 0
	canDelegate := depth < c.cfg.Config().Options.SubagentMaxDepth()
	var manageBackgroundAgents bool

	if canDelegate && agent.AllowedTools.Allows(toolnames.Agent) {
		agentTool, err := c.agentTool(depth, agent, parent)
		if err != nil {
			return nil, err
		}
		if agentTool == nil {
			slog.Info("No subagents available; omitting the agent tool")
		} else {
			allTools = append(allTools, agentTool)
			manageBackgroundAgents = c.backgroundAgentsAvailable(agent, depth)
		}
	}

	logFile := filepath.Join(c.cfg.Config().Options.DataDirectory, "logs", "angela.log")

	// Build hook runner if PreToolUse hooks are configured.
	var hookRunner *hooks.Runner
	if preToolHooks := c.cfg.Config().Hooks[hooks.EventPreToolUse]; len(preToolHooks) > 0 {
		hookRunner = hooks.NewRunner(preToolHooks, c.cfg.WorkingDir(), c.cfg.WorkingDir(),
			hooks.AgentIdentity{ID: agent.ID, Depth: depth})
	}

	pageClient := browserhttp.NewClient(browserhttp.Options{})
	downloadClient := browserhttp.NewClient(browserhttp.Options{Timeout: tools.DownloadHTTPTimeout})

	webSearchRouter, err := c.newWebSearchRouter(pageClient)
	if err != nil {
		return nil, fmt.Errorf("build web search router: %w", err)
	}

	allTools = append(
		allTools,
		tools.NewBashTool(c.cfg.WorkingDir(), c.cfg.Config().Options.Attribution, modelName),
		tools.NewGitTool(c.cfg.WorkingDir()),
		tools.NewAngelaInfoTool(c.cfg, c.lspManager, c.skills, c.skillTracker),
		tools.NewAngelaLogsTool(logFile),
		tools.NewJobOutputTool(),
		tools.NewJobKillTool(),
		tools.NewDownloadTool(c.cfg.WorkingDir(), downloadClient),
		tools.NewEditTool(c.lspManager, c.history, c.filetracker, c.cfg.WorkingDir()),
		tools.NewMultiEditTool(c.lspManager, c.history, c.filetracker, c.cfg.WorkingDir()),
		tools.NewFetchTool(c.cfg.WorkingDir(), pageClient),
		tools.NewWebFetchTool(filepath.Join(c.cfg.Config().Options.DataDirectory, scratch.WebFetchDir), pageClient),
		tools.NewWebSearchTool(webSearchRouter),
		tools.NewMultiSearchTool(webSearchRouter),
		tools.NewGlobTool(c.cfg.WorkingDir(), c.cfg.Config().Tools.Glob),
		tools.NewGrepTool(c.cfg.WorkingDir(), c.cfg.Config().Tools.Grep),
		tools.NewLsTool(c.cfg.WorkingDir(), c.cfg.Config().Tools.Ls),
		tools.NewSourcegraphTool(pageClient),
		tools.NewTodosTool(c.sessions),
		tools.NewReadTool(c.lspManager, c.filetracker, c.skillTracker, c.cfg.WorkingDir(), c.cfg.Config().Tools.Read, c.cfg.Config().Options.SkillsPaths...),
		tools.NewWriteTool(c.lspManager, c.history, c.filetracker, c.cfg.WorkingDir()),
	)

	if c.shouldEnableQuestionTool(agent, isSubAgent) {
		allTools = append(allTools, tools.NewQuestionTool(c.questions))
	}

	if c.reload != nil {
		allTools = append(allTools, tools.NewAngelaReloadTool(c.reload))
	}

	// Add LSP tools if user has configured LSPs or auto_lsp is enabled (nil or true).
	if len(c.cfg.Config().LSP) > 0 || c.cfg.Config().Options.AutoLSP == nil || *c.cfg.Config().Options.AutoLSP {
		allTools = append(
			allTools,
			tools.NewDiagnosticsTool(c.lspManager),
			tools.NewReferencesTool(c.lspManager),
			tools.NewLSPRestartTool(c.lspManager),
			tools.NewSymbolsTool(c.lspManager),
			tools.NewDefinitionTool(c.lspManager),
			tools.NewCallHierarchyTool(c.lspManager),
			tools.NewRenameTool(c.lspManager, c.history, c.filetracker),
			tools.NewReplaceSymbolTool(c.lspManager, c.history, c.filetracker),
		)
	}

	if len(c.cfg.Config().MCP) > 0 {
		allTools = append(
			allTools,
			tools.NewListMCPResourcesTool(c.cfg),
			tools.NewReadMCPResourceTool(c.cfg),
		)
	}

	// Appended here, before the AllowedTools/disabled_tools filtering
	// below, so a restrictive allowed_tools list or a global
	// disabled_tools entry excludes these two tools the same way it
	// would any other built-in, with no special-casing needed.
	if available, reason := c.imageToolsAvailable(agent, depth); available {
		allTools = append(
			allTools,
			tools.NewImageGenerateTool(c.newImageClient, c.images),
			tools.NewImageEditTool(c.newImageClient, c.images, c.cfg.WorkingDir()),
		)
	} else {
		slog.Debug("Image tools unavailable", "agent", agent.ID, "depth", depth, "reason", reason)
	}

	var filteredTools []fantasy.AgentTool
	for _, tool := range allTools {
		if agent.AllowedTools.Allows(tool.Info().Name) {
			filteredTools = append(filteredTools, tool)
		}
	}

	// NOTE: Past the whitelist so no allowed_tools can strand a report id.
	if agent.Mode == config.AgentModePrimary || agent.Mode == config.AgentModeBranch {
		filteredTools = append(filteredTools, tools.NewLoadReportTool(c.sessions, c.messages))
	}

	// NOTE: Past the whitelist, and offered only with an agent tool that
	// survived it, so allowed_tools cannot strand an agent it let launch.
	if manageBackgroundAgents {
		filteredTools = append(filteredTools, c.agentStatusTool(), c.agentStopTool())
	}

	// Appended past the whitelist rather than through it: these are the
	// only way a branch can draft its result and end, and the
	// conversation that forked it stays suspended until it does. A user
	// narrowing their branch agent's tools would otherwise strand both
	// sides with no way back.
	if agent.Mode == config.AgentModeBranch {
		filteredTools = append(filteredTools,
			c.mergeTool(),
			tools.NewProposalWriteTool(c.proposals),
			tools.NewProposalEditTool(c.proposals),
			tools.NewProposalReadTool(c.proposals),
		)
	}

	for _, tool := range tools.GetMCPTools(c.cfg, c.cfg.WorkingDir()) {
		if agent.AllowedMCP.Allows(tool.MCP(), tool.MCPToolName()) {
			filteredTools = append(filteredTools, tool)
			continue
		}
		slog.Debug("MCP not allowed", "tool", tool.Name(), "agent", agent.Name)
	}
	slices.SortFunc(filteredTools, func(a, b fantasy.AgentTool) int {
		return strings.Compare(a.Info().Name, b.Info().Name)
	})

	// The wrappers compose outside in as safety -> hooks -> spill ->
	// permissions -> tool. A hook must run first so its allow decision is
	// already on the context when the gate looks for one, and the gate must
	// run before the tool so a tool cannot forget to ask. Spill stays
	// outside the gate because the gate type-asserts on the real tool.
	filteredTools = wrapToolsWithPermissions(filteredTools, c.permissions, c.cfg.WorkingDir(), agent.AllowYoloMerge)

	filteredTools = wrapToolsWithSpill(filteredTools, ToolOutputRoot(c.cfg.Config().Options.DataDirectory))

	// Every tool call runs through the user's PreToolUse hooks, including
	// the ones a sub-agent makes. A delegated `bash` is still a bash
	// command on the user's machine, so it must face the same policy as
	// a top-level one; the payload's agent_id and depth let a hook tell
	// the two apart. The top-level `agent` call and the tool calls the
	// sub-agent then makes are distinct events, not duplicates.
	filteredTools = wrapToolsWithHooks(filteredTools, hookRunner)

	// Outermost: catches a stray Go error from any tool (builtin, MCP,
	// or otherwise) before it reaches fantasy's step loop, where it
	// would be classified alongside provider errors and could trigger
	// an unrelated retry. See safe_tool.go.
	filteredTools = wrapToolsWithSafety(filteredTools)

	return filteredTools, nil
}

// shouldEnableQuestionTool reports whether an agent may ask the user
// questions. It needs someone to ask, which rules out a non-interactive run
// and an ordinary sub-agent, whose output is read by another agent rather
// than a person. A branch is dispatched like a sub-agent but hands the
// conversation over, so asking is the reason it exists.
func (c *coordinator) shouldEnableQuestionTool(agent config.Agent, isSubAgent bool) bool {
	if !c.interactive {
		return false
	}
	return !isSubAgent || agent.Mode == config.AgentModeBranch
}

// removeWebFetchScratch clears the web_fetch pages cached for one
// delegated session, once its run is done reading them. A top-level
// session keeps its pages across turns, since the model may reread
// them later; the tool prunes those by age. Sessions that never
// triggered a large fetch never created the directory, so this is a
// no-op for them.
func removeWebFetchScratch(dataDirectory, sessionID string) {
	dir, err := tools.SessionScratchDir(filepath.Join(dataDirectory, scratch.WebFetchDir), sessionID)
	if err != nil {
		slog.Warn("Refusing to remove web_fetch scratch directory", "session", sessionID, "error", err)
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("Failed to remove web_fetch scratch directory", "session", sessionID, "error", err)
	}
}

// BeginAccepted reserves an accept slot for sessionID on the active
// agent and returns the ownership handle. It is the fire-and-forget
// dispatch path's only way to mark a run as accepted-but-not-yet-active
// so a cancel arriving before the run registers in activeRequests is not
// lost.
//
// It resolves through acceptExecutorFor, not executorForSession: this
// path is synchronous but not latency-critical, so unlike Cancel it can
// afford to rebuild a persisted child session's route from the database.
// A nil return means the session has no executor to run on at all, and
// the run that follows reports why.
func (c *coordinator) BeginAccepted(ctx context.Context, sessionID string) *AcceptedRun {
	executor, ok := c.acceptExecutorFor(ctx, sessionID)
	if !ok {
		return nil
	}
	return executor.BeginAccepted(sessionID)
}

// LockSession resolves sessionID the same way BeginAccepted does, then
// blocks on its executor's per-session dispatch mutex. See the
// Coordinator interface doc for what holding it guarantees.
func (c *coordinator) LockSession(ctx context.Context, sessionID string) (func(), bool) {
	executor, ok := c.acceptExecutorFor(ctx, sessionID)
	if !ok {
		return nil, false
	}
	return executor.LockSession(sessionID), true
}

// branchAbandonedMessage is what a parent's suspended tool call resolves to
// when the user gives up the branch through /abort — the only call that
// ever produces this outcome.
const branchAbandonedMessage = "The user ended this branch without merging it."

func (c *coordinator) Cancel(sessionID string) {
	// A sub-session is stopped through its own dispatch context. Going
	// through the executor alone would do nothing before the run registers
	// as active.
	c.subSessions.stop(sessionID)
	// Reaching through to a parent's branches below never abandons them:
	// interruptBranchTree only interrupts whatever turn each one has
	// running, the same as the branch directly in front of the user gets
	// when its own turn is cancelled. Giving one up outright, on demand,
	// is what AbandonBranch is for.
	//
	// Cancelling sessionID's own context, below, is different: when
	// sessionID is itself a parent suspended on a branch, that context is
	// exactly what runBranchAgent's select is blocked on, so this ends
	// that wait through its ctx.Done() case — the third way a branch can
	// end, alongside a merge and an AbandonBranch, and one that never
	// goes through Signal at all.
	for _, branchSessionID := range c.branches.branchesOf(sessionID) {
		c.interruptBranchTree(branchSessionID)
	}
	if executor, ok := c.executorForSession(sessionID); ok {
		executor.Cancel(sessionID)
	}
}

// interruptBranchTree stops whatever turn is running on a branch, then
// does the same for every branch it has forked in turn. Ordinarily that
// only interrupts: a turn cut short survives, same as the branch directly
// in front of the user does when its own turn is cancelled. But when a
// branch in the tree is itself suspended waiting on the next one down,
// its context is exactly what that wait is blocked on, so cancelling it
// here ends the wait too — the third way a branch can end, alongside a
// merge and an AbandonBranch, and the reason this walks the whole tree
// instead of stopping at the first level.
//
// Children are read before the branch itself is touched, for the same
// reason AbandonBranch reads them first: interrupting a branch suspended
// on another can unblock its dispatch and let it forget that child before
// this walk gets a chance to see it.
func (c *coordinator) interruptBranchTree(sessionID string) {
	children := c.branches.branchesOf(sessionID)
	if executor, ok := c.executorForSession(sessionID); ok {
		executor.Cancel(sessionID)
	}
	for _, childID := range children {
		c.interruptBranchTree(childID)
	}
}

// AbandonBranch gives a branch up outright and reports whether it was one.
// Unlike Cancel it does not care whether a turn is running: the user named
// this outcome, so a turn still in flight is given up along with the branch
// rather than merely interrupted.
//
// The order is load-bearing. Signalling first claims the rendezvous for the
// abandonment, so a merge the branch's own turn is racing to complete
// arrives second and is discarded. Cancelling first would leave a window
// in which that in-flight turn could still merge successfully and win,
// leaving the parent with an outcome the user never chose.
//
// It also cascades. A branch can fork branches of its own, within the same
// delegation budget as any other dispatch, and once this one is gone
// nothing else is left to resolve them. Its children are read before it is
// touched, because cancelling it can race with its own dispatch unwinding
// and forgetting them first; losing that race would leave a nested branch
// waiting on a parent that no longer exists. A branch only ever hangs under
// the top-level session or another branch, never under a subagent, so
// following branchesOf reaches every nested branch.
func (c *coordinator) AbandonBranch(sessionID string) bool {
	children := c.branches.branchesOf(sessionID)
	if !c.branches.Signal(sessionID, branchOutcome{Payload: branchAbandonedMessage}) {
		return false
	}
	if executor, ok := c.executorForSession(sessionID); ok {
		executor.Cancel(sessionID)
	}
	for _, childID := range children {
		c.AbandonBranch(childID)
	}
	return true
}

// CancelAll is the shutdown hook, so it also ends the background agents,
// which a plain Cancel of their parent deliberately leaves running. They
// go first: once the registry is closed, an agent unwinding under the
// cancel below reports nothing and wakes nobody.
func (c *coordinator) CancelAll() {
	c.background.shutdown()
	c.eachExecutor(SessionAgent.CancelAll)
}

func (c *coordinator) ClearQueue(sessionID string) {
	if executor, ok := c.executorForSession(sessionID); ok {
		executor.ClearQueue(sessionID)
	}
}

func (c *coordinator) IsBusy() bool {
	busy := false
	c.eachExecutor(func(executor SessionAgent) {
		busy = busy || executor.IsBusy()
	})
	return busy
}

func (c *coordinator) IsSessionBusy(sessionID string) bool {
	executor, ok := c.executorForSession(sessionID)
	return ok && executor.IsSessionBusy(sessionID)
}

func (c *coordinator) IsSessionBranch(sessionID string) bool {
	return c.branches.Waiting(sessionID)
}

func (c *coordinator) BackgroundAgentsRunning(sessionID string) int {
	return c.subSessions.backgroundRunningUnder(sessionID)
}

func (c *coordinator) SubSessions(ctx context.Context, parentSessionID string) ([]tools.SubSession, error) {
	err := c.subSessions.hydrate(parentSessionID, func() ([]tools.SubSession, error) {
		rows, err := c.sessions.ListChildren(ctx, parentSessionID)
		if err != nil {
			return nil, fmt.Errorf("list sub-sessions of %q: %w", parentSessionID, err)
		}
		infos := make([]tools.SubSession, len(rows))
		for i, row := range rows {
			infos[i] = endedSubSession(row)
		}
		return infos, nil
	})
	if err != nil {
		return nil, err
	}
	return c.subSessions.childrenOf(parentSessionID), nil
}

// DefaultModel reports the model a brand new session would run on. It
// resolves the coder from config, so it answers "what is configured",
// not "what is this session running" — callers that mean the latter
// want ActiveAgent.
func (c *coordinator) DefaultModel() Model {
	active, ok := c.cfg.Config().InstantiateAgent(config.AgentCoder)
	if !ok {
		return Model{}
	}
	model, err := c.models().buildModel(context.Background(), active, false)
	if err != nil {
		slog.Error("Failed to resolve the primary model", "error", err)
		return Model{}
	}
	return model
}

// ActiveAgent reports the agent instance a session runs on, along with
// the model resolved from it. An empty sessionID answers with the
// configured default, which is what the landing page shows before any
// session exists.
// ActiveAgent reports the agent instance a session runs on, along with
// the model resolved from it. An empty sessionID reports the draft
// instance the landing page edits before any session exists.
func (c *coordinator) ActiveAgent(ctx context.Context, sessionID string) (config.ActiveAgent, Model, error) {
	active, err := c.runningInstance(ctx, sessionID)
	if err != nil {
		return config.ActiveAgent{}, Model{}, err
	}
	model, err := c.models().buildModel(ctx, active, false)
	if err != nil {
		return config.ActiveAgent{}, Model{}, err
	}
	return active, model, nil
}

// UpdateModels reconciles the dispatch table against the config as it
// stands now. It no longer refreshes any model: every turn resolves its
// own agent, so a config change is picked up by the next turn without
// anything being mutated in place.
func (c *coordinator) UpdateModels(ctx context.Context) error {
	if _, ok := c.cfg.Config().Agents[config.AgentCoder]; !ok {
		return errCoderAgentNotConfigured
	}
	c.reconcileSubagents()
	c.skillTracker.SetActive(c.activeSkills())
	return nil
}

// activeSkills returns the current active skills, or nil when the
// coordinator has no skills manager.
func (c *coordinator) activeSkills() []*skills.Skill {
	if c.skills == nil {
		return nil
	}
	return c.skills.ActiveSkills()
}

func (c *coordinator) QueuedPrompts(sessionID string) int {
	executor, ok := c.executorForSession(sessionID)
	if !ok {
		return 0
	}
	return executor.QueuedPrompts(sessionID)
}

func (c *coordinator) QueuedPromptsList(sessionID string) []message.QueuedPrompt {
	executor, ok := c.executorForSession(sessionID)
	if !ok {
		return nil
	}
	return executor.QueuedPromptsList(sessionID)
}

func (c *coordinator) TakeQueuedPrompts(sessionID string) []message.QueuedPrompt {
	executor, ok := c.executorForSession(sessionID)
	if !ok {
		return nil
	}
	return executor.TakeQueuedPrompts(sessionID)
}

func (c *coordinator) Summarize(ctx context.Context, sessionID string) error {
	// A child session's turns run on its own sub-agent executor, which
	// owns the busy-check and per-session bookkeeping Summarize relies
	// on. Resolving rootExecutor unconditionally here would let a
	// summarize pass rootExecutor's busy check while the child's own
	// executor is mid-turn, then race that turn writing the same
	// session's messages.
	executor, err := c.summarizeExecutorFor(ctx, sessionID)
	if err != nil {
		return err
	}

	// The compact agent is resolved first because it decides which
	// provider this call talks to. Taking the provider from the
	// primary model instead would refresh credentials for one provider
	// and then send the request to another.
	active, err := c.runningInstance(ctx, sessionID)
	if err != nil {
		return err
	}
	compact := c.compactFor(ctx, sessionID, active)
	if !compact.agent.Available() {
		return fmt.Errorf("resolve the compact agent: %w", compact.agent.Err)
	}
	if !compact.ready {
		return errModelProviderNotConfigured
	}

	if err := c.refreshTokenIfExpired(ctx, compact.provider); err != nil {
		slog.Error("Failed to refresh OAuth2 token before summarize. Proceeding with existing token.", "error", err)
	}

	// Auth failures during summarize flow through fantasy's OnAuthRefresh,
	// the same path used by regular turns.
	return executor.Summarize(ctx, sessionID, compact.agent, compact.options, compact.onAuthRefresh)
}

// GenerateTitle names a session from a user prompt, using the title
// agent's own model and system prompt.
func (c *coordinator) GenerateTitle(ctx context.Context, sessionID, prompt string) {
	c.generateSessionTitle(ctx, sessionID, prompt)
}
