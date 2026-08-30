package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/discover"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/NaturalSelect/angela/internal/log"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/oauth/copilot"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/toolnames"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	openaisdk "github.com/openai/openai-go/v3/option"
	"github.com/qjebbs/go-jsons"
)

// Coordinator errors.
//
// The exported three are what a caller can get wrong: they name an
// agent, a preset or a model slot that does not fit. Transports answer
// them as a bad request, so they have to be nameable from outside this
// package. The rest describe a broken configuration or a broken
// process, which is nobody's request to fix.
var (
	ErrAgentNotAvailable   = errors.New("agent not available")
	ErrVariantNotAvailable = errors.New("model variant not available")
	ErrModelSlotMismatch   = errors.New("model slot does not match the agent")

	errCoderAgentNotConfigured    = errors.New("coder agent not configured")
	errModelProviderNotConfigured = errors.New("model provider not configured")
	errModelNotSelected           = errors.New("model config not selected")
	errModelNotFound              = errors.New("model not found in provider config")
)

// Copilot models that use the Responses API instead of Chat Completions.
var copilotResponsesModels = map[string]bool{
	"gpt-5.2":       true,
	"gpt-5.2-codex": true,
	"gpt-5.3-codex": true,
	"gpt-5.4":       true,
	"gpt-5.4-mini":  true,
	"gpt-5.5":       true,
	"gpt-5-mini":    true,
	"gpt-5.6-luna":  true,
	"gpt-5.6-terra": true,
	"gpt-5.6-sol":   true,
}

// openaiCompatResponsesAPIFunc returns the model filter used to select
// which models the given openai-compat provider dispatches through the
// Responses API instead of Chat Completions. The second return value is
// false when the provider never uses the Responses API. This is the
// single source of truth consulted both when constructing the provider
// (to opt into fantasy's Responses API routing) and when building
// per-call provider options (to know which options[Name] key the
// request will actually be read from).
func openaiCompatResponsesAPIFunc(providerID string) (fn func(modelID string) bool, ok bool) {
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		return func(modelID string) bool { return copilotResponsesModels[modelID] }, true
	default:
		return nil, false
	}
}

// OpenCode models that user Anthropic Messages API instead of Chat Completions.
var opencodeMessagesModels = map[string]bool{
	"qwen3.7-max": true,
}

type Coordinator interface {
	// INFO: (kujtim) this is not used yet we will use this when we have multiple agents
	// SetMainAgent(string)
	Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	// RunAccepted runs a call that was already accepted via
	// BeginAccepted on the fire-and-forget dispatch path. The handle is
	// the only carrier of accept-state across the backend.runAgent /
	// Coordinator / sessionAgent.Run layers: it reaches
	// sessionAgent.Run as SessionAgentCall.Accepted, where it is
	// consumed under dispatchMu once the accepted -> (cancel-on-entry |
	// queued | active) transition is chosen.
	RunAccepted(ctx context.Context, accept *AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error)
	// BeginAccepted reserves the accept slot the handle above carries.
	// For a child session this process has not dispatched itself it
	// also rebuilds the session's route, which costs one database read
	// and one agent resolution, once per child session per process.
	BeginAccepted(ctx context.Context, sessionID string) *AcceptedRun
	Cancel(sessionID string)
	// AbandonBranch gives a branch up whether or not a turn is running,
	// releasing the parent call suspended on it. Cancel is the gesture
	// that interrupts a turn and only abandons an idle branch; this is
	// the outcome a user names outright, and the two are kept apart so
	// neither has to guess which one was meant.
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
	IsBusy() bool
	QueuedPrompts(sessionID string) int
	QueuedPromptsList(sessionID string) []string
	ClearQueue(sessionID string)
	Summarize(context.Context, string) error

	// DefaultModel reports what a brand new session would run on.
	// Callers asking what a specific session runs want ActiveAgent.
	DefaultModel() Model

	// ActiveAgent reports the agent instance a session owns, together
	// with the model resolved from it. An empty sessionID answers with
	// the configured default.
	ActiveAgent(ctx context.Context, sessionID string) (config.ActiveAgent, Model, error)

	UpdateModels(ctx context.Context) error
	GenerateTitle(ctx context.Context, sessionID, prompt string)
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

	currentAgent SessionAgent
	subagents    *subagentRegistry

	// subagentRoutes maps a child session ID to the executor that owns it.
	subagentRoutes *csync.Map[string, subagentRoute]

	// branches holds the rendezvous for every dispatch currently
	// suspended on a branch session.
	branches *branchController

	// proposals holds the document each branch is drafting, which is
	// what its merge hands back.
	proposals *tools.ProposalStore

	// active holds each session's own agent instance. Everything a
	// user changes at runtime lands here, never in cfg. The zero value
	// is ready to use.
	active activeAgentStore

	// Skills discovery results (session-start snapshot).
	allSkills    []*skills.Skill // Pre-filter: all discovered after dedup.
	activeSkills []*skills.Skill // Post-filter: active skills only.
	skillTracker *skills.Tracker
}

// CoordinatorOptions holds the dependencies for NewCoordinator. Using a
// struct keeps the constructor self-documenting and avoids a long
// positional parameter list.
type CoordinatorOptions struct {
	Config      *config.ConfigStore
	Sessions    session.Service
	Messages    message.Service
	Permissions permission.Service
	Questions   question.Service
	History     history.Service
	FileTracker filetracker.Service
	LSPManager  *lsp.Manager
	Notify      pubsub.Publisher[notify.Notification]
	RunComplete pubsub.Publisher[notify.RunComplete]
	Skills      *skills.Manager
	Interactive bool
}

func NewCoordinator(ctx context.Context, opts CoordinatorOptions) (Coordinator, error) {
	// Skills are pre-discovered by the caller (see app.New /
	// backend.CreateWorkspace) and passed in via the manager. If no
	// manager was provided (legacy callers), fall back to an in-line
	// discovery so the coordinator still works.
	var allSkills, activeSkills []*skills.Skill
	if opts.Skills != nil {
		allSkills = opts.Skills.AllSkills()
		activeSkills = opts.Skills.ActiveSkills()
	} else {
		allSkills, activeSkills = discoverSkills(opts.Config)
	}
	skillTracker := skills.NewTracker(activeSkills)

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
		proposals:      tools.NewProposalStore(),
		allSkills:      allSkills,
		activeSkills:   activeSkills,
		skillTracker:   skillTracker,
		interactive:    opts.Interactive,
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

	c.currentAgent = c.buildAgent(config.AgentCoder, false)
	go c.forgetDeletedSessions(ctx, opts.Sessions.Subscribe(ctx))
	return c, nil
}

// forgetDeletedSessions evicts a session's cached agent and routing entry
// as the session goes away. Deletion runs through several entry points
// (CLI, workspace and backend), so the stores follow the event instead of
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
// dispatchMu; when nil (the in-process/local path) no accept tracking
// applies.
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
	defer removeWebFetchScratch(c.cfg.Config().Options.DataDirectory, sessionID)

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
	// Propagate the caller-supplied RunID (set via agent.WithRunID
	// at the HTTP boundary in backend.SendMessage) onto the
	// SessionAgentCall so the terminal RunComplete event echoes it
	// back. Both attempts in the retry chain reuse the same RunID;
	// the coalesce closure publishes the final outcome under that
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
		})
	}
	if target.child {
		run = c.rollingUpCost(ctx, sessionID, run)
	}
	beforeLoaded := c.skillTracker.LoadedNames()
	result, originalErr := run()
	logTurnSkillUsage(sessionID, prompt, c.activeSkills, c.skillTracker, beforeLoaded)

	if hasLatest && c.runComplete != nil {
		c.runComplete.PublishMustDeliver(ctx, pubsub.UpdatedEvent, latest)
		// Signal to the dispatcher (backend.runAgent) that the
		// authoritative terminal RunComplete for this run was already
		// emitted, so it does not publish a duplicate fallback for the
		// error it is about to receive.
		MarkRunCompletePublished(ctx)
	}
	return result, originalErr
}

// openaiCompatUsesResponses reports whether an openai-compat provider
// dispatches this model to the Responses API. It mirrors the transport
// choice made in buildOpenaiCompatProvider, where an explicit
// use_responses setting replaces the per-provider table.
func openaiCompatUsesResponses(providerCfg config.ProviderConfig, modelID string) bool {
	if providerCfg.UseResponses != nil {
		return *providerCfg.UseResponses
	}
	fn, ok := openaiCompatResponsesAPIFunc(providerCfg.ID)
	return ok && fn(modelID)
}

// responsesAPIEnabled reports whether a model on this provider talks the
// OpenAI Responses API. An explicit use_responses setting decides
// outright; left unset the choice falls back to recognizing the model ID,
// which only knows OpenAI's own names and misses gateway aliases.
func responsesAPIEnabled(providerCfg config.ProviderConfig, modelID string) bool {
	if providerCfg.UseResponses != nil {
		return *providerCfg.UseResponses
	}
	return openai.IsResponsesModel(modelID)
}

// effectiveReasoningEffort returns the reasoning effort to apply for provider calls.
// An effort set explicitly in the model config wins outright: it is the user's own
// statement that the model reasons, and catalog metadata cannot know about hand-typed
// models or gateway aliases. Otherwise it takes the model default when valid, and
// finally falls back to the first configured reasoning level.
func effectiveReasoningEffort(model Model) string {
	if effort := model.ModelCfg.ReasoningEffort; effort != "" {
		return effort
	}
	if !model.CatwalkCfg.CanReason {
		return ""
	}

	if effort := model.CatwalkCfg.DefaultReasoningEffort; effort != "" && slices.Contains(model.CatwalkCfg.ReasoningLevels, effort) {
		return effort
	}
	if len(model.CatwalkCfg.ReasoningLevels) > 0 {
		return model.CatwalkCfg.ReasoningLevels[0]
	}
	return ""
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
	agent         CompactAgent
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
	// The prompt cache key says "compact" rather than the running
	// agent's own name: summarize sends a different system prompt
	// than a normal turn, so it must not share a cache route with it.
	call.options = getProviderOptions(call.agent.Model, providerCfg, buildPromptCacheKey(sessionID, "compact"))
	call.onAuthRefresh = c.makeAuthRefreshCallback(providerCfg)
	call.ready = true
	return call
}

