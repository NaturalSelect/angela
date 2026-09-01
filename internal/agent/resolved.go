package agent

import (
	"context"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
)

// resolvedAgent is everything a turn needs to know about the agent it
// runs on, frozen at the moment the turn was dispatched.
//
// It is a value, and that is the whole point. The session agent used to
// hold the model, tools and system prompt as mutable fields that
// UpdateModels rewrote in place; a second session resolving its own
// agent would then silently rewrite what a running turn was using. A
// turn now carries its own copy, so no other session can reach it.
type resolvedAgent struct {
	ID                 string
	Name               string
	Model              Model
	Tools              []fantasy.AgentTool
	SystemPrompt       string
	SystemPromptPrefix string
	MaxTokens          int64

	// Host is the instance this turn was resolved from. Internal
	// agents dispatched on the turn's behalf resolve through it, so
	// compaction follows the model the session actually picked.
	Host config.ActiveAgent

	// RebuildModel re-resolves Model against the config as it stands
	// now. Refreshing credentials does not touch the instance frozen
	// here, so a retry after a 401 needs a freshly built one.
	RebuildModel func(context.Context) (fantasy.LanguageModel, error)

	// Err holds why this agent could not be resolved. Only
	// buildCompactAgent populates it: compaction is a recovery path,
	// not a precondition, so a resolution failure is recorded here
	// instead of aborting the turn. Callers must check Available
	// before assuming Model is usable.
	Err error
}

// Available reports whether this agent resolved cleanly and can
// actually be run. Meaningful for values that might carry Err, such
// as the compact agent; resolveAgent's own callers get a Go error
// instead and never populate it.
func (r resolvedAgent) Available() bool {
	return r.Err == nil && r.Model.Model != nil
}

// resolveAgent turns an agent's configuration into the immutable value
// a turn runs on. Every turn calls this, so a config edit takes effect
// on the next turn without anything being mutated underneath a turn
// already in flight.
//
// depth is how many delegation hops separate this agent from the
// top-level primary agent: 0 for a primary turn, 1 for a subagent it
// dispatched, 2 for a subagent that subagent dispatched, and so on. It
// drives both isSubAgent-flavored behavior (depth > 0) and, in
// buildTools, whether this agent is still within its delegation budget.
func (c *coordinator) resolveAgent(ctx context.Context, active config.ActiveAgent, depth int) (resolvedAgent, error) {
	agentCfg := active.Agent
	isSubAgent := depth > 0

	model, err := c.buildModel(ctx, active, isSubAgent)
	if err != nil {
		return resolvedAgent{}, err
	}

	tools, err := c.buildTools(agentCfg, model.CatwalkCfg.ID, depth)
	if err != nil {
		return resolvedAgent{}, err
	}

	p, err := agentPrompt(agentCfg, prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return resolvedAgent{}, err
	}
	systemPrompt, err := p.Build(ctx, model.Model.Provider(), model.Model.Model(), c.cfg)
	if err != nil {
		return resolvedAgent{}, err
	}

	providerCfg, _ := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)

	return resolvedAgent{
		ID:                 agentCfg.ID,
		Name:               agentCfg.Name,
		Model:              model,
		Tools:              tools,
		SystemPrompt:       systemPrompt,
		SystemPromptPrefix: providerCfg.SystemPromptPrefix,
		MaxTokens:          maxTokensFor(agentCfg, model),
		Host:               active,
		RebuildModel: func(ctx context.Context) (fantasy.LanguageModel, error) {
			rebuilt, err := c.buildModel(ctx, active, isSubAgent)
			if err != nil {
				return nil, err
			}
			return rebuilt.Model, nil
		},
	}, nil
}

// maxTokensFor picks the output cap for a turn: the agent's own
// override when set, otherwise the model's configured cap, otherwise
// the model's catalog default.
func maxTokensFor(agentCfg config.Agent, model Model) int64 {
	if agentCfg.MaxTokens != nil && *agentCfg.MaxTokens > 0 {
		return *agentCfg.MaxTokens
	}
	if model.ModelCfg.MaxTokens != 0 {
		return model.ModelCfg.MaxTokens
	}
	return model.CatwalkCfg.DefaultMaxTokens
}
