package agent

import (
	"context"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
)

// resolveInternalAgent looks up one of Angela's hidden built-in agents
// and resolves everything a one-shot LLM call needs from it: the model
// it runs on and its system prompt.
//
// Hidden agents are not in the subagent registry (they are never
// dispatchable), so they are resolved straight from config each time
// they are used — which is also what lets a user retune them without a
// restart.
//
// host is the instance this call runs on behalf of, and lets the
// internal agent inherit that session's model where the two share a
// model role. Callers outside any session pass the zero value.
func (c *coordinator) resolveInternalAgent(ctx context.Context, agentID string, host config.ActiveAgent, promptOpts ...prompt.Option) (active config.ActiveAgent, model Model, systemPrompt string, err error) {
	active, ok := c.cfg.Config().InstantiateFor(agentID, host)
	if !ok {
		return config.ActiveAgent{}, Model{}, "", fmt.Errorf("agent %q not configured", agentID)
	}

	model, err = c.buildModel(ctx, active, true)
	if err != nil {
		return config.ActiveAgent{}, Model{}, "", err
	}

	p, err := agentPrompt(active.Agent, promptOpts...)
	if err != nil {
		return config.ActiveAgent{}, Model{}, "", err
	}
	systemPrompt, err = p.Build(ctx, model.Model.Provider(), model.Model.Model(), c.cfg)
	if err != nil {
		return config.ActiveAgent{}, Model{}, "", err
	}

	return active, model, systemPrompt, nil
}

// buildCompactAgent resolves the compact agent into the value a turn
// carries so it can summarize itself mid-flight. Resolution failures
// are logged and recorded on the returned value rather than aborting:
// compaction is a recovery path, and refusing to start the turn
// because it could not be prepared would be worse than the turn
// running without it. Callers check Available before using it.
func (c *coordinator) buildCompactAgent(ctx context.Context, host config.ActiveAgent) resolvedAgent {
	active, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentCompact, host)
	if err != nil {
		slog.Error("Failed to resolve the compact agent", "error", err)
		return resolvedAgent{Err: err}
	}
	providerCfg, _ := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	return resolvedAgent{
		ID:                 active.Agent.ID,
		Name:               active.Agent.Name,
		Model:              model,
		SystemPrompt:       systemPrompt,
		SystemPromptPrefix: providerCfg.SystemPromptPrefix,
		MaxTokens:          maxTokensFor(active.Agent, model),
		Host:               active,
		RebuildModel: func(ctx context.Context) (fantasy.LanguageModel, error) {
			_, rebuilt, _, err := c.resolveInternalAgent(ctx, config.AgentCompact, host)
			if err != nil {
				return nil, err
			}
			return rebuilt.Model, nil
		},
	}
}