func getProviderOptions(model Model, providerCfg config.ProviderConfig, promptCacheKey string) fantasy.ProviderOptions {
	options := fantasy.ProviderOptions{}

	cfgOpts := []byte("{}")
	providerCfgOpts := []byte("{}")
	catwalkOpts := []byte("{}")

	if model.ModelCfg.ProviderOptions != nil {
		data, err := json.Marshal(model.ModelCfg.ProviderOptions)
		if err == nil {
			cfgOpts = data
		}
	}

	if providerCfg.ProviderOptions != nil {
		data, err := json.Marshal(providerCfg.ProviderOptions)
		if err == nil {
			providerCfgOpts = data
		}
	}

	if model.CatwalkCfg.Options.ProviderOptions != nil {
		data, err := json.Marshal(model.CatwalkCfg.Options.ProviderOptions)
		if err == nil {
			catwalkOpts = data
		}
	}

	readers := []io.Reader{
		bytes.NewReader(catwalkOpts),
		bytes.NewReader(providerCfgOpts),
		bytes.NewReader(cfgOpts),
	}

	got, err := jsons.Merge(readers)
	if err != nil {
		slog.Error("Could not merge call config", "err", err)
		return options
	}

	mergedOptions := make(map[string]any)

	err = json.Unmarshal([]byte(got), &mergedOptions)
	if err != nil {
		slog.Error("Could not create config for call", "err", err)
		return options
	}

	reasoningEffort := effectiveReasoningEffort(model)
	// effectiveReasoningEffort already applied the catalog rules, so a
	// non-empty result is by itself the decision to send an effort.
	shouldSetEffort := reasoningEffort != ""

	switch providerCfg.Type {
	case openai.Name, azure.Name:
		_, hasReasoningEffort := mergedOptions["reasoning_effort"]
		if !hasReasoningEffort && shouldSetEffort {
			mergedOptions["reasoning_effort"] = reasoningEffort
		}
		if _, hasPromptCacheKey := mergedOptions["prompt_cache_key"]; !hasPromptCacheKey && promptCacheKey != "" {
			mergedOptions["prompt_cache_key"] = promptCacheKey
		}
		if responsesAPIEnabled(providerCfg, model.CatwalkCfg.ID) {
			if openai.IsResponsesReasoningModel(model.CatwalkCfg.ID) || shouldSetEffort {
				mergedOptions["reasoning_summary"] = "auto"
				mergedOptions["include"] = []openai.IncludeType{openai.IncludeReasoningEncryptedContent}
			}
			parsed, err := openai.ParseResponsesOptions(mergedOptions)
			if err == nil {
				options[openai.Name] = parsed
			}
		} else {
			parsed, err := openai.ParseOptions(mergedOptions)
			if err == nil {
				options[openai.Name] = parsed
			}
		}

	case anthropic.Name, bedrock.Name:
		var (
			_, hasEffort = mergedOptions["effort"]
			_, hasThink  = mergedOptions["thinking"]
			extraBody    = make(map[string]any)
		)

		switch providerCfg.ID {
		case string(catwalk.InferenceProviderAlibabaSingapore), string(catwalk.InferenceProviderAlibabaUS):
			switch {
			case !hasEffort && shouldSetEffort:
				extraBody["reasoning_effort"] = reasoningEffort
			case !hasThink && model.CatwalkCfg.CanReason:
				if model.ModelCfg.Think {
					extraBody["thinking"] = map[string]any{"type": "enabled"}
				} else {
					extraBody["thinking"] = map[string]any{"type": "disabled"}
				}
			}
			mergedOptions["extra_body"] = extraBody

		default:
			switch {
			case !hasEffort && shouldSetEffort:
				mergedOptions["effort"] = reasoningEffort
			case !hasThink && model.ModelCfg.Think:
				mergedOptions["thinking"] = map[string]any{"budget_tokens": 2000}
			}

			// metadata.user_id is an Anthropic-API-specific anti-abuse field.
			// Bedrock (a different providerCfg.Type sharing this case) has its own fixed request schema and does not
			// support it.
			if providerCfg.Type == anthropic.Name {
				existingExtraBody, _ := mergedOptions["extra_body"].(map[string]any)
				mergedOptions["extra_body"] = withAnthropicUserID(existingExtraBody, promptCacheKey)
			}
		}

		requestVisibleThinking(mergedOptions)

		parsed, err := anthropic.ParseOptions(mergedOptions)
		if err == nil {
			options[anthropic.Name] = parsed
		}

	case openrouter.Name:
		_, hasReasoning := mergedOptions["reasoning"]
		if !hasReasoning && shouldSetEffort {
			mergedOptions["reasoning"] = map[string]any{
				"enabled": true,
				"effort":  reasoningEffort,
			}
		}
		parsed, err := openrouter.ParseOptions(mergedOptions)
		if err == nil {
			options[openrouter.Name] = parsed
		}

	case vercel.Name:
		_, hasReasoning := mergedOptions["reasoning"]
		if !hasReasoning && shouldSetEffort {
			mergedOptions["reasoning"] = map[string]any{
				"enabled": true,
				"effort":  reasoningEffort,
			}
		}
		parsed, err := vercel.ParseOptions(mergedOptions)
		if err == nil {
			options[vercel.Name] = parsed
		}

	case google.Name:
		_, hasReasoning := mergedOptions["thinking_config"]
		if !hasReasoning {
			if strings.HasPrefix(model.CatwalkCfg.ID, "gemini-2") {
				mergedOptions["thinking_config"] = map[string]any{
					"thinking_budget":  2000,
					"include_thoughts": true,
				}
			} else {
				mergedOptions["thinking_config"] = map[string]any{
					"thinking_level":   reasoningEffort,
					"include_thoughts": true,
				}
			}
		}
		parsed, err := google.ParseOptions(mergedOptions)
		if err == nil {
			options[google.Name] = parsed
		}

	case openaicompat.Name:
		extraBody, _ := mergedOptions["extra_body"].(map[string]any)
		if extraBody == nil {
			extraBody = make(map[string]any)
		}

		_, hasReasoningEffort := mergedOptions["reasoning_effort"]
		if !hasReasoningEffort && shouldSetEffort {
			switch providerCfg.ID {
			case string(catwalk.InferenceProviderIoNet):
				extraBody["reasoning"] = map[string]string{"effort": reasoningEffort}
			case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
				// MiniMax models use the "thinking" parameter instead of
				// "reasoning_effort". Other models on these providers still
				// use the standard field.
				if !strings.HasPrefix(strings.ToLower(model.CatwalkCfg.ID), "minimax") {
					mergedOptions["reasoning_effort"] = reasoningEffort
				}
			default:
				mergedOptions["reasoning_effort"] = reasoningEffort
			}
		}

		// "reasoning effort" is a standard OpenAI field, but "thinking" is not.
		// Setting it in the right way for each provider.
		// TODO: Abstract this in Fantasy somehow?
		// TODO: Allow custom providers to specify how to set this?
		switch providerCfg.ID {
		case string(catwalk.InferenceProviderIoNet):
			if _, ok := extraBody["reasoning"]; !ok && model.CatwalkCfg.CanReason {
				if model.ModelCfg.Think {
					extraBody["reasoning"] = map[string]string{"effort": "medium"}
				} else {
					extraBody["reasoning"] = map[string]string{"effort": "none"}
				}
			}

		case string(catwalk.InferenceProviderZAI), string(catwalk.InferenceProviderDeepSeek):
			if model.ModelCfg.Think || reasoningEffort != "" {
				extraBody["thinking"] = map[string]any{"type": "enabled"}
			} else {
				extraBody["thinking"] = map[string]any{"type": "disabled"}
			}

		case string(catwalk.InferenceProviderFireworks):
			// NOTE: Fireworks break if we set both `reasoning_effort` and `thinking`.
			if reasoningEffort == "" {
				if model.ModelCfg.Think {
					extraBody["thinking"] = map[string]any{"type": "enabled"}
				} else {
					extraBody["thinking"] = map[string]any{"type": "disabled"}
				}
			}

		case string(catwalk.InferenceProviderBaseten):
			extraBody["chat_template_args"] = map[string]any{
				"enable_thinking": model.ModelCfg.Think || reasoningEffort != "" && reasoningEffort != "none",
			}

		case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
			// MiniMax M3 uses the "thinking" parameter to control reasoning.
			// "reasoning_split" must be true so thinking content is returned
			// in the "reasoning_content" field instead of inline in "content".
			if strings.HasPrefix(strings.ToLower(model.CatwalkCfg.ID), "minimax") {
				if model.CatwalkCfg.CanReason && (model.ModelCfg.Think || reasoningEffort != "") {
					extraBody["thinking"] = map[string]any{"type": "adaptive"}
					extraBody["reasoning_split"] = true
				} else {
					extraBody["thinking"] = map[string]any{"type": "disabled"}
				}
			}

		case string(catwalk.InferenceProviderAlibabaSingapore), string(catwalk.InferenceProviderAlibabaUS):
			if model.CatwalkCfg.CanReason {
				extraBody["enable_thinking"] = model.ModelCfg.Think || reasoningEffort != ""
			}
		}

		mergedOptions["extra_body"] = withPromptCacheKey(extraBody, promptCacheKey)

		parsed, err := openaicompat.ParseOptions(mergedOptions)
		if err == nil {
			options[openaicompat.Name] = parsed
		}

		// Some openai-compat providers silently dispatch specific models
		// to the Responses API even though the provider is configured as
		// openai-compat, and that path reads options[openai.Name]
		// instead of options[openaicompat.Name]. Mirror the full
		// extra_body there too (not just prompt_cache_key), so nothing
		// configured through it silently disappears from the request.
		// Fields with no Responses equivalent are dropped by
		// ParseResponsesOptions like any other unrecognized JSON key.
		if openaiCompatUsesResponses(providerCfg, model.CatwalkCfg.ID) {
			if respParsed, err := openai.ParseResponsesOptions(extraBody); err == nil {
				options[openai.Name] = respParsed
			}
		}

	default:
		// Known custom providers (litellm, ollama, omlx) are
		// openai-compat under the hood.
		if discover.IsKnownCustomProvider(string(providerCfg.Type)) {
			extraBody, _ := mergedOptions["extra_body"].(map[string]any)
			mergedOptions["extra_body"] = withPromptCacheKey(extraBody, promptCacheKey)
			parsed, err := openaicompat.ParseOptions(mergedOptions)
			if err == nil {
				options[openaicompat.Name] = parsed
			}
		}
	}

	return options
}

