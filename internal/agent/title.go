package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/charmbracelet/x/ansi"
)

// ErrNoTitlePrompt reports that a session holds no user prompt or shell
// command to derive a title from.
var ErrNoTitlePrompt = errors.New("session has no prompt to title")

// sessionTitle is a title the title agent produced, together with what
// producing it cost.
type sessionTitle struct {
	text             string
	promptTokens     int64
	completionTokens int64
	cost             float64
}

// generateSessionTitle is the callback a session agent fires on the
// first user prompt of a session. The session agent decides when a
// title is needed; which model and prompt produce it stays here.
//
// There is exactly one attempt. The old small-then-large fallback chain
// paid for a second LLM call to rescue a title, which is not worth it:
// the deferred fallback below already guarantees every session gets a
// name. RegenerateTitle is the manual retry for when that attempt fails.
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

	title, err := c.requestSessionTitle(ctx, sessionID, userPrompt)
	if err != nil {
		slog.Error("Failed to generate session title", "error", err)
		return
	}

	// Atomically update only title and usage fields to avoid overriding other
	// concurrent session updates.
	if err := c.sessions.UpdateTitleAndUsage(ctx, sessionID, title.text, title.promptTokens, title.completionTokens, title.cost); err != nil {
		slog.Error("Failed to save session title and usage", "error", err)
		return
	}
	titleSaved = true
}

// RegenerateTitle names a session again from the prompt that opened it.
//
// NOTE: Unlike generateSessionTitle it reports failure to the caller and
// leaves the current title untouched, so a retry can never replace a
// good title with the placeholder.
func (c *coordinator) RegenerateTitle(ctx context.Context, sessionID string) error {
	msgs, err := c.messages.List(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("list session messages: %w", err)
	}
	prompt := openingPrompt(msgs)
	if prompt == "" {
		return ErrNoTitlePrompt
	}

	title, err := c.requestSessionTitle(ctx, sessionID, prompt)
	if err != nil {
		return err
	}
	if err := c.sessions.UpdateTitleAndUsage(ctx, sessionID, title.text, title.promptTokens, title.completionTokens, title.cost); err != nil {
		return fmt.Errorf("save session title: %w", err)
	}
	return nil
}

// openingPrompt returns the text a session's title is derived from: its
// first real user prompt, or failing that its first shell command in the
// form generateSessionTitle is given. It returns "" when the session has
// neither.
func openingPrompt(msgs []message.Message) string {
	var firstShellCommand string
	for _, msg := range msgs {
		if msg.Role != message.User || msg.IsReminder() {
			continue
		}
		if text := strings.TrimSpace(msg.Content().Text); text != "" {
			return text
		}
		if firstShellCommand == "" {
			if cmds := msg.ShellCommands(); len(cmds) > 0 {
				firstShellCommand = "$ " + cmds[0].Command
			}
		}
	}
	return firstShellCommand
}

// requestSessionTitle asks the title agent to name a conversation that
// opened with userPrompt. It persists nothing.
func (c *coordinator) requestSessionTitle(ctx context.Context, sessionID, userPrompt string) (sessionTitle, error) {
	// Titling runs on behalf of the session, so it inherits that
	// session's model where the two share a model role. An unreadable
	// session degrades to plain config resolution rather than failing
	// the title.
	host := c.instanceFor(ctx, sessionID)
	if host.Agent.ID == "" {
		slog.Warn("Failed to resolve the session's agent for titling; using the configured title model",
			"sessionID", sessionID)
	}
	active, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentTitle, host)
	if err != nil {
		return sessionTitle{}, fmt.Errorf("resolve the title agent: %w", err)
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
		fantasy.WithMaxOutputTokens(titleMaxTokens(active.Agent, model)),
		fantasy.WithUserAgent(userAgent),
	)
	resp, err := agent.Stream(ctx, streamCall)
	if err != nil {
		return sessionTitle{}, fmt.Errorf("generate the title: %w", err)
	}
	if resp.Response.FinishReason == fantasy.FinishReasonLength {
		return sessionTitle{}, errors.New("title generation hit the output token limit")
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

	return sessionTitle{
		text:             title,
		promptTokens:     resp.TotalUsage.InputTokens + resp.TotalUsage.CacheCreationTokens,
		completionTokens: resp.TotalUsage.OutputTokens,
		cost:             cost,
	}, nil
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
