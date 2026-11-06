package agent

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
)

// draftSessionID names the landing page's draft instance: the one a
// user edits before any session exists. It is never a real session
// ID, so its delta lives only in activeAgentStore's cache and is
// never read from or written to the session table — see
// loadActiveAgent and saveActiveAgent.
const draftSessionID = ""

// activeIO wires the store to the session record and the config.
func (c *coordinator) activeIO() activeAgentIO {
	return activeAgentIO{
		load:        c.loadActiveAgent,
		materialize: c.materializeActiveAgent,
		save:        c.saveActiveAgent,
	}
}

// loadActiveAgent reads a session's persisted delta. A session that
// cannot be read at all is an error: running a turn on a guessed agent
// would silently answer as something the user did not pick.
//
// The draft always "reads" as freshly unpicked: there is no session
// row behind it, so its only record is whatever activeAgentStore has
// cached from an earlier edit in this process.
func (c *coordinator) loadActiveAgent(ctx context.Context, sessionID string) (config.ActiveAgentState, error) {
	if sessionID == draftSessionID {
		return config.ActiveAgentState{}, nil
	}
	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return config.ActiveAgentState{}, fmt.Errorf("read session %q: %w", sessionID, err)
	}
	return sess.ActiveAgent, nil
}

// saveActiveAgent persists a session's delta. The draft has no row to
// write it to; activeAgentStore.edit caches the result regardless of
// what save returns, which is what keeps the draft's edits visible
// for the rest of the process.
func (c *coordinator) saveActiveAgent(ctx context.Context, sessionID string, state config.ActiveAgentState) error {
	if sessionID == draftSessionID {
		return nil
	}
	return c.sessions.UpdateActiveAgent(ctx, sessionID, state)
}

// materializeActiveAgent turns a session's delta into a live instance.
// The agent definition always comes from the config as it stands now;
// only the model selection comes from the delta. That split is the
// point: editing the config steers prompts and tools on the next turn,
// while a model the user picked for this session stays picked.
//
// A session with no delta yet runs the config-level default agent —
// the coder, unless a pre-session pick pointed it elsewhere — and
// keeps following that default until it picks something of its own.
// A session naming an agent config has since removed, disabled or
// hidden falls back the same way — erroring there would strand the
// session with no way to type the command that fixes it. The fallback
// still carries both pick layers: the picks it holds for other agents
// are read when it dispatches them, and the next edit writes the layers
// back from this instance, so dropping them here would erase them for
// good.
func (c *coordinator) materializeActiveAgent(sessionID string, state config.ActiveAgentState) (config.ActiveAgent, error) {
	cfg := c.cfg.Config()

	if state.IsZero() {
		return defaultAgentInstance(cfg)
	}
	if active, ok := cfg.Restore(state); ok && !active.Agent.IsHidden() {
		return active, nil
	}
	slog.Warn("Session points at an agent that is no longer available; falling back to the default",
		"sessionID", sessionID, "agent", state.Agent)
	fallback, ok := cfg.InstantiateLayered(config.AgentCoder, state.SessionModel, state.AgentPicks, config.ActiveAgent{})
	if !ok {
		return config.ActiveAgent{}, errCoderAgentNotConfigured
	}
	return fallback, nil
}

