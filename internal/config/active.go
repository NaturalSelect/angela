package config

import (
	"encoding/json"
	"log/slog"
)

// ActiveAgent is a session's own instance of an agent configuration.
// It is instantiated by copying from the global config and is then
// owned by the session: editing it never writes back, so two sessions
// running the same agent can diverge freely.
//
// The model is materialized rather than named. Agent.Slot is a
// SlotName pointing into the shared Config.Slots table, so a
// session that only held the name could not change its own model
// without mutating that shared table.
type ActiveAgent struct {
	// Agent is the agent definition, copied from the global config.
	// It is a read-only snapshot: editing never writes to it, even
	// when the user picks a preset — that is what VariantPick is for.
	// Its Variant field is therefore always the config's own default.
	Agent Agent

	// Slot records which global model slot Model was instantiated
	// from. It is a label for display and for the model dialog, not a
	// live reference. It is never itself persisted: Restore always
	// re-derives it from InstantiateAgent, so reorganizing the slot
	// layout after a session was created is reflected immediately
	// instead of replaying a name that may no longer mean anything.
	Slot SlotName

	// Model is the materialized model configuration this session
	// runs.
	Model SelectedModel

	// AgentModels is the session's model-pick table, keyed by agent
	// ID. It is the single source of truth for what the user chose:
	// the entry for this instance's own agent is what Switch Model
	// writes, and the entries for other agents are what Switch Agent
	// Model writes. It travels from a session to every agent that
	// session dispatches or hosts, which is how those agents find the
	// pick made for them.
	AgentModels map[string]AgentModelPick

	// ModelPick is the model AgentModels holds for this instance's own
	// agent once ApplyPicks accepted it, or nil when the session never
	// chose one, in which case Model still tracks whatever the config
	// resolves for Slot. It is a derived view and is never persisted
	// on its own: State reads AgentModels only.
	ModelPick *SelectedModel

	// Think is the current thinking-mode state, resolved from the
	// model's catalog default and the active variant until ThinkPick
	// says otherwise.
	Think bool

	// ThinkPick is the thinking-mode value the user chose for this
	// session, or nil when they never touched it and Think keeps
	// following the model's catalog default and the active variant.
	// Mirrors VariantPick's reasoning.
	ThinkPick *bool

	// VariantPick is the preset AgentModels holds for this instance's
	// own agent, or nil when the session never chose one, in which
	// case EffectiveVariant falls back to Agent.Variant and then the
	// model's own default. Like ModelPick it is a derived view of
	// AgentModels. Only a pick is worth persisting: a session that
	// never touched the preset has to keep following the config, the
	// same way its prompt and tools do.
	VariantPick *string
}

// AgentModelPick is what a session chose for one agent: the model it
// runs on, the parameter preset, or both. Either half may be absent,
// since switching only the preset leaves the model to the config.
type AgentModelPick struct {
	// Model is the model the user picked, and is absent when they
	// only picked a preset: a config default must keep reaching an
	// agent whose model the user never overrode.
	Model SelectedModel `json:"model,omitzero"`

	// Variant is the preset the user picked, and is absent when they
	// never picked one. The distinction matters: Agent.Variant also
	// holds config-derived defaults, and persisting one of those
	// would freeze it. A pointer to the empty string is a real pick,
	// namely backing out of a preset.
	Variant *string `json:"variant,omitempty"`
}

// IsZero reports whether the pick chooses nothing.
func (p AgentModelPick) IsZero() bool {
	return p.Model == (SelectedModel{}) && p.Variant == nil
}

func (p AgentModelPick) clone() AgentModelPick {
	p.Model = p.Model.clone()
	p.Variant = clonePtr(p.Variant)
	return p
}

// clonePicks copies a pick table so two instances never share it. An
// empty table comes back nil, so "no picks" has one representation.
func clonePicks(picks map[string]AgentModelPick) map[string]AgentModelPick {
	if len(picks) == 0 {
		return nil
	}
	out := make(map[string]AgentModelPick, len(picks))
	for agentID, pick := range picks {
		out[agentID] = pick.clone()
	}
	return out
}

