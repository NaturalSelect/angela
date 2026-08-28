package agent

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"

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
}

// executor returns the entry's SessionAgent, building it on first call.
// It holds only run state — queues, active requests, accept bookkeeping
// — so it is shared across dispatches; everything config-derived is
// resolved per dispatch instead.
func (e *subagentEntry) executor(build func(config.Agent) SessionAgent) SessionAgent {
	e.once.Do(func() {
		e.agent = build(e.cfg)
	})
	return e.agent
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
		if cfg.Mode == config.AgentModePrimary || cfg.IsHidden() {
			continue
		}
		if existing, ok := r.entries[id]; ok && !hooksChanged && reflect.DeepEqual(existing.cfg, cfg) {
			continue
		}
		r.entries[id] = &subagentEntry{cfg: cfg}
	}

	for id := range r.entries {
		if cfg, ok := agents[id]; !ok || cfg.Mode == config.AgentModePrimary || cfg.IsHidden() {
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
		agents = append(agents, agentToolDescriptionAgent{
			ID:          id,
			Description: e.cfg.Description,
			Branch:      e.cfg.Mode == config.AgentModeBranch,
		})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

// reconcileSubagents refreshes the dispatch table from the current
// config. Cheap enough for the run hot path: it compares config values
// and builds no agents.
func (c *coordinator) reconcileSubagents() {
	cfg := c.cfg.Config()
	c.subagents.Reconcile(cfg.Agents, cfg.Hooks[hooks.EventPreToolUse])
}

// dispatchSubAgent produces what a dispatch needs: the cached executor
// for this agent, plus a resolution made fresh for this dispatch.
//
// The split matters. The executor holds per-session run state (queues,
// active requests, accept bookkeeping) and must survive across
// dispatches, so it is cached on the registry entry. The resolution
// holds model, tools and prompt and must not be shared, so it is built
// per dispatch and travels on the call. Resolving here rather than at
// registration also means a dispatch that cannot reach its provider
// fails as a tool error instead of poisoning the coordinator.
//
// depth is the dispatch depth the new subagent runs at (its dispatcher's
// depth plus one). It is never baked into the cached executor: the same
// entry can be reused across dispatches at different depths, and only
// the per-dispatch resolution (its tool list) is depth-sensitive.
// subagentExecutor returns the entry's cached executor, building it on first
// use. Unlike dispatchSubAgent it resolves no identity, so callers that only
// need somewhere to run — routing and cancellation — do not pay for a model
// resolution they would throw away.
func (c *coordinator) subagentExecutor(entry *subagentEntry) SessionAgent {
	return entry.executor(func(agentCfg config.Agent) SessionAgent {
		return c.buildAgent(agentCfg.ID, true)
	})
}

func (c *coordinator) dispatchSubAgent(ctx context.Context, entry *subagentEntry, depth int) (SessionAgent, resolvedAgent, error) {
	// A subagent is instantiated fresh for every dispatch and never
	// stored: it has no session of its own to own an instance, and its
	// model is whatever config says right now.
	active, ok := c.cfg.Config().InstantiateAgent(entry.cfg.ID)
	if !ok {
		return nil, resolvedAgent{}, fmt.Errorf("%w: %q", ErrAgentNotAvailable, entry.cfg.ID)
	}
	resolved, err := c.resolveAgent(ctx, active, depth)
	if err != nil {
		return nil, resolvedAgent{}, err
	}
	return c.subagentExecutor(entry), resolved, nil
}
