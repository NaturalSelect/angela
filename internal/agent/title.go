package agent

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/charmbracelet/x/ansi"
)

// generateSessionTitle is the callback a session agent fires on the
// first user prompt of a session. The session agent decides when a
// title is needed; which model and prompt produce it stays here.
//
// There is exactly one attempt. The old small-then-large fallback chain
// paid for a second LLM call to rescue a title, which is not worth it:
// the deferred fallback below already guarantees every session gets a
// name.
func (c *coordinator) generateSessionTitle(ctx context.Context, sessionID, userPrompt string) {
	if userPrompt == "" {
		return
	}

	// Ensure the session always gets a title even if every path below
	// fails or the context is cancelled before we finish.
	var titleSaved bool
	defer func() {
		if !titleSaved {
			fallbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := c.sessions.Rename(fallbackCtx, sessionID, DefaultSessionName); err != nil {
				slog.Error("Failed to save fallback session title", "error", err)
			}
		}
	}()

	agentCfg, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentTitle)
	if err != nil {
		slog.Error("Failed to resolve the title agent", "error", err)
		return
	}
	providerCfg, _ := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	systemPromptPrefix := providerCfg.SystemPromptPrefix

	streamCall := fantasy.AgentStreamCall{
		Prompt:  fmt.Sprintf("Generate a concise title for the following content:\n\n%s\n <think>\n\n</think>", userPrompt),
		Headers: sessionHeaders(sessionID),
		PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = opts.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{
					fantasy.NewSystemMessage(systemPromptPrefix),
				}, prepared.Messages...)
			}
			return callCtx, prepared, nil
		},
	}

	agent := fantasy.NewAgent(
		model.Model,
		fantasy.WithSystemPrompt(systemPrompt+"\n /no_think"),
		fantasy.WithMaxOutputTokens(titleMaxTokens(agentCfg, model)),
		fantasy.WithUserAgent(userAgent),
	)
	resp, err := agent.Stream(ctx, streamCall)
	if err != nil {
		slog.Error("Error generating title", "error", err)
		return
	}
	if resp.Response.FinishReason == fantasy.FinishReasonLength {
		slog.Error("Title generation hit the output token limit")
		return
	}

	title := strings.ReplaceAll(resp.Response.Content.Text(), "\n", " ")

	// Remove thinking tags if present.
	title = thinkTagRegex.ReplaceAllString(title, "")
	title = orphanThinkTagRegex.ReplaceAllString(title, "")

	title = strings.TrimSpace(title)
	if title == "" {
		// LLM returned empty content. Use the prompt itself as a
		// fallback title, truncated to 50 chars, before resorting to
		// the generic default.
		fallback := strings.ReplaceAll(userPrompt, "\n", " ")
		fallback = strings.TrimSpace(fallback)
		if len(fallback) > 50 {
			fallback = ansi.Truncate(fallback, 50, "…")
		}
		title = cmp.Or(fallback, DefaultSessionName)
	}

	// Calculate usage and cost.
	var costOverride *float64
	for _, step := range resp.Steps {
		stepCost := openrouterCost(step.ProviderMetadata)
		if stepCost != nil {
			newCost := *stepCost
			if costOverride != nil {
				newCost += *costOverride
			}
			costOverride = &newCost
		}
		extractHyperCredits(step.ProviderMetadata)
	}

	modelConfig := model.CatwalkCfg
	cost := modelConfig.CostPer1MInCached/1e6*float64(resp.TotalUsage.CacheCreationTokens) +
		modelConfig.CostPer1MOutCached/1e6*float64(resp.TotalUsage.CacheReadTokens) +
		modelConfig.CostPer1MIn/1e6*float64(resp.TotalUsage.InputTokens) +
		modelConfig.CostPer1MOut/1e6*float64(resp.TotalUsage.OutputTokens)

	// Use override cost if available (e.g., from OpenRouter).
	if costOverride != nil {
		cost = *costOverride
	}

	// Skip cost accumulation
	if model.FlatRate {
		cost = 0
	}

	promptTokens := resp.TotalUsage.InputTokens + resp.TotalUsage.CacheCreationTokens
	completionTokens := resp.TotalUsage.OutputTokens

	// Atomically update only title and usage fields to avoid overriding other
	// concurrent session updates.
	saveErr := c.sessions.UpdateTitleAndUsage(ctx, sessionID, title, promptTokens, completionTokens, cost)
	if saveErr != nil {
		slog.Error("Failed to save session title and usage", "error", saveErr)
		return
	}
	titleSaved = true
}

// titleMaxTokens honours the agent's configured cap, falling back to the
// model's own default. A reasoning model needs room for its thinking
// budget on top of the handful of tokens a title costs, so a cap that
// small would make it hit FinishReasonLength every time.
func titleMaxTokens(agentCfg config.Agent, model Model) int64 {
	if model.CatwalkCfg.CanReason {
		return model.CatwalkCfg.DefaultMaxTokens
	}
	if agentCfg.MaxTokens != nil && *agentCfg.MaxTokens > 0 {
		return *agentCfg.MaxTokens
	}
	return model.CatwalkCfg.DefaultMaxTokens
}