// withPromptCacheKey sets extraBody["prompt_cache_key"] to promptCacheKey,
// unless the caller already configured one explicitly or promptCacheKey
// is empty. Used by the openai-compat family, which has no typed
// PromptCacheKey field and instead relies on the extra_body escape hatch.
func withPromptCacheKey(extraBody map[string]any, promptCacheKey string) map[string]any {
	if extraBody == nil {
		extraBody = make(map[string]any)
	}
	if _, ok := extraBody["prompt_cache_key"]; !ok && promptCacheKey != "" {
		extraBody["prompt_cache_key"] = promptCacheKey
	}
	return extraBody
}

// requestVisibleThinking asks Anthropic to stream its thinking summary.
// Adaptive thinking — which the effort option always selects — omits the
// trace unless display says otherwise, and the default is decided per
// model ID, so a gateway-served or renamed model thinks with nothing to
// show. Setting display here short-circuits that lookup for every model;
// an explicit user setting still wins.
func requestVisibleThinking(mergedOptions map[string]any) {
	if _, ok := mergedOptions["thinking_display"]; ok {
		return
	}
	mergedOptions["thinking_display"] = string(anthropic.ThinkingDisplaySummarized)
}

// withAnthropicUserID sets extraBody["metadata"]["user_id"] to a value
// derived from promptCacheKey, unless the caller already configured a
// metadata.user_id explicitly or promptCacheKey is empty. Anthropic's
// ProviderOptions has no typed Metadata field, so this relies on the
// same extra_body escape hatch as withPromptCacheKey.
func withAnthropicUserID(extraBody map[string]any, promptCacheKey string) map[string]any {
	if promptCacheKey == "" {
		return extraBody
	}
	if extraBody == nil {
		extraBody = make(map[string]any)
	}
	metadata, _ := extraBody["metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	if _, ok := metadata["user_id"]; !ok {
		metadata["user_id"] = buildAnthropicUserID(promptCacheKey)
	}
	extraBody["metadata"] = metadata
	return extraBody
}

func mergeCallOptions(model Model, cfg config.ProviderConfig, promptCacheKey string) (fantasy.ProviderOptions, *float64, *float64, *int64, *float64, *float64) {
	modelOptions := getProviderOptions(model, cfg, promptCacheKey)
	temp := cmp.Or(model.ModelCfg.Temperature, model.CatwalkCfg.Options.Temperature)
	topP := cmp.Or(model.ModelCfg.TopP, model.CatwalkCfg.Options.TopP)
	topK := cmp.Or(model.ModelCfg.TopK, model.CatwalkCfg.Options.TopK)
	freqPenalty := cmp.Or(model.ModelCfg.FrequencyPenalty, model.CatwalkCfg.Options.FrequencyPenalty)
	presPenalty := cmp.Or(model.ModelCfg.PresencePenalty, model.CatwalkCfg.Options.PresencePenalty)
	return modelOptions, temp, topP, topK, freqPenalty, presPenalty
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
		IsYolo:        c.permissions.SkipRequests(),
		Sessions:      c.sessions,
		Messages:      c.messages,
		Notify:        c.notify,
		RunComplete:   c.runComplete,
		GenerateTitle: c.generateSessionTitle,
		SkillTracker:  c.skillTracker,
		Reminders:     c.cfg.Config().Options.Reminders,
	})
}

