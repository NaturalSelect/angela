package agent

import (
	"context"
	"log/slog"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openrouter"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
)

func openrouterCost(metadata fantasy.ProviderMetadata) *float64 {
	openrouterMetadata, ok := metadata[openrouter.Name]
	if !ok {
		return nil
	}

	opts, ok := openrouterMetadata.(*openrouter.ProviderMetadata)
	if !ok {
		return nil
	}
	return &opts.Usage.Cost
}

// addOpenrouterCosts adds the cost OpenRouter reported for each step to
// total. The result stays nil until some step reports one, so a provider
// without reported costs keeps being priced from the model's rates.
func addOpenrouterCosts(total *float64, steps []fantasy.StepResult) *float64 {
	for _, step := range steps {
		stepCost := openrouterCost(step.ProviderMetadata)
		if stepCost == nil {
			continue
		}
		sum := *stepCost
		if total != nil {
			sum += *total
		}
		total = &sum
	}
	return total
}

// chargeDiscardedSummary bills summarize responses that never became the
// session's summary.
//
// NOTE: Only the cost is written. The token counters hold the session's
// current context size, which these responses have no part in, and a Save
// would write back a whole row that may be stale.
func (a *sessionAgent) chargeDiscardedSummary(ctx context.Context, model Model, sessionID string, usage fantasy.Usage, cost *float64) {
	// A scratch session lets updateSessionUsage keep owning the pricing and
	// telemetry rules instead of repeating them here.
	charge := session.Session{ID: sessionID}
	a.updateSessionUsage(model, &charge, usage, cost, false)
	if charge.Cost == 0 {
		return
	}
	if err := a.sessions.AddCost(ctx, sessionID, charge.Cost); err != nil {
		slog.Error("Failed to record the cost of discarded summaries", "session_id", sessionID, "error", err)
	}
}

func (a *sessionAgent) updateSessionUsage(model Model, session *session.Session, usage fantasy.Usage, overrideCost *float64, estimated bool) {
	if !usageIsZero(usage) {
		session.EstimatedUsage = estimated
	}

	modelConfig := model.CatwalkCfg
	cost := modelConfig.CostPer1MInCached/1e6*float64(usage.CacheCreationTokens) +
		modelConfig.CostPer1MOutCached/1e6*float64(usage.CacheReadTokens) +
		modelConfig.CostPer1MIn/1e6*float64(usage.InputTokens) +
		modelConfig.CostPer1MOut/1e6*float64(usage.OutputTokens)

	if !estimated {
		a.eventTokensUsed(session.ID, model, usage, cost)
	}

	if estimated {
		cost = 0
	} else {
		// Use override cost if available (e.g., from OpenRouter).
		if overrideCost != nil {
			cost = *overrideCost
		}

		// Skip cost accumulation
		if model.FlatRate {
			cost = 0
		}
	}

	session.Cost += cost
	updateSessionTokenCounters(session, usage)
}

func updateSessionTokenCounters(session *session.Session, usage fantasy.Usage) {
	if usage.OutputTokens != 0 {
		session.CompletionTokens = usage.OutputTokens
	}
	if promptTokens := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens; promptTokens != 0 {
		session.PromptTokens = promptTokens
	}
}

func summaryCompletionTokens(usage fantasy.Usage, summaryMessage message.Message) int64 {
	if usage.OutputTokens != 0 {
		return usage.OutputTokens
	}
	return approxTokenCount(summaryMessage.Content().Text) + approxTokenCount(summaryMessage.ReasoningContent().String())
}
