package agent

import (
	"context"
	"fmt"
	"log/slog"

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
func (c *coordinator) resolveInternalAgent(ctx context.Context, agentID string, promptOpts ...prompt.Option) (agentCfg config.Agent, model Model, systemPrompt string, err error) {
	agentCfg, ok := c.cfg.Config().Agents[agentID]
	if !ok {
		return config.Agent{}, Model{}, "", fmt.Errorf("agent %q not configured", agentID)
	}

	model, err = c.buildModel(ctx, agentCfg, true)
	if err != nil {
		return config.Agent{}, Model{}, "", err
	}

	p, err := agentPrompt(agentCfg, promptOpts...)
	if err != nil {
		return config.Agent{}, Model{}, "", err
	}
	systemPrompt, err = p.Build(ctx, model.Model.Provider(), model.Model.Model(), c.cfg)
	if err != nil {
		return config.Agent{}, Model{}, "", err
	}

	return agentCfg, model, systemPrompt, nil
}

// buildCompactAgent resolves the compact agent into the value a turn
// carries so it can summarize itself mid-flight. Resolution failures
// are logged and yield a zero CompactAgent: compaction is a recovery
// path, and refusing to start the turn because it could not be
// prepared would be worse than the turn running without it.
func (c *coordinator) buildCompactAgent(ctx context.Context) CompactAgent {
	_, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentCompact)
	if err != nil {
		slog.Error("Failed to resolve the compact agent", "error", err)
		return CompactAgent{}
	}
	providerCfg, _ := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	return CompactAgent{
		Model:              model,
		SystemPrompt:       systemPrompt,
		SystemPromptPrefix: providerCfg.SystemPromptPrefix,
	}
}