// buildTools assembles the tool set for a turn. modelID is the model
// the turn actually resolved to; it reaches the bash tool's commit
// attribution, which must name the model that did the work rather than
// whatever the global slot happens to point at.
//
// depth is this turn's dispatch depth (0 for a primary turn, 1+ for a
// subagent). It gates both the question tool and, against
// Options.SubagentMaxDepth, the agent tool: a turn only holds it while
// it still has delegation budget left, which keeps the dispatch chain
// from growing past the configured limit.
func (c *coordinator) buildTools(agent config.Agent, modelID string, depth int) ([]fantasy.AgentTool, error) {
	var allTools []fantasy.AgentTool
	isSubAgent := depth > 0
	canDelegate := depth < c.cfg.Config().Options.SubagentMaxDepth()

	if canDelegate && agent.AllowedTools.Allows(toolnames.Agent) {
		if c.subagents.Len() == 0 {
			slog.Info("No subagents available; omitting the agent tool")
		} else {
			agentTool, err := c.agentTool(depth)
			if err != nil {
				return nil, err
			}
			allTools = append(allTools, agentTool)
		}
	}

	logFile := filepath.Join(c.cfg.Config().Options.DataDirectory, "logs", "angela.log")

	// Build hook runner if PreToolUse hooks are configured.
	var hookRunner *hooks.Runner
	if preToolHooks := c.cfg.Config().Hooks[hooks.EventPreToolUse]; len(preToolHooks) > 0 {
		hookRunner = hooks.NewRunner(preToolHooks, c.cfg.WorkingDir(), c.cfg.WorkingDir(),
			hooks.AgentIdentity{ID: agent.ID, Depth: depth})
	}

	allTools = append(
		allTools,
		tools.NewBashTool(c.cfg.WorkingDir(), c.cfg.Config().Options.Attribution, modelID),
		tools.NewAngelaInfoTool(c.cfg, c.lspManager, c.allSkills, c.activeSkills, c.skillTracker),
		tools.NewAngelaLogsTool(logFile),
		tools.NewJobOutputTool(),
		tools.NewJobKillTool(),
		tools.NewDownloadTool(c.cfg.WorkingDir(), nil),
		tools.NewEditTool(c.lspManager, c.history, c.filetracker, c.cfg.WorkingDir()),
		tools.NewMultiEditTool(c.lspManager, c.history, c.filetracker, c.cfg.WorkingDir()),
		tools.NewFetchTool(c.cfg.WorkingDir(), nil),
		tools.NewWebFetchTool(filepath.Join(c.cfg.Config().Options.DataDirectory, "webfetch"), nil),
		tools.NewWebSearchTool(c.cfg.WorkingDir(), nil),
		tools.NewGlobTool(c.cfg.WorkingDir(), c.cfg.Config().Tools.Glob),
		tools.NewGrepTool(c.cfg.WorkingDir(), c.cfg.Config().Tools.Grep),
		tools.NewLoadReportTool(c.sessions, c.messages),
		tools.NewLsTool(c.cfg.WorkingDir(), c.cfg.Config().Tools.Ls),
		tools.NewSourcegraphTool(nil),
		tools.NewTodosTool(c.sessions),
		tools.NewViewTool(c.lspManager, c.filetracker, c.skillTracker, c.cfg.WorkingDir(), c.cfg.Config().Options.SkillsPaths...),
		tools.NewWriteTool(c.lspManager, c.history, c.filetracker, c.cfg.WorkingDir()),
	)

	if c.shouldEnableQuestionTool(agent, isSubAgent) {
		allTools = append(allTools, tools.NewQuestionTool(c.questions))
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

	var filteredTools []fantasy.AgentTool
	for _, tool := range allTools {
		if agent.AllowedTools.Allows(tool.Info().Name) {
			filteredTools = append(filteredTools, tool)
		}
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

	// The wrappers compose outside in as hooks -> permissions -> tool.
	// A hook must run first so its allow decision is already on the
	// context when the gate looks for one, and the gate must run before
	// the tool so a tool cannot forget to ask.
	filteredTools = wrapToolsWithPermissions(filteredTools, c.permissions, c.cfg.WorkingDir())

	// Every tool call runs through the user's PreToolUse hooks, including
	// the ones a sub-agent makes. A delegated `bash` is still a bash
	// command on the user's machine, so it must face the same policy as
	// a top-level one; the payload's agent_id and depth let a hook tell
	// the two apart. The top-level `agent` call and the tool calls the
	// sub-agent then makes are distinct events, not duplicates.
	filteredTools = wrapToolsWithHooks(filteredTools, hookRunner)

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
// session, once that session's turn is done reading them. Sessions
// that never triggered a large fetch never created the directory, so
// this is a no-op for them.
func removeWebFetchScratch(dataDirectory, sessionID string) {
	dir, err := tools.WebFetchScratchDir(filepath.Join(dataDirectory, "webfetch"), sessionID)
	if err != nil {
		slog.Warn("Refusing to remove web_fetch scratch directory", "session", sessionID, "error", err)
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("Failed to remove web_fetch scratch directory", "session", sessionID, "error", err)
	}
}

// activeIO wires the store to the session record and the config.
func (c *coordinator) activeIO() activeAgentIO {
	return activeAgentIO{
		load:        c.loadActiveAgent,
		materialize: c.materializeActiveAgent,
		save:        c.sessions.UpdateActiveAgent,
	}
}

// loadActiveAgent reads a session's persisted delta. A session that
// cannot be read at all is an error: running a turn on a guessed agent
// would silently answer as something the user did not pick.
func (c *coordinator) loadActiveAgent(ctx context.Context, sessionID string) (config.ActiveAgentState, error) {
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return config.ActiveAgentState{}, fmt.Errorf("read session %q: %w", sessionID, err)
	}
	return sess.ActiveAgent, nil
}

// materializeActiveAgent turns a session's delta into a live instance.
// The agent definition always comes from the config as it stands now;
// only the model selection comes from the delta. That split is the
// point: editing the config steers prompts and tools on the next turn,
// while a model the user picked for this session stays picked.
//
// A session with no delta yet runs the coder on the configured default,
// and keeps following that default until it picks something of its own.
// A session naming an agent config has since removed, disabled or
// hidden falls back to the coder — erroring there would strand the
// session with no way to type the command that fixes it.
func (c *coordinator) materializeActiveAgent(sessionID string, state config.ActiveAgentState) (config.ActiveAgent, error) {
	cfg := c.cfg.Config()

	if state.IsZero() {
		return coderInstance(cfg)
	}
	if active, ok := cfg.Restore(state); ok && !active.Agent.IsHidden() {
		return active, nil
	}
	slog.Warn("Session points at an agent that is no longer available; falling back to the coder",
		"sessionID", sessionID, "agent", state.Agent)
	return coderInstance(cfg)
}

func coderInstance(cfg *config.Config) (config.ActiveAgent, error) {
	active, ok := cfg.InstantiateAgent(config.AgentCoder)
	if !ok {
		return config.ActiveAgent{}, errCoderAgentNotConfigured
	}
	return active, nil
}

// activeAgentFor returns the agent instance this session runs on. The
// instance is the session's own: editing it never reaches the config
// every other session reads.
func (c *coordinator) activeAgentFor(ctx context.Context, sessionID string) (config.ActiveAgent, error) {
	return c.active.get(ctx, sessionID, c.activeIO())
}

// editActiveAgent changes a session's own instance under the store's
// lock, so two concurrent edits cannot lose one another.
func (c *coordinator) editActiveAgent(
	ctx context.Context,
	sessionID string,
	fn func(config.ActiveAgent) (config.ActiveAgent, bool, error),
) error {
	return c.active.edit(ctx, sessionID, c.activeIO(), fn)
}

// EditActiveAgent changes a session's own agent instance: which agent
// it runs, which model, which parameter preset, whether it thinks — or
// any combination, applied atomically. Nothing here reaches the global
// config; the instance belongs to the session.
//
// It returns the instance as it stands after the edit, so a caller that
// asked for a relative change (toggling thinking) learns the value that
// was actually reached instead of guessing from what it last read.
//
// Identity-level moves leave a trail message so the transcript explains
// why the assistant's behavior changed mid-session. The trail is
// written after the edit is durable, and failing to write it never
// fails the edit.
func (c *coordinator) EditActiveAgent(ctx context.Context, sessionID string, edit config.ActiveAgentEdit) (config.ActiveAgent, error) {
	if edit.IsZero() {
		return c.activeAgentFor(ctx, sessionID)
	}
	cfg := c.cfg.Config()
	if edit.Agent != "" {
		if err := checkPrimaryAgent(cfg, edit.Agent); err != nil {
			return config.ActiveAgent{}, err
		}
	}

	var (
		change   activeAgentChange
		switched Model
		agentID  string
		result   config.ActiveAgent
	)
	err := c.editActiveAgent(ctx, sessionID, func(current config.ActiveAgent) (config.ActiveAgent, bool, error) {
		result = current
		next, moved, err := applyActiveAgentEdit(cfg, current, edit)
		if err != nil || !moved.any() {
			return current, false, err
		}
		model, err := c.buildModel(ctx, next, false)
		if err != nil {
			return current, false, fmt.Errorf("resolve the session's model: %w", err)
		}
		// An unknown preset is an error here, where the user is
		// watching and can pick again; per-turn resolution stays
		// lenient about the same name for the opposite reason.
		if v := next.Agent.Variant; v != "" && !slices.Contains(model.ModelCfg.VariantNames(&model.CatwalkCfg), v) {
			return current, false, fmt.Errorf("%w: %q on %q", ErrVariantNotAvailable, v, model.ModelCfg.Model)
		}
		change, switched, agentID, result = moved, model, next.Agent.ID, next
		return next, true, nil
	})
	if err != nil {
		return config.ActiveAgent{}, err
	}
	if switched.Model == nil {
		return result, nil
	}

	for _, text := range change.trail(switched) {
		if _, err := c.messages.Create(ctx, sessionID, message.CreateMessageParams{
			Role:     message.System,
			Parts:    []message.ContentPart{message.TextContent{Text: text}},
			Model:    switched.ModelCfg.Model,
			Provider: switched.ModelCfg.Provider,
			Agent:    agentID,
		}); err != nil {
			// The edit itself is already durable; losing its trail
			// must not fail it.
			slog.Warn("Failed to write the active agent trail",
				"error", err, "sessionID", sessionID)
		}
	}
	return result, nil
}

// SwitchAgent points a session at a different agent from the next turn
// on. Switching to the agent already in effect is a no-op: it writes no
// trail, so repeated selection does not litter the transcript.
func (c *coordinator) SwitchAgent(ctx context.Context, sessionID, agentID string) error {
	_, err := c.EditActiveAgent(ctx, sessionID, config.ActiveAgentEdit{Agent: agentID})
	return err
}

// SwitchVariant points a session at a different parameter preset of the
// model it already runs on. Nothing about the session's identity moves:
// same agent, same model, different request parameters. The empty name
// selects the model's baseline, which is how a user backs out.
func (c *coordinator) SwitchVariant(ctx context.Context, sessionID, variant string) error {
	_, err := c.EditActiveAgent(ctx, sessionID, config.ActiveAgentEdit{Variant: &variant})
	return err
}

// checkPrimaryAgent rejects the agents a session must not be pointed
// at: ones config does not have, ones hidden from the user, and
// subagents, which exist only to be dispatched by a primary.
func checkPrimaryAgent(cfg *config.Config, agentID string) error {
	agentCfg, ok := cfg.Agents[agentID]
	if !ok || agentCfg.IsHidden() {
		return fmt.Errorf("%w: %q", ErrAgentNotAvailable, agentID)
	}
	if agentCfg.Mode != config.AgentModePrimary {
		return fmt.Errorf("%w: %q is a subagent", ErrAgentNotAvailable, agentID)
	}
	return nil
}

// activeAgentChange records what an edit actually moved. The trail
// lines cannot be rendered during the fold because they name the
// resulting model, which is only built afterwards.
type activeAgentChange struct {
	agentFrom    string
	agentTo      config.Agent
	agentMoved   bool
	variantFrom  string
	variantTo    string
	variantMoved bool
	modelMoved   bool
	thinkMoved   bool
}

func (ch activeAgentChange) any() bool {
	return ch.agentMoved || ch.variantMoved || ch.modelMoved || ch.thinkMoved
}

// trail renders the transcript lines this change deserves. Only moves
// that change how the assistant answers get one; the thinking flag and
// the model are shown in the UI's own chrome instead.
func (ch activeAgentChange) trail(model Model) []string {
	var lines []string
	if ch.agentMoved {
		lines = append(lines, agentSwitchedText(ch.agentFrom, ch.agentTo))
	}
	if ch.variantMoved {
		lines = append(lines, variantSwitchedText(ch.variantFrom, ch.variantTo, modelDisplayName(model)))
	}
	return lines
}

// applyActiveAgentEdit folds an edit into an instance and reports what
// moved. It is pure: the validation that needs a built model happens in
// the caller, once, on the result.
//
// Switching agent takes the new agent's own configured model rather
// than carrying the old one across: an agent's model is part of what
// the user picked when they picked the agent.
func applyActiveAgentEdit(cfg *config.Config, current config.ActiveAgent, edit config.ActiveAgentEdit) (config.ActiveAgent, activeAgentChange, error) {
	next := current
	var change activeAgentChange

	if edit.Agent != "" && edit.Agent != current.Agent.ID {
		instantiated, ok := cfg.InstantiateAgent(edit.Agent)
		if !ok {
			return current, change, fmt.Errorf("%w: %q", ErrAgentNotAvailable, edit.Agent)
		}
		next = instantiated
		change.agentFrom, change.agentTo, change.agentMoved = current.Agent.ID, instantiated.Agent, true
	}

	if edit.Model != nil && !sameModel(*edit.Model, next.Model) {
		// The slot belongs to the agent, not to the caller: it is what
		// InstantiateFor matches on to decide whether an internal agent
		// inherits this model. An omitted name keeps the agent's own; a
		// name that disagrees would silently break that inheritance, so
		// it is reported rather than taken.
		if edit.ModelName != "" && edit.ModelName != next.ModelName {
			return current, change, fmt.Errorf("%w: %q runs on %q, not %q",
				ErrModelSlotMismatch, next.Agent.ID, next.ModelName, edit.ModelName)
		}
		next.Model = *edit.Model
		change.modelMoved = true
	}

	if edit.Variant != nil {
		if *edit.Variant != next.Agent.Variant {
			change.variantFrom, change.variantTo, change.variantMoved = next.Agent.Variant, *edit.Variant, true
			next.Agent.Variant = *edit.Variant
		}
		// Recorded even when it matches what the config says today:
		// the user picked this preset, so later editing the config's
		// default must not move them off it.
		pick := *edit.Variant
		next.VariantPick = &pick
	}

	if think, ok := thinkAfter(edit, next.Model.Think); ok {
		next.Model.Think = think
		change.thinkMoved = true
	}

	return next.Clone(), change, nil
}

// thinkAfter resolves what the thinking flag becomes, given what it is
// now. A toggle is answered here, where current is the value under the
// session's lock, rather than by the caller against a value it read
// some time ago.
func thinkAfter(edit config.ActiveAgentEdit, current bool) (bool, bool) {
	switch {
	case edit.ToggleThink:
		return !current, true
	case edit.Think != nil && *edit.Think != current:
		return *edit.Think, true
	default:
		return current, false
	}
}

// sameModel reports whether two selections name the same model.
// SelectedModel carries maps and so cannot be compared directly, and
// the identity a user means by "the model" is the provider and the
// model ID anyway — the rest is parameters.
func sameModel(a, b config.SelectedModel) bool {
	return a.Provider == b.Provider && a.Model == b.Model
}

// agentSwitchedText renders the transcript line for a switch. The
// previous agent is omitted when the session had none recorded, which
// reads better than naming an empty agent.
func agentSwitchedText(from string, to config.Agent) string {
	name := to.Name
	if name == "" {
		name = to.ID
	}
	if from == "" {
		return fmt.Sprintf("Switched to the %s agent.", name)
	}
	return fmt.Sprintf("Switched from %s to the %s agent.", from, name)
}

// modelDisplayName prefers the catalog's display name and falls back to
// the raw ID for models a provider discovered without one.
func modelDisplayName(model Model) string {
	return cmp.Or(model.CatwalkCfg.Name, model.ModelCfg.Model)
}

// variantSwitchedText renders the transcript line for a preset change.
func variantSwitchedText(from, to, modelName string) string {
	switch {
	case to == "":
		return fmt.Sprintf("Switched %s back to its baseline parameters.", modelName)
	case from == "":
		return fmt.Sprintf("Switched %s to the %s preset.", modelName, to)
	default:
		return fmt.Sprintf("Switched %s from the %s preset to %s.", modelName, from, to)
	}
}

// buildModel resolves the single model an ActiveAgent runs on. The
// model is already materialized on the instance, so this no longer
// consults the shared Config.Models table — that lookup, and its
// fallback for a bad name, moved into config.InstantiateAgent.
func (c *coordinator) buildModel(ctx context.Context, active config.ActiveAgent, isSubAgent bool) (Model, error) {
	agent := active.Agent
	modelCfg := active.Model
	if modelCfg.Provider == "" || modelCfg.Model == "" {
		return Model{}, errModelNotSelected
	}

	providerCfg, ok := c.cfg.Config().Providers.Get(modelCfg.Provider)
	if !ok {
		return Model{}, errModelProviderNotConfigured
	}

	var catwalkModel *catwalk.Model
	for _, m := range providerCfg.Models {
		if m.ID == modelCfg.Model {
			catwalkModel = &m
		}
	}
	if catwalkModel == nil {
		return Model{}, errModelNotFound
	}

	// The variant overlay lands before the provider is built so that
	// provider options a variant sets reach buildProvider too.
	modelCfg, variantApplied := modelCfg.WithVariant(agent.Variant, catwalkModel)
	if agent.Variant != "" && !variantApplied {
		slog.Warn("Unknown model variant; falling back to the model baseline",
			"agent", agent.ID, "model", active.ModelName, "variant", agent.Variant)
	}

	provider, err := c.buildProvider(providerCfg, modelCfg, isSubAgent)
	if err != nil {
		return Model{}, err
	}

	modelID := modelCfg.Model
	if modelCfg.Provider == openrouter.Name && isExactoSupported(modelID) {
		modelID += ":exacto"
	}

	languageModel, err := provider.LanguageModel(ctx, modelID)
	if err != nil {
		return Model{}, err
	}

	resolved := Model{
		Model:      languageModel,
		CatwalkCfg: *catwalkModel,
		ModelCfg:   modelCfg,
		FlatRate:   providerCfg.FlatRate,
	}
	if variantApplied {
		resolved.Variant = agent.Variant
	}

	// Baking the agent's temperature into the model's config lets every
	// downstream reader of Model().ModelCfg.Temperature (mergeCallOptions,
	// runSubAgent) pick it up without knowing about the agent at all.
	if agent.Temperature != nil {
		resolved.ModelCfg.Temperature = agent.Temperature
	}

	return resolved, nil
}

func (c *coordinator) buildAnthropicProvider(baseURL, apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	var opts []anthropic.Option

	switch {
	case strings.HasPrefix(apiKey, "Bearer "):
		// NOTE: Prevent the SDK from picking up the API key from env.
		os.Setenv("ANTHROPIC_API_KEY", "")
		headers["Authorization"] = apiKey
	case providerID == string(catwalk.InferenceProviderMiniMax) || providerID == string(catwalk.InferenceProviderMiniMaxChina):
		// NOTE: Prevent the SDK from picking up the API key from env.
		os.Setenv("ANTHROPIC_API_KEY", "")
		headers["Authorization"] = "Bearer " + apiKey
	case apiKey != "":
		// X-Api-Key header
		opts = append(opts, anthropic.WithAPIKey(apiKey))
	}

	if len(headers) > 0 {
		opts = append(opts, anthropic.WithHeaders(headers))
	}

	if baseURL := config.NormalizeBaseURL(baseURL, catwalk.TypeAnthropic); baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}

	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, anthropic.WithHTTPClient(httpClient))
	}
	return anthropic.New(opts...)
}

