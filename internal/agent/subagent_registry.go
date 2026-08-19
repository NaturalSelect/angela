package agent

import (
	"context"
	"reflect"
	"sort"
	"sync"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/hooks"
)

// subagentEntry is one dispatchable subagent. The SessionAgent behind it
// is built on first dispatch rather than up front: building it resolves
// providers, renders a system prompt and assembles a tool list, and most
// sessions never dispatch to most agents.
//
// cfg is the resolved config the entry was created from. Reconcile
// compares against it to decide whether the cached agent is still valid,
// so an entry is immutable once published — a config change replaces the
// whole entry rather than mutating it, which resets the sync.Once and
// discards the stale agent along with its tools, prompt and hook runner.
type subagentEntry struct {
	cfg   config.Agent
	once  sync.Once
	agent SessionAgent
	err   error
}

// resolve builds the agent on first call and caches the outcome,
// including failure. A build error is returned to every later dispatch
// instead of being retried, matching the previous behaviour where a
// broken agent was skipped once at construction time.
func (e *subagentEntry) resolve(build func(config.Agent) (SessionAgent, error)) (SessionAgent, error) {
	e.once.Do(func() {
		e.agent, e.err = build(e.cfg)
	})
	return e.agent, e.err
}

// subagentRegistry is the coordinator's dispatch table. It is rebuilt
// from config on every UpdateModels call, so a hot config reload cannot
// leave an agent running with permissions the user has since revoked.
type subagentRegistry struct {
	mu      sync.RWMutex
	entries map[string]*subagentEntry
	hooks   []config.HookConfig
}

func newSubagentRegistry() *subagentRegistry {
	return &subagentRegistry{entries: map[string]*subagentEntry{}}
}

// Reconcile brings the table in line with the given resolved config.
// An entry is replaced when its config changed, when the PreToolUse hook
// set changed (hooks are baked into a built agent's tools), or when it
// is new; entries whose agent disappeared from config are dropped.
//
// Replacing rather than patching is deliberate: the cached SessionAgent
// captured the old config in its tool list and system prompt, so there
// is no way to narrow its permissions in place.
func (r *subagentRegistry) Reconcile(agents map[string]config.Agent, preToolHooks []config.HookConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	hooksChanged := !reflect.DeepEqual(r.hooks, preToolHooks)
	r.hooks = preToolHooks

	for id, cfg := range agents {
		if cfg.Mode == config.AgentModePrimary {
			continue
		}
		if existing, ok := r.entries[id]; ok && !hooksChanged && reflect.DeepEqual(existing.cfg, cfg) {
			continue
		}
		r.entries[id] = &subagentEntry{cfg: cfg}
	}

	for id := range r.entries {
		if cfg, ok := agents[id]; !ok || cfg.Mode == config.AgentModePrimary {
			delete(r.entries, id)
		}
	}
}

func (r *subagentRegistry) Get(id string) (*subagentEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	return e, ok
}

func (r *subagentRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// IDs returns the dispatchable agent IDs in sorted order.
func (r *subagentRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Metadata returns what the agent tool's description needs, sorted by ID
// so the rendered description is stable across calls. It reads config
// only, so it works before any agent has been built.
func (r *subagentRegistry) Metadata() []agentToolDescriptionAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]agentToolDescriptionAgent, 0, len(r.entries))
	for id, e := range r.entries {
		agents = append(agents, agentToolDescriptionAgent{ID: id, Description: e.cfg.Description})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

// Built returns the entries whose agent has already been built, so
// callers can refresh live agents without forcing the rest into
// existence.
func (r *subagentRegistry) Built() map[string]*subagentEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	built := make(map[string]*subagentEntry, len(r.entries))
	for id, e := range r.entries {
		if e.agent != nil {
			built[id] = e
		}
	}
	return built
}

// reconcileSubagents refreshes the dispatch table from the current
// config. Cheap enough for the run hot path: it compares config values
// and builds no agents.
func (c *coordinator) reconcileSubagents() {
	cfg := c.cfg.Config()
	c.subagents.Reconcile(cfg.Agents, cfg.Hooks[hooks.EventPreToolUse])
}

// buildSubAgentSync builds a subagent to completion. Unlike buildAgent
// it does not defer prompt and tool construction to the coordinator's
// readiness group: a subagent is built lazily on the dispatch path,
// where the caller can surface the error, and a failure here must
// poison only its own dispatch rather than the whole coordinator.
func (c *coordinator) buildSubAgentSync(ctx context.Context, agentCfg config.Agent) (SessionAgent, error) {
	p, err := agentPrompt(agentCfg, prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}

	primary, secondary, err := c.buildAgentModels(ctx, agentCfg, true)
	if err != nil {
		return nil, err
	}

	primaryProviderCfg, _ := c.cfg.Config().Providers.Get(primary.ModelCfg.Provider)
	result := NewSessionAgent(SessionAgentOptions{
		LargeModel:           primary,
		SmallModel:           secondary,
		SystemPromptPrefix:   primaryProviderCfg.SystemPromptPrefix,
		SystemPrompt:         "",
		IsSubAgent:           true,
		AgentName:            agentCfg.Name,
		DisableAutoSummarize: c.cfg.Config().Options.DisableAutoSummarize,
		IsYolo:               c.permissions.SkipRequests(),
		Sessions:             c.sessions,
		Messages:             c.messages,
		Tools:                nil,
		Notify:               c.notify,
		RunComplete:          c.runComplete,
	})

	systemPrompt, err := p.Build(ctx, primary.Model.Provider(), primary.Model.Model(), c.cfg)
	if err != nil {
		return nil, err
	}
	result.SetSystemPrompt(systemPrompt)

	agentTools, err := c.buildTools(ctx, agentCfg, true)
	if err != nil {
		return nil, err
	}
	result.SetTools(agentTools)

	return result, nil
}
