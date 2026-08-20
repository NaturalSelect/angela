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
}

// resolveAgent turns an agent's configuration into the immutable value
// a turn runs on. Every turn calls this, so a config edit takes effect
// on the next turn without anything being mutated underneath a turn
// already in flight.
func (c *coordinator) resolveAgent(ctx context.Context, agentCfg config.Agent, isSubAgent bool) (resolvedAgent, error) {
	model, err := c.buildModel(ctx, agentCfg, isSubAgent)
	if err != nil {
		return resolvedAgent{}, err
	}

	tools, err := c.buildTools(ctx, agentCfg, isSubAgent)
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