func (c *coordinator) buildOpenaiProvider(baseURL, apiKey string, headers map[string]string, useResponses *bool) (fantasy.Provider, error) {
	opts := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithUseResponsesAPI(),
		// Gateways are routinely configured as a plain "openai" provider
		// (the type defaults to it) while answering in the
		// chat-completions shape, where reasoning arrives as
		// delta.reasoning_content. The openai provider parses reasoning
		// only on its Responses path, so without these hooks the text is
		// read into nothing and the turn renders with no thinking at all.
		openai.WithLanguageModelOptions(
			openai.WithLanguageModelStreamExtraFunc(openaicompat.StreamExtraFunc),
			openai.WithLanguageModelExtraContentFunc(openaicompat.ExtraContentFunc),
		),
	}
	if useResponses != nil {
		forced := *useResponses
		opts = append(opts, openai.WithResponsesAPIFunc(func(string) bool { return forced }))
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, openai.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, openai.WithHeaders(headers))
	}
	if baseURL := config.NormalizeBaseURL(baseURL, catwalk.TypeOpenAI); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return openai.New(opts...)
}

func (c *coordinator) buildOpenrouterProvider(_, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []openrouter.Option{
		openrouter.WithAPIKey(apiKey),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, openrouter.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, openrouter.WithHeaders(headers))
	}
	return openrouter.New(opts...)
}

func (c *coordinator) buildVercelProvider(_, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []vercel.Option{
		vercel.WithAPIKey(apiKey),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, vercel.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, vercel.WithHeaders(headers))
	}
	return vercel.New(opts...)
}

func (c *coordinator) buildOpenaiCompatProvider(baseURL, apiKey string, headers map[string]string, extraBody map[string]any, providerID string, isSubAgent bool, useResponses *bool) (fantasy.Provider, error) {
	opts := []openaicompat.Option{
		openaicompat.WithBaseURL(config.NormalizeBaseURL(baseURL, catwalk.TypeOpenAICompat)),
		openaicompat.WithAPIKey(apiKey),
	}

	switch {
	case useResponses != nil:
		if *useResponses {
			opts = append(opts, openaicompat.WithUseResponsesAPI(),
				openaicompat.WithResponsesAPIFunc(func(string) bool { return true }))
		}
	default:
		if fn, ok := openaiCompatResponsesAPIFunc(providerID); ok {
			opts = append(opts, openaicompat.WithUseResponsesAPI(), openaicompat.WithResponsesAPIFunc(fn))
		}
	}

	// Set HTTP client based on provider and debug mode.
	var httpClient *http.Client
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		httpClient = copilot.NewClient(isSubAgent, c.cfg.Config().Options.Debug)
	}
	if httpClient == nil && c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	if httpClient != nil {
		opts = append(opts, openaicompat.WithHTTPClient(httpClient))
	}

	if len(headers) > 0 {
		opts = append(opts, openaicompat.WithHeaders(headers))
	}

	for extraKey, extraValue := range extraBody {
		opts = append(opts, openaicompat.WithSDKOptions(openaisdk.WithJSONSet(extraKey, extraValue)))
	}

	return openaicompat.New(opts...)
}

func (c *coordinator) buildAzureProvider(baseURL, apiKey string, headers map[string]string, options map[string]string) (fantasy.Provider, error) {
	opts := []azure.Option{
		azure.WithBaseURL(baseURL),
		azure.WithAPIKey(apiKey),
		azure.WithUseResponsesAPI(),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, azure.WithHTTPClient(httpClient))
	}
	if options == nil {
		options = make(map[string]string)
	}
	if apiVersion, ok := options["apiVersion"]; ok {
		opts = append(opts, azure.WithAPIVersion(apiVersion))
	}
	if len(headers) > 0 {
		opts = append(opts, azure.WithHeaders(headers))
	}

	return azure.New(opts...)
}

func (c *coordinator) buildBedrockProvider(apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	var opts []bedrock.Option
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, bedrock.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, bedrock.WithHeaders(headers))
	}

	switch {
	case apiKey != "":
		opts = append(opts, bedrock.WithAPIKey(apiKey))
	case os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "":
		opts = append(opts, bedrock.WithAPIKey(os.Getenv("AWS_BEARER_TOKEN_BEDROCK")))
	default:
		// Skip, let the SDK do authentication.
	}

	switch providerID {
	case string(catwalk.InferenceProviderBedrockEurope):
		opts = append(opts, bedrock.WithRegion("eu-west-1"))
	default:
		opts = append(opts, bedrock.WithRegion("us-east-1"))
	}

	return bedrock.New(opts...)
}

