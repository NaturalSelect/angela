package config

import (
	"log/slog"
)

// ActiveAgent is a session's own instance of an agent configuration.
// It is instantiated by copying from the global config and is then
// owned by the session: editing it never writes back, so two sessions
// running the same agent can diverge freely.
//
// The model is materialized rather than named. Agent.Model is a
// ModelConfigName pointing into the shared Config.Models table, so a
// session that only held the name could not change its own model
// without mutating that shared table.
type ActiveAgent struct {
	// Agent is the agent definition, copied from the global config.
	// Its Variant field is the session's parameter preset.
	Agent Agent

	// ModelName records which global model slot Model was
	// instantiated from. It is a label for display and for the model
	// dialog, not a live reference.
	ModelName ModelConfigName

	// Model is the materialized model configuration this session
	// runs.
	Model SelectedModel

	// VariantPick is the preset the user chose for this session, or
	// nil when they never chose one and Agent.Variant is still
	// whatever the config says. Only a pick is worth persisting: a
	// session that never touched the preset has to keep following the
	// config, the same way its prompt and tools do.
	VariantPick *string
}

// ActiveAgentState is the persisted, session-scoped delta of an
// ActiveAgent: which agent it runs and which model that agent was
// pointed at. The agent definition itself is deliberately absent —
// prompts, tools and permissions are re-read from the config files on
// every load so a session never runs a stale copy of them.
type ActiveAgentState struct {
	Agent     string          `json:"agent,omitempty"`
	ModelName ModelConfigName `json:"model_name,omitempty"`
	Model     SelectedModel   `json:"model,omitzero"`

	// Variant is the preset the user picked, and is absent when they
	// never picked one. The distinction matters: Agent.Variant also
	// holds config-derived defaults, and persisting one of those
	// would freeze it — changing the default in the config file
	// would then never reach this session again. A pointer to the
	// empty string is a real pick, namely backing out of a preset.
	Variant *string `json:"variant,omitempty"`
}

// IsZero reports whether the state names no agent, meaning the session
// has never been instantiated and the config alone decides.
func (s ActiveAgentState) IsZero() bool {
	return s.Agent == ""
}

// InstantiateAgent builds a session's own copy of an agent from the
// global config. It reports false when no such agent is resolved.
//
// An unknown or unset model name warns and falls back to ModelMain,
// matching warnUnknownTools' tolerant philosophy — a typo must not
// brick a turn. A missing ModelMain is left to the caller: it is the
// difference between a misconfigured agent and an unconfigured app.
func (c *Config) InstantiateAgent(agentID string) (ActiveAgent, bool) {
	agent, ok := c.Agents[agentID]
	if !ok {
		return ActiveAgent{}, false
	}

	name := agent.Model
	model, ok := c.ModelForName(name)
	if !ok {
		if name != "" {
			slog.Warn("Unknown model config name; falling back to main",
				"agent", agentID, "model", name, "fallback", ModelMain)
		}
		name = ModelMain
		model = c.Models[name]
	}

	active := ActiveAgent{Agent: agent, ModelName: name, Model: model}
	return active.Clone(), true
}

// ActiveAgentEdit describes a change to a session's own agent
// instance. Every field is optional and a zero edit changes nothing,
// so moving the agent, the model, the preset or the thinking flag all
// go through one request instead of one route each.
type ActiveAgentEdit struct {
	// Agent, when non-empty, re-instantiates the session on a
	// different primary agent.
	Agent string `json:"agent,omitempty"`

	// Model, when non-nil, replaces the session's model outright, and
	// ModelName labels which global slot it was taken from.
	ModelName ModelConfigName `json:"model_name,omitempty"`
	Model     *SelectedModel  `json:"model,omitempty"`

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
}

// IsZero reports whether the edit asks for nothing.
func (e ActiveAgentEdit) IsZero() bool {
	return e.Agent == "" && e.Model == nil && e.Variant == nil &&
		e.Think == nil && !e.ToggleThink
}

// InstantiateFor builds an agent instance that runs on behalf of a
// host instance, which is how Angela's internal agents (compaction,
// titling, agentic fetch) inherit the session's model choice.
//
// A session's instance is an override of one model role: it says "for
// this session, ModelMain is really this model". An internal agent on
// that same role inherits it, so a session switched to a big model
// compacts with the big model. An internal agent on a different role
// resolves that role from config, because the session never chose
// anything for it — which is what keeps titling cheap on ModelChore
// while the session itself runs on ModelMain.
//
// A zero host applies no override, so callers outside any session get
// plain config resolution.
func (c *Config) InstantiateFor(agentID string, host ActiveAgent) (ActiveAgent, bool) {
	active, ok := c.InstantiateAgent(agentID)
	if !ok {
		return ActiveAgent{}, false
	}
	if active.ModelName != host.ModelName {
		return active, true
	}
	active.Model = host.Model
	return active.Clone(), true
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
	a.VariantPick = clonePtr(a.VariantPick)
	return a
}

// State reduces the instance to the part worth persisting: what the
// user chose, never what the config supplied.
func (a ActiveAgent) State() ActiveAgentState {
	return ActiveAgentState{
		Agent:     a.Agent.ID,
		ModelName: a.ModelName,
		Model:     a.Model,
		Variant:   a.VariantPick,
	}
}

// Restore rebuilds an instance from a persisted state: the agent
// definition comes from the config files as they are right now, the
// model selection and the preset pick come from the state. That split
// is the whole point — a session keeps what the user chose for it
// while picking up edits to prompts, tools and permissions. A state
// that records no preset pick keeps following the configured one.
//
// It reports false when the recorded agent no longer resolves, which
// leaves the caller to decide on a fallback.
func (c *Config) Restore(state ActiveAgentState) (ActiveAgent, bool) {
	active, ok := c.InstantiateAgent(state.Agent)
	if !ok {
		return ActiveAgent{}, false
	}
	if state.Variant != nil {
		pick := *state.Variant
		active.Agent.Variant = pick
		active.VariantPick = &pick
	}
	if state.Model.Model == "" || state.Model.Provider == "" {
		return active, true
	}
	active.ModelName = state.ModelName
	active.Model = state.Model
	return active.Clone(), true
}