// defaultAgentInstance builds the agent instance a session runs before
// it has picked one of its own: the built-in coder.
func defaultAgentInstance(cfg *config.Config) (config.ActiveAgent, error) {
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
// An edit lands on one of two places and never both. Model, Variant,
// Think and the session reset change the session's own state, the
// nearest layer of the model stack. An edit aimed at an agent with
// ForAgent, or the reset of those picks, changes the session tree's
// per-agent picks, which live on the root session whatever session the
// edit was made in. Mixing the two in one edit is an error.
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
		return c.runningInstance(ctx, sessionID)
	}
	if edit.TreeWide() && edit.SessionWide() {
		return config.ActiveAgent{}, ErrMixedActiveAgentEdit
	}
	if edit.TreeWide() {
		return c.editAgentPicks(ctx, sessionID, edit)
	}

	cfg := c.cfg.Config()
	if edit.Agent != "" {
		if err := checkPrimaryAgent(cfg, edit.Agent); err != nil {
			return config.ActiveAgent{}, err
		}
	}
	scope, err := c.scopeFor(ctx, sessionID)
	if err != nil {
		return config.ActiveAgent{}, err
	}
	if scope.child {
		if edit.Agent != "" && edit.Agent != scope.agentID {
			return config.ActiveAgent{}, fmt.Errorf("%w: %q cannot replace the agent dispatched into a child session", ErrAgentNotAvailable, edit.Agent)
		}
		// A branch is a conversation the user drives, so it keeps a
		// model of its own. A sub-agent runs on what its tree gives it,
		// and so does a branch once it has been merged or abandoned.
		if !c.branches.Waiting(sessionID) {
			return config.ActiveAgent{}, ErrSubAgentModelLocked
		}
	}

	var (
		change   activeAgentChange
		switched Model
		agentID  string
		result   config.ActiveAgent
	)
	err = c.active.editState(ctx, sessionID, c.activeIO(), func(record config.ActiveAgentState, stored config.ActiveAgent) (config.ActiveAgentState, bool, error) {
		current := stored
		if scope.child {
			resolved, ok := scope.resolve(cfg, record)
			if !ok {
				return config.ActiveAgentState{}, false, fmt.Errorf("%w: agent %q is no longer configured", ErrSubSessionNotResumable, scope.agentID)
			}
			current = resolved
		}
		result = current
		next, nextRecord, moved, err := applyActiveAgentEdit(cfg, scope, current, record, edit)
		if err != nil || !moved.any() {
			return config.ActiveAgentState{}, false, err
		}
		model, err := c.models().buildModel(ctx, next, false)
		if err != nil {
			return config.ActiveAgentState{}, false, fmt.Errorf("resolve the session's model: %w", err)
		}
		// An unknown preset is an error here, where the user is
		// watching and can pick again; per-turn resolution stays
		// lenient about the same name for the opposite reason.
		// Only validate an explicit user pick: agent-config and slot
		// defaults fall through leniently at turn time, so rejecting
		// them here would block a model switch merely because the
		// agent's configured default is absent from the new model.
		if v := next.VariantPick; v != nil && *v != "" && !slices.Contains(model.CatwalkCfg.VariantNames(), *v) {
			return config.ActiveAgentState{}, false, fmt.Errorf("%w: %q on %q", ErrVariantNotAvailable, *v, model.ModelCfg.Model)
		}
		change, switched, agentID, result = moved, model, next.Agent.ID, next
		return nextRecord, true, nil
	})
	if err != nil {
		return config.ActiveAgent{}, err
	}
	if switched.Model == nil {
		return result, nil
	}
	if sessionID == draftSessionID {
		// The draft has no transcript to explain itself to.
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

// editAgentPicks applies a tree-wide edit to the root session of
// sessionID's tree and returns the instance sessionID runs afterwards.
// The pick is read on every turn of every session below the root, so it
// takes effect from the next turn on, in the session the edit was made in
// and in every other one. Only the root's lock is taken: the editing
// session's own state is not touched.
//
// These edits leave no trail in the transcript. The pick is not a change
// to the session being viewed, and the root may be a different session.
func (c *coordinator) editAgentPicks(ctx context.Context, sessionID string, edit config.ActiveAgentEdit) (config.ActiveAgent, error) {
	cfg := c.cfg.Config()
	root := c.rootSessionID(ctx, sessionID)
	err := c.active.editState(ctx, root, c.activeIO(), func(record config.ActiveAgentState, stored config.ActiveAgent) (config.ActiveAgentState, bool, error) {
		picks, moved, err := applyAgentPickEdit(cfg, record.AgentPicks, edit)
		if err != nil || !moved {
			return config.ActiveAgentState{}, false, err
		}
		// A record naming no agent reads as a session that never picked
		// anything, which would hide the table just written.
		record.Agent = cmp.Or(record.Agent, stored.Agent.ID)
		record.AgentPicks = picks
		return record, true, nil
	})
	if err != nil {
		return config.ActiveAgent{}, err
	}
	return c.runningInstance(ctx, sessionID)
}

// AdoptDraft copies the landing page's draft pick, if any, onto
// sessionID, a session that was just created. Without this a model
// or preset chosen before any session existed would be silently
// discarded the moment the first session came into being, and the
// session the user just watched get configured would open on
// whatever the config's own defaults are instead.
//
// The draft is consumed by this call rather than left in place: once
// the pick has landed on sessionID, the draft's own cached state is
// reset to zero. A landing-page pick belongs to whichever session the
// user is about to start, not to every session started afterward —
// leaving it in place would let a second, unrelated new session
// silently inherit the same stale one-time pick instead of falling
// back to whatever the config actually specifies.
func (c *coordinator) AdoptDraft(ctx context.Context, sessionID string) error {
	// The draft's lock is held for the whole operation, not just the
	// read below: releasing it early would let a concurrent edit on
	// the draft land between the read and this call's own write,
	// which this call would have no way to notice since it has
	// already captured draft by value. Holding it throughout instead
	// makes the two calls linearize: whichever reaches the lock first
	// is the one the other observes. sessionID is a session nobody
	// else can reference yet, so nothing ever holds its lock while
	// waiting on the draft's, and this cannot deadlock.
	l := c.active.lockFor(draftSessionID)
	defer c.active.release(draftSessionID, l)
	l.mu.Lock()
	defer l.mu.Unlock()

	draft, ok := c.active.cachedState(draftSessionID)
	if !ok || draft.IsZero() {
		return nil
	}

	if err := c.active.edit(ctx, sessionID, c.activeIO(), func(config.ActiveAgent) (config.ActiveAgent, bool, error) {
		active, err := c.materializeActiveAgent(sessionID, draft)
		if err != nil {
			return config.ActiveAgent{}, false, err
		}
		return active, true, nil
	}); err != nil {
		return err
	}

	// The pick just copied onto sessionID was a one-time landing-page
	// choice, not a standing preference. Clearing it here — still
	// under the draft's lock acquired above — is what stops a later
	// session from adopting that same stale pick a second time.
	c.active.remember(draftSessionID, config.ActiveAgentState{})
	return nil
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
	picksMoved   bool
	thinkMoved   bool
}

func (ch activeAgentChange) any() bool {
	return ch.agentMoved || ch.variantMoved || ch.picksMoved || ch.thinkMoved
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

// applyActiveAgentEdit folds a session-wide edit into a session and
// reports what moved. It is pure: the validation that needs a built
// model happens in the caller, once, on the result.
//
// current is the instance the session runs and record is the state its
// row holds. For a top-level session they say the same thing. For a
// child, current also carries what its tree supplies, so an edit is
// judged against current but written onto record alone: the returned
// record holds only the child's own layer and never the tree's picks.
//
// Model and Variant land in the session's own model, the nearest layer
// of the stack, and act on whichever agent the session runs once
// edit.Agent has taken effect. The edit is applied to the persisted form
// of the instance and the instance is then rebuilt from it, which is
// what keeps a live session and a restored one from ever disagreeing.
//
// Switching agent keeps the session's own model: that layer belongs to
// the session, not to the agent it happened to run when it was picked.
// The caller has already refused a switch inside a child, whose agent is
// the one that was dispatched into it.
func applyActiveAgentEdit(
	cfg *config.Config,
	scope sessionScope,
	current config.ActiveAgent,
	record config.ActiveAgentState,
	edit config.ActiveAgentEdit,
) (config.ActiveAgent, config.ActiveAgentState, activeAgentChange, error) {
	var change activeAgentChange
	state := current.State()
	if scope.child {
		state = scope.ownState(record)
	}

	if edit.Agent != "" && edit.Agent != current.Agent.ID {
		agent, ok := cfg.Agents[edit.Agent]
		if !ok {
			return current, record, change, fmt.Errorf("%w: %q", ErrAgentNotAvailable, edit.Agent)
		}
		state.Agent = edit.Agent
		change.agentFrom, change.agentTo, change.agentMoved = current.Agent.ID, agent, true
	}

	if edit.ClearSessionModel && (!state.SessionModel.IsZero() || state.Think != nil) {
		state.SessionModel = config.AgentModelPick{}
		state.Think = nil
		change.picksMoved = true
	}

	if edit.Model != nil || edit.Variant != nil {
		pick, err := applySessionPickEdit(cfg, scope, state, edit, &change)
		if err != nil {
			return current, record, change, err
		}
		state.SessionModel = pick
	}

	next, ok := scope.resolve(cfg, state)
	if !ok {
		return current, record, change, fmt.Errorf("%w: %q", ErrAgentNotAvailable, state.Agent)
	}

	if think, ok := thinkAfter(edit, next.Think); ok {
		next.Think = think
		pick := think
		next.ThinkPick = &pick
		state.Think = &pick
		change.thinkMoved = true
	}

	return next.Clone(), state, change, nil
}

// applySessionPickEdit folds the Model and Variant of an edit into the
// session's own model and returns the new one. state.Agent is already
// the agent the session will run, so the edit is judged against what
// that agent runs now, the picks of every layer included.
//
// Picking the model the session already runs records nothing: whether
// that model comes from the config, the tree or the dispatcher, the
// session keeps following it until the user chooses something else.
func applySessionPickEdit(cfg *config.Config, scope sessionScope, state config.ActiveAgentState, edit config.ActiveAgentEdit, change *activeAgentChange) (config.AgentModelPick, error) {
	instantiate := func(local config.AgentModelPick) (config.ActiveAgent, bool) {
		own := state
		own.SessionModel = local
		return scope.resolve(cfg, own)
	}

	running, ok := instantiate(state.SessionModel)
	if !ok {
		return config.AgentModelPick{}, fmt.Errorf("%w: %q", ErrAgentNotAvailable, state.Agent)
	}

	pick := state.SessionModel

	if edit.Model != nil && !sameModel(*edit.Model, running.Model) {
		if err := cfg.ValidateAgentModelOverride(state.Agent, *edit.Model); err != nil {
			return config.AgentModelPick{}, err
		}
		// A preset is a different way to call the model it names,
		// not a property of the agent: carrying a pick across to a
		// model that never offered it either fails validation or
		// silently applies a preset the user never chose for it
		// there. Dropping it falls back to Agent.Variant, which
		// already holds whatever the agent's config says today.
		pick.Model, pick.Variant = *edit.Model, nil
		change.picksMoved = true
	}

	if edit.Variant != nil {
		running, _ = instantiate(pick)

		// Compared against the effective preset, not the agent's raw
		// field: a slot can be supplying one while the field reads
		// empty, and picking that same name (including the baseline)
		// must still count as a move so the pick gets recorded.
		effective := running.EffectiveVariant()
		variant := *edit.Variant
		if variant != effective {
			change.variantFrom, change.variantTo, change.variantMoved = effective, variant, true
		}

		// The preset is validated by the caller against the model it
		// builds, where a missing one has its own error.
		//
		// Recorded even when it matches what the config says today:
		// the user picked this preset, so later editing the config's
		// default must not move them off it.
		pick.Variant = &variant
	}

	return pick, nil
}

// applyAgentPickEdit folds a tree-wide edit into the session tree's
// per-agent pick table and returns the new table, and whether anything
// moved. Model and Variant act on edit.ForAgent, which may be any
// configured agent: it is only read when that agent is dispatched or
// hosted.
//
// A model is compared against the agent's own pick only. What the agent
// resolves to without one can be an inherited model that changes under
// it, and the user asked for this model specifically. The table passed
// in is not edited, so an edit that fails leaves the cached record alone.
func applyAgentPickEdit(cfg *config.Config, picks map[string]config.AgentModelPick, edit config.ActiveAgentEdit) (map[string]config.AgentModelPick, bool, error) {
	picks = maps.Clone(picks)
	moved := false

	if edit.ClearAgentPicks && len(picks) > 0 {
		picks = nil
		moved = true
	}
	if edit.Model == nil && edit.Variant == nil {
		return picks, moved, nil
	}

	targetID := edit.ForAgent
	if _, ok := cfg.InstantiateWith(targetID, picks); !ok {
		return nil, false, fmt.Errorf("%w: %s", config.ErrAgentOverrideUnknownAgent, targetID)
	}
	pick := picks[targetID]

	if edit.Model != nil && !sameModel(*edit.Model, pick.Model) {
		if err := cfg.ValidateAgentModelOverride(targetID, *edit.Model); err != nil {
			return nil, false, err
		}
		// Dropping the preset here follows the same rule as a
		// session's own pick: it belonged to the model just replaced.
		pick.Model, pick.Variant = *edit.Model, nil
		moved = true
	}

	if edit.Variant != nil {
		picks = withPick(picks, targetID, pick)
		target, _ := cfg.InstantiateWith(targetID, picks)
		variant := *edit.Variant
		if variant != target.EffectiveVariant() {
			moved = true
		}

		// The agent is never built here, so its preset is checked
		// against its model now, while the user is watching.
		if variant != "" {
			candidate := target.Model
			candidate.Variant = variant
			if err := cfg.ValidateAgentModelOverride(targetID, candidate); err != nil {
				return nil, false, err
			}
		}
		pick.Variant = &variant
	}

	return withPick(picks, targetID, pick), moved, nil
}

// withPick sets agentID's entry in picks, creating the table when it is
// nil and dropping the entry when the pick chooses nothing, so a table
// never holds an entry that says nothing.
func withPick(picks map[string]config.AgentModelPick, agentID string, pick config.AgentModelPick) map[string]config.AgentModelPick {
	if pick.IsZero() {
		delete(picks, agentID)
		return picks
	}
	if picks == nil {
		picks = make(map[string]config.AgentModelPick, 1)
	}
	picks[agentID] = pick
	return picks
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