func (c *coordinator) buildGoogleProvider(baseURL, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []google.Option{
		google.WithBaseURL(baseURL),
		google.WithGeminiAPIKey(apiKey),
	}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, google.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, google.WithHeaders(headers))
	}
	return google.New(opts...)
}

func (c *coordinator) buildGoogleVertexProvider(headers map[string]string, options map[string]string) (fantasy.Provider, error) {
	opts := []google.Option{}
	if c.cfg.Config().Options.Debug {
		httpClient := log.NewHTTPClient()
		opts = append(opts, google.WithHTTPClient(httpClient))
	}
	if len(headers) > 0 {
		opts = append(opts, google.WithHeaders(headers))
	}

	project := options["project"]
	location := options["location"]

	opts = append(opts, google.WithVertex(project, location))

	return google.New(opts...)
}

func (c *coordinator) isAnthropicThinking(model config.SelectedModel) bool {
	if model.Think {
		return true
	}
	opts, err := anthropic.ParseOptions(model.ProviderOptions)
	return err == nil && opts.Thinking != nil
}

func (c *coordinator) buildProvider(providerCfg config.ProviderConfig, model config.SelectedModel, isSubAgent bool) (fantasy.Provider, error) {
	headers := maps.Clone(providerCfg.ExtraHeaders)
	if headers == nil {
		headers = make(map[string]string)
	}

	// handle special headers for anthropic
	if providerCfg.Type == anthropic.Name && c.isAnthropicThinking(model) {
		if v, ok := headers["anthropic-beta"]; ok {
			headers["anthropic-beta"] = v + ",interleaved-thinking-2025-05-14"
		} else {
			headers["anthropic-beta"] = "interleaved-thinking-2025-05-14"
		}
	}

	apiKey, _ := c.cfg.Resolve(providerCfg.APIKey)
	baseURL, _ := c.cfg.Resolve(providerCfg.BaseURL)

	switch providerCfg.ID {
	case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
		if opencodeMessagesModels[model.Model] {
			return c.buildAnthropicProvider(baseURL, apiKey, headers, providerCfg.ID)
		}
	}

	switch providerCfg.Type {
	case openai.Name:
		return c.buildOpenaiProvider(baseURL, apiKey, headers, providerCfg.UseResponses)
	case anthropic.Name:
		return c.buildAnthropicProvider(baseURL, apiKey, headers, providerCfg.ID)
	case openrouter.Name:
		return c.buildOpenrouterProvider(baseURL, apiKey, headers)
	case vercel.Name:
		return c.buildVercelProvider(baseURL, apiKey, headers)
	case azure.Name:
		return c.buildAzureProvider(baseURL, apiKey, headers, providerCfg.ExtraParams)
	case bedrock.Name:
		return c.buildBedrockProvider(apiKey, headers, providerCfg.ID)
	case google.Name:
		return c.buildGoogleProvider(baseURL, apiKey, headers)
	case "google-vertex":
		return c.buildGoogleVertexProvider(headers, providerCfg.ExtraParams)
	case openaicompat.Name:
		switch providerCfg.ID {
		case string(catwalk.InferenceProviderZAI):
			// providerCfg is a value copy, but ExtraBody still aliases
			// the published config snapshot that other turns read.
			providerCfg.ExtraBody = maps.Clone(providerCfg.ExtraBody)
			if providerCfg.ExtraBody == nil {
				providerCfg.ExtraBody = map[string]any{}
			}
			providerCfg.ExtraBody["tool_stream"] = true
		}
		return c.buildOpenaiCompatProvider(baseURL, apiKey, headers, providerCfg.ExtraBody, providerCfg.ID, isSubAgent, providerCfg.UseResponses)
	default:
		// Known custom providers (litellm, ollama, omlx) are
		// openai-compat under the hood.
		if discover.IsKnownCustomProvider(string(providerCfg.Type)) {
			return c.buildOpenaiCompatProvider(baseURL, apiKey, headers, providerCfg.ExtraBody, providerCfg.ID, isSubAgent, providerCfg.UseResponses)
		}
		return nil, fmt.Errorf("provider type not supported: %q", providerCfg.Type)
	}
}