// ActiveAgentState is the persisted, session-scoped delta of an
// ActiveAgent: which agent it runs and which models the user actually
// picked for which agents, if any. The agent definition itself is
// deliberately absent — prompts, tools and permissions are re-read from
// the config files on every load so a session never runs a stale copy of
// them. The slot a model happens to run on is absent for the same reason:
// it is implementation-detail plumbing that InstantiateAgent always
// derives fresh from the config, never a user choice worth freezing
// here.
type ActiveAgentState struct {
	Agent string `json:"agent,omitempty"`

	// AgentModels holds the model and preset the user picked, keyed
	// by the agent they were picked for. The running agent's own
	// entry is what Switch Model writes; the entries for other
	// agents are what Switch Agent Model writes. It is absent when
	// the user never picked anything: a config default must keep
	// reaching a session that never overrode it.
	AgentModels map[string]AgentModelPick `json:"agent_models,omitempty"`

	// Think is the thinking-mode value the user picked, and is absent
	// when they never touched it, for the same reason AgentModels is
	// sparse: a model's catalog default must keep reaching a session
	// that never overrode it.
	Think *bool `json:"think,omitempty"`
}

// UnmarshalJSON reads both the current layout and the one written
// before picks were keyed by agent, where the running agent's model and
// preset sat at the top level as "model" and "variant". Those are
// folded into the entry for the recorded agent, unless the new layout
// already holds one. Writing always produces the current layout.
func (s *ActiveAgentState) UnmarshalJSON(data []byte) error {
	var wire struct {
		Agent       string                    `json:"agent"`
		AgentModels map[string]AgentModelPick `json:"agent_models"`
		Think       *bool                     `json:"think"`
		Model       SelectedModel             `json:"model"`
		Variant     *string                   `json:"variant"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*s = ActiveAgentState{
		Agent:       wire.Agent,
		AgentModels: wire.AgentModels,
		Think:       wire.Think,
	}
	legacy := AgentModelPick{Model: wire.Model, Variant: wire.Variant}
	if wire.Agent == "" || legacy.IsZero() {
		return nil
	}
	if _, exists := s.AgentModels[wire.Agent]; exists {
		return nil
	}
	if s.AgentModels == nil {
		s.AgentModels = make(map[string]AgentModelPick, 1)
	}
	s.AgentModels[wire.Agent] = legacy
	return nil
}

// IsZero reports whether the state names no agent, meaning the session
// has never been instantiated and the config alone decides.
func (s ActiveAgentState) IsZero() bool {
	return s.Agent == ""
}

// InstantiateAgent builds a session's own copy of an agent from the
// global config. It reports false when no such agent is resolved.
//
// An unknown or unset model name warns and falls back to SlotMain,
// matching warnUnknownTools' tolerant philosophy — a typo must not
// brick a turn. A missing SlotMain is left to the caller: it is the
// difference between a misconfigured agent and an unconfigured app.
//
// An agent whose slot is SlotInherited resolves here to the coder
// agent's own slot, since InstantiateAgent has no dispatcher to
// inherit a live model from — InstantiateUnder and InstantiateFor are
// the entry points that inherit an actual dispatcher's state. The
// returned instance's Agent.Slot is left as SlotInherited regardless
// (only the derived Slot label changes), so InstantiateUnder can tell
// this instance apart from one pinned to a concrete slot.
func (c *Config) InstantiateAgent(agentID string) (ActiveAgent, bool) {
	agent, ok := c.Agents[agentID]
	if !ok {
		return ActiveAgent{}, false
	}

	name := agent.Slot
	if name == SlotInherited {
		name = c.coderSlot()
	}
	model, ok := c.ModelForSlot(name)
	if !ok {
		if name != "" {
			slog.Warn("Unknown model config name; falling back to main",
				"agent", agentID, "model", name, "fallback", SlotMain)
		}
		name = SlotMain
		model = c.Slots[name]
	}

	active := ActiveAgent{Agent: agent, Slot: name, Model: model}
	active.Think = c.EffectiveThink(model, active.EffectiveVariant())
	return active.Clone(), true
}

// InstantiateWith builds an agent the way InstantiateAgent does and then
// applies the session's picks to it, so a caller that only holds the
// pick table sees the model that agent would actually run on.
func (c *Config) InstantiateWith(agentID string, picks map[string]AgentModelPick) (ActiveAgent, bool) {
	active, ok := c.InstantiateAgent(agentID)
	if !ok {
		return ActiveAgent{}, false
	}
	active, _ = c.ApplyPicks(active, picks)
	return active, true
}

// ApplyPicks lays a session's pick table over active and reports
// whether a model pick for active's own agent took effect. The whole
// table is kept on the result, not only the entry for this agent, so
// the agents it dispatches or hosts can find theirs.
//
// A model pick that no longer resolves, because its provider was
// disabled or the model left the catalog, is skipped with a warning
// and its preset with it, since a preset only means something on the
// model it was chosen for. The entry stays in the table, so the pick
// takes effect again once the model comes back. A pick that names only
// a preset applies that preset and reports false, because the model
// still comes from the config or from whoever dispatched the agent.
//
// Slot is left pointing at the agent's normal slot, so
// InstantiateFor's same-slot inheritance still applies to a picked
// model. Think is re-derived only when a model pick took effect and
// the user never picked a thinking value of their own.
func (c *Config) ApplyPicks(active ActiveAgent, picks map[string]AgentModelPick) (ActiveAgent, bool) {
	active.AgentModels = clonePicks(picks)
	active.ModelPick = nil
	active.VariantPick = nil

	pick, ok := active.AgentModels[active.Agent.ID]
	if !ok {
		return active, false
	}

	pinned := false
	if pick.Model.Provider != "" && pick.Model.Model != "" {
		if err := c.ValidateAgentModelOverride(active.Agent.ID, pick.Model); err != nil {
			slog.Warn("Ignoring a model pick that no longer resolves",
				"agent", active.Agent.ID, "error", err)
			return active, false
		}
		picked := pick.Model
		active.Model = picked
		active.ModelPick = &picked
		pinned = true
	}
	active.VariantPick = clonePtr(pick.Variant)

	if pinned && active.ThinkPick == nil {
		active.Think = c.EffectiveThink(active.Model, active.EffectiveVariant())
	}
	return active.Clone(), pinned
}

// coderSlot returns the model-config slot the coder agent resolved
// to, which is where an "inherited" agent lands when nothing
// dispatched it. The coder itself can never be SlotInherited —
// resolveCoderAgent downgrades that to SlotMain while resolving the
// config, before this is ever read — so the checks here only guard a
// Config assembled by hand, bypassing ResolveAgents, the way tests
// sometimes do.
func (c *Config) coderSlot() SlotName {
	slot := c.Agents[AgentCoder].Slot
	if slot == "" || slot == SlotInherited {
		return SlotMain
	}
	return slot
}

// inheritFrom overlays parent's live model state onto active, which is
// how a dispatched SlotInherited agent ends up running on whatever
// model actually dispatched it rather than a name in Config.Slots.
// Slot and Model are copied wholesale; ModelPick stays nil because an
// inherited value is never a user's own pick and must not be
// persisted as one.
//
// Variant follows EffectiveVariant's usual precedence: active's own
// Agent.Variant, when set, still outranks whatever parent is
// currently running, exactly as it would against a named slot's own
// default. Think mirrors that split — it is copied straight from
// parent when parent's Think was itself an explicit pick (ThinkPick)
// or when active has no variant of its own to reconsider it against,
// and is otherwise recomputed against active's own effective variant.
func (c *Config) inheritFrom(active, parent ActiveAgent) ActiveAgent {
	active.Slot = parent.Slot
	active.Model = parent.Model
	active.Model.Variant = parent.EffectiveVariant()
	active.ModelPick = nil

	if parent.ThinkPick != nil || active.Agent.Variant == "" {
		active.Think = parent.Think
	} else {
		active.Think = c.EffectiveThink(active.Model, active.EffectiveVariant())
	}

	return active.Clone()
}

// InstantiateUnder builds a dispatched agent's own instance, the way
// dispatching through the agent tool does. It behaves exactly like
// InstantiateAgent unless the agent's slot is SlotInherited, in which
// case it inherits parent's live model instead of falling back to the
// coder's slot — parent is the instance that actually dispatched this
// agent, which InstantiateAgent has no way to know about.
//
// An explicit model pick for this agent in parent's pick table still
// wins over inheritance, the same way it wins over a named slot: the
// user asked to override this agent specifically. A zero parent —
// nothing actually dispatched this agent, such as a primary agent's
// own top-level instantiation — leaves InstantiateAgent's coder-slot
// fallback in place.
func (c *Config) InstantiateUnder(agentID string, parent ActiveAgent) (ActiveAgent, bool) {
	active, ok := c.InstantiateAgent(agentID)
	if !ok {
		return ActiveAgent{}, false
	}
	active, pinned := c.ApplyPicks(active, parent.AgentModels)
	if pinned {
		return active, true
	}
	if active.Agent.Slot != SlotInherited || parent.Model.Provider == "" || parent.Model.Model == "" {
		return active, true
	}
	return c.inheritFrom(active, parent), true
}

// EffectiveThink resolves the thinking-mode default for model, as
// adjusted by the named variant, falling back to false when the
// model is not in the catalog. It is how a session's Think starts,
// and how it re-derives after the model or variant changes, until
// the user picks a value of their own.
func (c *Config) EffectiveThink(model SelectedModel, variant string) bool {
	catalog := c.GetModel(model.Provider, model.Model)
	if catalog == nil {
		return false
	}
	effective, _ := catalog.WithVariant(variant)
	return effective.Think
}

// ActiveAgentEdit describes a change to a session's own agent
// instance. Every field is optional and a zero edit changes nothing,
// so moving the agent, the model, the preset or the thinking flag all
// go through one request instead of one route each.
type ActiveAgentEdit struct {
	// Agent, when non-empty, re-instantiates the session on a
	// different primary agent.
	Agent string `json:"agent,omitempty"`

	// Model, when non-nil, replaces the session's model outright.
	Model *SelectedModel `json:"model,omitempty"`

	// Variant, when non-nil, sets the parameter preset. The empty
	// string selects the model's baseline, which is how a user backs
	// out of a preset.
	Variant *string `json:"variant,omitempty"`

	// Think, when non-nil, sets the thinking flag to an absolute
	// value. Callers that mean "flip it" must use ToggleThink: reading
	// the flag, flipping it locally and writing the result back races
	// another client doing the same, and two flips that should cancel
	// out instead both write true.
	Think *bool `json:"think,omitempty"`

	// ToggleThink flips the thinking flag against the value held under
	// the session's lock, so the read and the write cannot be split by
	// another edit. It takes precedence over Think.
	ToggleThink bool `json:"toggle_think,omitempty"`

	// ForAgent names the agent Model and Variant are picked for. Empty
	// means the agent the session runs once Agent has taken effect,
	// which is all that Switch Model is: Switch Agent Model without a
	// target. Any configured agent may be named, including hidden ones
	// and subagents, since a pick is only ever read when that agent is
	// dispatched.
	ForAgent string `json:"for_agent,omitempty"`

	// ClearModels drops every model and preset pick the session holds,
	// for every agent, so each one goes back to following the config.
	ClearModels bool `json:"clear_models,omitempty"`
}

// IsZero reports whether the edit asks for nothing.
func (e ActiveAgentEdit) IsZero() bool {
	return e.Agent == "" && e.Model == nil && e.Variant == nil &&
		e.Think == nil && !e.ToggleThink && !e.ClearModels
}

// InstantiateFor builds an agent instance that runs on behalf of a
// host instance, which is how Angela's internal agents (compaction,
// titling, agent generation) inherit the session's model choice.
//
// A session's instance is an override of one model role: it says "for
// this session, SlotMain is really this model". An internal agent on
// that same role inherits it, so a session switched to a big model
// compacts with the big model. An internal agent on a different role
// resolves that role from config, because the session never chose
// anything for it — which is what keeps titling cheap on SlotChore
// while the session itself runs on SlotMain. An internal agent whose
// own slot is SlotInherited always follows host, regardless of role,
// the same way InstantiateUnder follows a dispatcher.
//
// A zero host applies no override, so callers outside any session get
// plain config resolution.
func (c *Config) InstantiateFor(agentID string, host ActiveAgent) (ActiveAgent, bool) {
	active, ok := c.InstantiateAgent(agentID)
	if !ok {
		return ActiveAgent{}, false
	}
	active, pinned := c.ApplyPicks(active, host.AgentModels)
	if pinned {
		// An explicit pick on this agent wins over inheriting the
		// host's model: the user asked to override this agent
		// specifically, not whatever session it happens to run in.
		return active, true
	}
	if active.Agent.Slot == SlotInherited && host.Model.Provider != "" && host.Model.Model != "" {
		return c.inheritFrom(active, host), true
	}
	if active.Slot != host.Slot {
		return active, true
	}
	active.Model = host.Model
	active.Think = host.Think
	return active.Clone(), true
}

// CompactAgentIDFor returns the ID of the compact-mode agent that
// summarizes sessions host drives. host naming no agent, an unknown
// agent, or one that isn't compact-mode all fall back to the built-in
// "compact" agent — ResolveAgents already warned about the two error
// cases at load time, so this only needs to decide, quietly, which ID
// a turn actually resolves against.
func (c *Config) CompactAgentIDFor(host Agent) string {
	if host.CompactAgent == "" {
		return AgentCompact
	}
	target, ok := c.Agents[host.CompactAgent]
	if !ok || target.Mode != AgentModeCompact {
		slog.Debug("Host agent's compact_agent does not resolve; falling back to the built-in compact agent",
			"host", host.ID, "compact_agent", host.CompactAgent)
		return AgentCompact
	}
	return host.CompactAgent
}

// Clone returns a copy that shares no mutable state with a. Without
// it a session editing its own model would reach into the maps the
// published config still hands out to everyone else. The copy goes all
// the way down — nested provider options, per-variant presets and the
// tool whitelists included — because isolation that stops one level
// short is isolation nobody can rely on.
func (a ActiveAgent) Clone() ActiveAgent {
	a.Agent = a.Agent.clone()
	a.Model = a.Model.clone()
	a.AgentModels = clonePicks(a.AgentModels)
	a.ModelPick = clonePtr(a.ModelPick)
	a.ThinkPick = clonePtr(a.ThinkPick)
	a.VariantPick = clonePtr(a.VariantPick)
	return a
}

// EffectiveVariant resolves the parameter preset that actually
// governs this instance: the user's own pick when there is one,
// otherwise the agent's configured Variant, otherwise the slot's, so
// a slot can carry a sensible default that an agent leaves unset
// while an agent naming one of its own always wins.
//
// An explicit pick of the baseline — VariantPick set to a pointer to
// the empty string — is a deliberate opt-out and does not fall
// through to the agent or the slot either: a user backing out of a
// preset means to run with none, not with whatever those would have
// supplied.
func (a ActiveAgent) EffectiveVariant() string {
	if a.VariantPick != nil {
		return *a.VariantPick
	}
	if a.Agent.Variant != "" {
		return a.Agent.Variant
	}
	return a.Model.Variant
}

// State reduces the instance to the part worth persisting: what the
// user chose, never what the config supplied or what InstantiateAgent
// happened to derive. The pick table is the only place model and
// preset choices are read from; ModelPick and VariantPick are views
// of it and add nothing.
func (a ActiveAgent) State() ActiveAgentState {
	return ActiveAgentState{
		Agent:       a.Agent.ID,
		AgentModels: clonePicks(a.AgentModels),
		Think:       clonePtr(a.ThinkPick),
	}
}

// Restore rebuilds an instance from a persisted state: the agent
// definition comes from the config files as they are right now, the
// model and preset picks come from the state. That split is the whole
// point — a session keeps what the user chose for it while picking up
// edits to prompts, tools and permissions. A state that records no
// pick keeps following the configured model and preset.
//
// Slot is never taken from state either, and for the same reason: it
// always comes from whatever InstantiateAgent resolves right now, so
// a config that reorganizes its slot layout after the state was
// written is reflected immediately rather than replayed from a name
// that may no longer mean anything.
//
// It reports false when the recorded agent no longer resolves, which
// leaves the caller to decide on a fallback.
func (c *Config) Restore(state ActiveAgentState) (ActiveAgent, bool) {
	active, ok := c.InstantiateAgent(state.Agent)
	if !ok {
		return ActiveAgent{}, false
	}
	if state.Think != nil {
		pick := *state.Think
		active.Think = pick
		active.ThinkPick = &pick
	}
	active, _ = c.ApplyPicks(active, state.AgentModels)
	return active, true
}