func isExactoSupported(modelID string) bool {
	supportedModels := []string{
		"moonshotai/kimi-k2-0905",
		"deepseek/deepseek-v3.1-terminus",
		"z-ai/glm-4.6",
		"openai/gpt-oss-120b",
		"qwen/qwen3-coder",
	}
	return slices.Contains(supportedModels, modelID)
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

// branchAbandonedMessage is what a parent's suspended tool call resolves to
// when the user gives a branch up. Both routes to abandonment — the cancel
// gesture and the explicit command — report it, so the parent reads the same
// outcome however the user got there.
const branchAbandonedMessage = "The user ended this branch without merging it."

func (c *coordinator) Cancel(sessionID string) {
	// A cancel that lands on a branch or on a conversation suspended by
	// one means "give this up", not "interrupt this turn", so it resolves
	// the rendezvous instead of tearing down a run.
	if c.abortBranchFor(sessionID) {
		return
	}
	if executor, ok := c.executorForSession(sessionID); ok {
		executor.Cancel(sessionID)
	}
}

// AbandonBranch gives a branch up outright and reports whether it was one.
// Unlike Cancel it does not care whether a turn is running: the user named
// this outcome, so a turn still in flight is given up along with the branch
// rather than merely interrupted.
//
// The order is load-bearing. Signalling first claims the rendezvous for the
// abandonment, so the error the cancelled turn may raise on its way out
// arrives second and is discarded. Cancelling first would let a failing
// first turn report "could not be started" through the same rendezvous and
// win, leaving the parent with an outcome the user never chose.
func (c *coordinator) AbandonBranch(sessionID string) bool {
	if !c.branches.Signal(sessionID, branchOutcome{Payload: branchAbandonedMessage}) {
		return false
	}
	if executor, ok := c.executorForSession(sessionID); ok {
		executor.Cancel(sessionID)
	}
	return true
}

// abortBranchFor turns a cancel into an abandoned branch where one applies,
// reporting whether it handled the cancel. Only the user reaches it, through
// Esc or /abort; no model can end a branch.
//
// Three cases:
//   - a conversation suspended on branches: abandon them, and cancel their
//     runs so they stop working for a result nobody will read. The suspended
//     turn is left alone — it resumes with the abandonment as its tool result.
//   - an idle branch: abandon it. There is no turn to interrupt, so a cancel
//     here can only mean the branch itself.
//   - a branch mid-turn: not handled. The cancel falls through and interrupts
//     that turn, which is what lets the user stop a branch mid-thought and
//     redirect it rather than lose it. A user who means to give the branch up
//     regardless says so through AbandonBranch.
func (c *coordinator) abortBranchFor(sessionID string) bool {
	if aborted := c.branches.AbortByParent(sessionID, branchOutcome{Payload: branchAbandonedMessage}); len(aborted) > 0 {
		for _, branchSessionID := range aborted {
			if executor, ok := c.executorForSession(branchSessionID); ok {
				executor.Cancel(branchSessionID)
			}
		}
		return true
	}

	if c.branches.Waiting(sessionID) && !c.IsSessionBusy(sessionID) {
		return c.branches.Signal(sessionID, branchOutcome{Payload: branchAbandonedMessage})
	}
	return false
}

func (c *coordinator) CancelAll() {
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

// DefaultModel reports the model a brand new session would run on. It
// resolves the coder from config, so it answers "what is configured",
// not "what is this session running" — callers that mean the latter
// want ActiveAgent.
func (c *coordinator) DefaultModel() Model {
	active, ok := c.cfg.Config().InstantiateAgent(config.AgentCoder)
	if !ok {
		return Model{}
	}
	model, err := c.buildModel(context.Background(), active, false)
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
func (c *coordinator) ActiveAgent(ctx context.Context, sessionID string) (config.ActiveAgent, Model, error) {
	if sessionID == "" {
		active, ok := c.cfg.Config().InstantiateAgent(config.AgentCoder)
		if !ok {
			return config.ActiveAgent{}, Model{}, errCoderAgentNotConfigured
		}
		model, err := c.buildModel(ctx, active, false)
		return active, model, err
	}

	active, err := c.activeAgentFor(ctx, sessionID)
	if err != nil {
		return config.ActiveAgent{}, Model{}, err
	}
	model, err := c.buildModel(ctx, active, false)
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
	return nil
}

func (c *coordinator) QueuedPrompts(sessionID string) int {
	executor, ok := c.executorForSession(sessionID)
	if !ok {
		return 0
	}
	return executor.QueuedPrompts(sessionID)
}

func (c *coordinator) QueuedPromptsList(sessionID string) []string {
	executor, ok := c.executorForSession(sessionID)
	if !ok {
		return nil
	}
	return executor.QueuedPromptsList(sessionID)
}

func (c *coordinator) Summarize(ctx context.Context, sessionID string) error {
	// The compact agent is resolved first because it decides which
	// provider this call talks to. Taking the provider from the
	// primary model instead would refresh credentials for one provider
	// and then send the request to another.
	active, err := c.activeAgentFor(ctx, sessionID)
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
	return c.currentAgent.Summarize(ctx, sessionID, compact.agent, compact.options, compact.onAuthRefresh)
}

// GenerateTitle names a session from a user prompt, using the title
// agent's own model and system prompt.
func (c *coordinator) GenerateTitle(ctx context.Context, sessionID, prompt string) {
	c.generateSessionTitle(ctx, sessionID, prompt)
}

// refreshTokenIfExpired proactively refreshes the OAuth token if it has expired.
func (c *coordinator) refreshTokenIfExpired(ctx context.Context, providerCfg config.ProviderConfig) error {
	if providerCfg.OAuthToken == nil || !providerCfg.OAuthToken.IsExpired() {
		return nil
	}
	slog.Debug("Token needs to be refreshed", "provider", providerCfg.ID)
	return c.refreshOAuth2Token(ctx, providerCfg)
}

// retryAfterUnauthorized attempts to refresh credentials after an auth error
// and returns nil if the request should be retried. For OAuth providers whose
// refresh token is revoked, and for Bedrock providers whose AWS SSO session
// has expired, it triggers interactive re-authentication and blocks until the
// user completes it (or the context is cancelled).
func (c *coordinator) retryAfterUnauthorized(ctx context.Context, providerCfg config.ProviderConfig) error {
	switch {
	case providerCfg.OAuthToken != nil:
		slog.Debug("Received 401. Refreshing token and retrying", "provider", providerCfg.ID)
		if err := c.refreshOAuth2Token(ctx, providerCfg); err != nil {
			// If the refresh token was revoked, trigger interactive
			// re-auth and wait for the user to complete it.
			var exchangeErr *oauth.TokenExchangeError
			if c.notify != nil && errors.As(err, &exchangeErr) && exchangeErr.IsRefreshTokenRevoked() {
				slog.Info("Refresh token revoked, waiting for re-authentication", "provider", providerCfg.ID)
				// Read the generation before publishing: the user can
				// finish authenticating before the wait starts, and
				// that completion must still count.
				since := c.cfg.AuthGeneration(providerCfg.ID)
				c.notify.Publish(pubsub.CreatedEvent, notify.Notification{
					Type:       notify.TypeReAuthenticate,
					ProviderID: providerCfg.ID,
				})
				return c.waitForInteractiveReauth(ctx, providerCfg.ID, since)
			}
			return err
		}
		return nil
	case providerCfg.AWSAuthRefresh != "":
		return c.refreshAWSCredentials(ctx, providerCfg)
	case strings.Contains(providerCfg.APIKeyTemplate, "$"):
		slog.Debug("Received 401. Refreshing API Key template and retrying", "provider", providerCfg.ID)
		return c.refreshApiKeyTemplate(ctx, providerCfg)
	default:
		return nil
	}
}

// errNoInteractiveAuth is returned by an OnAuthRefresh callback when a
// provider needs interactive re-authentication but no notifier is available
// to drive it (e.g. headless runs). Returning it surfaces the original auth
// error rather than retrying.
var errNoInteractiveAuth = errors.New("interactive authentication unavailable")

// waitForInteractiveReauth blocks until interactive re-authentication for the
// provider completes (signalled via SignalAuthComplete) or the context is
// cancelled, then rebuilds models so the next attempt picks up fresh
// credentials. Returns nil when the caller should retry.
func (c *coordinator) waitForInteractiveReauth(ctx context.Context, providerID string, since uint64) error {
	// Use a detached context with a generous timeout so the wait survives
	// agent run cancellation. The user needs time to complete browser-based
	// authentication.
	waitCtx, waitCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer waitCancel()
	slog.Info("Blocking on WaitForTokenChangeSince", "provider", providerID)
	if waitErr := c.cfg.WaitForTokenChangeSince(waitCtx, providerID, since); waitErr != nil {
		slog.Info("WaitForTokenChangeSince returned error", "provider", providerID, "error", waitErr)
		return waitErr
	}
	// If the original context was cancelled during the wait, fantasy's retry
	// would fail immediately, so surface the cancellation instead.
	if ctx.Err() != nil {
		slog.Warn("Original context cancelled during auth wait, cannot retry",
			"provider", providerID, "ctx_err", ctx.Err())
		return ctx.Err()
	}
	// Reconcile the dispatch table; the in-flight turn picks up the new
	// credentials through its own model rebuild on retry.
	if updateErr := c.UpdateModels(waitCtx); updateErr != nil {
		slog.Error("Failed to update models after re-authentication", "error", updateErr)
		return updateErr
	}
	slog.Info("Reconciled after re-authentication, returning nil to retry", "provider", providerID)
	return nil
}

// isUnauthorized reports whether err is an HTTP 401 from a provider.
func isUnauthorized(err error) bool {
	var providerErr *fantasy.ProviderError
	return errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusUnauthorized
}

// makeAuthRefreshCallback returns an OnAuthRefresh callback for fantasy that
// delegates to the coordinator's existing credential refresh logic. Returns
// nil if no refresh mechanism is configured for the provider.
func (c *coordinator) makeAuthRefreshCallback(providerCfg config.ProviderConfig) func(context.Context, *fantasy.ProviderError) error {
	if providerCfg.OAuthToken == nil &&
		!strings.Contains(providerCfg.APIKeyTemplate, "$") &&
		providerCfg.AWSAuthRefresh == "" {
		return nil
	}
	return func(ctx context.Context, _ *fantasy.ProviderError) error {
		return c.retryAfterUnauthorized(ctx, providerCfg)
	}
}

func (c *coordinator) refreshOAuth2Token(ctx context.Context, providerCfg config.ProviderConfig) error {
	if err := c.cfg.RefreshOAuthToken(ctx, config.ScopeGlobal, providerCfg.ID); err != nil {
		slog.Error("Failed to refresh OAuth token after 401 error", "provider", providerCfg.ID, "error", err)
		return err
	}
	if err := c.UpdateModels(ctx); err != nil {
		return err
	}
	return nil
}

func (c *coordinator) refreshApiKeyTemplate(ctx context.Context, providerCfg config.ProviderConfig) error {
	newAPIKey, err := c.cfg.Resolve(providerCfg.APIKeyTemplate)
	if err != nil {
		slog.Error("Failed to re-resolve API key after 401 error", "provider", providerCfg.ID, "error", err)
		return err
	}

	providerCfg.APIKey = newAPIKey
	c.cfg.Config().Providers.Set(providerCfg.ID, providerCfg)

	if err := c.UpdateModels(ctx); err != nil {
		return err
	}
	return nil
}

// subAgentParams holds the parameters for running a sub-agent.
type subAgentParams struct {
	Agent          SessionAgent
	SessionID      string
	AgentMessageID string
	ToolCallID     string
	Prompt         string
	SessionTitle   string
	Resolved       resolvedAgent
}

// runSubAgent runs a sub-agent and handles session management and cost accumulation.
// It creates a sub-session, runs the agent with the given prompt, and propagates
// the cost to the parent session.
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, error) {
	// Create sub-session
	agentToolSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	session, err := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("create session: %w", err)
	}
	defer removeWebFetchScratch(c.cfg.Config().Options.DataDirectory, session.ID)

	c.registerSubagentRoute(session.ID, params.Resolved.ID, params.Agent)

	// Record which sub-agent owns this session. Without it the session is
	// only addressable for as long as this process lives, and a later one
	// has no identity to resume it under.
	if err := c.sessions.UpdateActiveAgent(ctx, session.ID, params.Resolved.Host.State()); err != nil {
		slog.Warn(
			"Failed to record the sub-agent on its session",
			"session", session.ID,
			"agent", params.Resolved.ID,
			"error", err,
		)
	}

	// The dispatch that created this session already ran inside the
	// permission scope the user controls; that covers the delegated work
	// too, so routine gated tools the child uses need not be asked about
	// again. Whether anyone can answer a prompt is a property of where
	// the run started, not of how deep it has nested, so the child
	// inherits it: under a TUI the child's prompts reach the same
	// dialog, and under a headless run they are refused instead of
	// stalling the whole dispatch.
	c.permissions.SetSessionPromptPolicy(session.ID, permission.PromptAllow)
	c.permissions.SetSessionUnattended(session.ID, c.permissions.SessionUnattended(params.SessionID))

	model := params.Resolved.Model
	maxTokens := params.Resolved.MaxTokens

	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return fantasy.ToolResponse{}, errModelProviderNotConfigured
	}

	// Run the agent
	compact := c.compactFor(ctx, params.SessionID, params.Resolved.Host)
	run := func() (*fantasy.AgentResult, error) {
		return params.Agent.Run(ctx, SessionAgentCall{
			SessionID:                session.ID,
			Agent:                    params.Resolved,
			Prompt:                   params.Prompt,
			MaxOutputTokens:          maxTokens,
			ProviderOptions:          getProviderOptions(model, providerCfg, buildPromptCacheKey(params.SessionID, params.Resolved.ID)),
			SummarizeProviderOptions: compact.options,
			Compact:                  compact.agent,
			SummarizeOnAuthRefresh:   compact.onAuthRefresh,
			Temperature:              model.ModelCfg.Temperature,
			TopP:                     model.ModelCfg.TopP,
			TopK:                     model.ModelCfg.TopK,
			FrequencyPenalty:         model.ModelCfg.FrequencyPenalty,
			PresencePenalty:          model.ModelCfg.PresencePenalty,
			NonInteractive:           true,
			OnAuthRefresh:            c.makeAuthRefreshCallback(providerCfg),
		})
	}
	result, err := run()
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to generate response: %s", err)), nil
	}

	// Update parent session cost on a best-effort basis. A failure here must
	// not discard the sub-agent output that was already produced.
	if err := c.updateParentSessionCost(ctx, session.ID, params.SessionID); err != nil {
		slog.Warn(
			"Failed to update parent session cost",
			"child_session", session.ID,
			"parent_session", params.SessionID,
			"error", err,
		)
	}

	output := subAgentOutput(result)
	if output == "" {
		return fantasy.NewTextErrorResponse("Sub-agent completed but produced no text output."), nil
	}
	return fantasy.NewTextResponse(output), nil
}

func subAgentOutput(result *fantasy.AgentResult) string {
	if result == nil {
		return ""
	}
	return result.Response.Content.Text()
}

// runBranchAgent forks the caller's conversation into a session the user
// drives themselves, and suspends this tool call until they resolve it.
//
// It differs from runSubAgent in what it does not do. It grants no blanket
// permission policy, because a branch acts on the user's behalf in front of
// the user, so its prompts belong to them. It does not run non-interactively.
// And it does not read a result off the agent run at all: the first turn
// merely starts the conversation, and what crosses back is whatever the user
// eventually approves through the merge tool — possibly many turns later.
func (c *coordinator) runBranchAgent(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, error) {
	branchSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	session, err := c.sessions.CreateTaskSession(ctx, branchSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("create branch session: %w", err)
	}
	defer removeWebFetchScratch(c.cfg.Config().Options.DataDirectory, session.ID)

	c.registerSubagentRoute(session.ID, params.Resolved.ID, params.Agent)

	if err := c.sessions.UpdateActiveAgent(ctx, session.ID, params.Resolved.Host.State()); err != nil {
		slog.Warn(
			"Failed to record the branch agent on its session",
			"session", session.ID,
			"agent", params.Resolved.ID,
			"error", err,
		)
	}

	// Everything the caller had said and seen up to the call that forked
	// it. The forking message itself is excluded: the branch is told what
	// to do by the prompt below, so copying a tool call nobody will answer
	// would only leave a dangling call in its history.
	if err := c.messages.ForkSession(ctx, params.SessionID, session.ID, params.AgentMessageID); err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("fork conversation into branch: %w", err)
	}

	forkPrompt, err := renderBranchForkPrompt(branchForkPrompt{
		ParentTitle: c.parentTitle(ctx, params.SessionID),
		Prompt:      params.Prompt,
	})
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("render branch prompt: %w", err)
	}

	// Registered before the first turn, not after: that turn may call
	// merge, and a merge arriving before anyone is listening must still
	// land.
	done := c.branches.Register(session.ID, params.SessionID)
	defer c.branches.Forget(session.ID)
	defer c.proposals.Discard(session.ID)

	if err := c.startBranchTurn(ctx, session.ID, forkPrompt, params); err != nil {
		// Reported through the rendezvous rather than returned, so that a
		// user who abandoned the branch while it was failing to start
		// still sees their own outcome: delivery happens once, and
		// whichever came first wins.
		slog.Error("Branch first turn failed", "session", session.ID, "error", err)
		c.branches.Signal(session.ID, branchOutcome{
			Payload: fmt.Sprintf("The branch could not be started: %s", err),
		})
	}

	select {
	case <-ctx.Done():
		return fantasy.ToolResponse{}, ctx.Err()
	case out := <-done:
		if out.Merged {
			return fantasy.NewTextResponse(out.Payload), nil
		}
		resp := fantasy.NewTextErrorResponse(out.Payload)
		resp.StopTurn = true
		return resp, nil
	}
}

// startBranchTurn runs the branch's opening turn, the one seeded with the
// fork prompt. Every turn after it is driven by the user through the normal
// session path.
func (c *coordinator) startBranchTurn(ctx context.Context, sessionID, prompt string, params subAgentParams) error {
	model := params.Resolved.Model
	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return errModelProviderNotConfigured
	}

	compact := c.compactFor(ctx, params.SessionID, params.Resolved.Host)
	_, err := params.Agent.Run(ctx, SessionAgentCall{
		SessionID:                sessionID,
		Agent:                    params.Resolved,
		Prompt:                   prompt,
		MaxOutputTokens:          params.Resolved.MaxTokens,
		ProviderOptions:          getProviderOptions(model, providerCfg, buildPromptCacheKey(sessionID, params.Resolved.ID)),
		SummarizeProviderOptions: compact.options,
		Compact:                  compact.agent,
		SummarizeOnAuthRefresh:   compact.onAuthRefresh,
		Temperature:              model.ModelCfg.Temperature,
		TopP:                     model.ModelCfg.TopP,
		TopK:                     model.ModelCfg.TopK,
		FrequencyPenalty:         model.ModelCfg.FrequencyPenalty,
		PresencePenalty:          model.ModelCfg.PresencePenalty,
		OnAuthRefresh:            c.makeAuthRefreshCallback(providerCfg),
	})
	return err
}

// parentTitle names the conversation a branch was forked from, for the fork
// prompt. Best effort: an unnamed parent just makes the prompt less specific.
func (c *coordinator) parentTitle(ctx context.Context, sessionID string) string {
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return ""
	}
	return sess.Title
}

// branchDispatchRefusal reports why this caller may not fork a branch, or an
// empty string when it may. Each refusal names the alternative, because the
// model has to be able to act on it without guessing.
//
// Being suspended on a branch already is not a refusal: forking several at
// once is the point. It lets the user hold alternative directions of one
// question, or several unrelated questions, side by side and resolve each on
// its own. The rendezvous keeps a waiter per branch, so the dispatches stay
// independent however many are outstanding.
func (c *coordinator) branchDispatchRefusal(ctx context.Context, sessionID string) string {
	if !c.interactive {
		return "A branch hands the conversation to the user, so it needs an interactive session. " +
			"Dispatch a regular subagent instead."
	}
	if c.dispatchDepth(ctx, sessionID) != 0 {
		return "Only a top-level conversation can fork a branch, because the user has to be able to take it over. " +
			"Dispatch a regular subagent instead."
	}
	return ""
}

// updateParentSessionCost accumulates the cost from a child session to its
// parent session. The add is atomic because sibling sub-runs under one
// parent finish concurrently.
func (c *coordinator) updateParentSessionCost(ctx context.Context, childSessionID, parentSessionID string) error {
	childSession, err := c.sessions.Get(ctx, childSessionID)
	if err != nil {
		return fmt.Errorf("get child session: %w", err)
	}

	if err := c.sessions.AddCost(ctx, parentSessionID, childSession.Cost); err != nil {
		return fmt.Errorf("add cost to parent session: %w", err)
	}

	return nil
}

// discoverSkills is a thin fallback wrapper used only when no
// skills.Manager has been threaded through to the coordinator. All
// production call sites (backend.CreateWorkspace, setupLocalWorkspace)
// run discovery in advance and pass the results via the manager;
// reaching this path means a caller bypassed both. It deliberately does
// NOT publish to the package-level broker — there are no subscribers in
// that case, so doing so would be misleading without delivering the
// snapshot anywhere useful.
func discoverSkills(cfg *config.ConfigStore) (allSkills, activeSkills []*skills.Skill) {
	opts := cfg.Config().Options
	var paths, disabled []string
	if opts != nil {
		paths = opts.SkillsPaths
		disabled = opts.DisabledSkills
	}
	var resolver func(string) (string, error)
	if r := cfg.Resolver(); r != nil {
		resolver = r.ResolveValue
	}
	allSkills, activeSkills, states := skills.DiscoverFromConfig(skills.DiscoveryConfig{
		SkillsPaths:    paths,
		DisabledSkills: disabled,
		Resolver:       resolver,
	})
	logDiscoveryStats(states, paths, allSkills, activeSkills, disabled)
	return allSkills, activeSkills
}

// logTurnSkillUsage emits a per-turn diagnostic line showing which skills
// (if any) were loaded during this turn and which looked relevant based on
// a cheap keyword match against the user prompt. The goal is to surface
// "should-have-loaded but didn't" situations for later analysis.
//
// Logged at Info level under component=skills; heavy fields are elided when
// there is nothing interesting to report.
func logTurnSkillUsage(
	sessionID string,
	prompt string,
	activeSkills []*skills.Skill,
	tracker *skills.Tracker,
	before []string,
) {
	if tracker == nil || len(activeSkills) == 0 {
		return
	}

	after := tracker.LoadedNames()

	beforeSet := make(map[string]bool, len(before))
	for _, n := range before {
		beforeSet[n] = true
	}
	var loadedThisTurn []string
	for _, n := range after {
		if !beforeSet[n] {
			loadedThisTurn = append(loadedThisTurn, n)
		}
	}

	slog.Info(
		"Skill turn summary",
		"component", "skills",
		"session_id", sessionID,
		"prompt_len", len(prompt),
		"active_total", len(activeSkills),
		"loaded_total", len(after),
		"loaded_this_turn", loadedThisTurn,
	)
}

// logDiscoveryStats emits a single structured log line summarising skill
// discovery for the current session. It is intentionally low-volume: one
// line per session start. Builtin vs user counts are derived from the
// SkillState.Path — builtin states use the "builtin/" embed prefix.
func logDiscoveryStats(
	states []*skills.SkillState,
	userPaths []string,
	allSkills, activeSkills []*skills.Skill,
	disabled []string,
) {
	var builtinOK, builtinErr, userOK, userErr int
	for _, s := range states {
		isBuiltin := strings.HasPrefix(s.Path, "builtin/")
		switch {
		case isBuiltin && s.State == skills.StateNormal:
			builtinOK++
		case isBuiltin && s.State == skills.StateError:
			builtinErr++
		case !isBuiltin && s.State == skills.StateNormal:
			userOK++
		case !isBuiltin && s.State == skills.StateError:
			userErr++
		}
	}

	activeNames := make([]string, 0, len(activeSkills))
	for _, s := range activeSkills {
		activeNames = append(activeNames, s.Name)
	}

	xml := skills.ToPromptXML(activeSkills)

	slog.Info(
		"Skill discovery complete",
		"component", "skills",
		"builtin_ok", builtinOK,
		"builtin_errors", builtinErr,
		"user_ok", userOK,
		"user_errors", userErr,
		"user_paths", len(userPaths),
		"deduped_total", len(allSkills),
		"active", len(activeSkills),
		"disabled", len(disabled),
		"prompt_bytes", len(xml),
		"prompt_tok_est", skills.ApproxTokenCount(xml),
		"active_names", activeNames,
	)
}
