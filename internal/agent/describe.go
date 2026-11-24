package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
)

// stripCodeFence removes a Markdown code fence that wraps the whole of
// s, which models sometimes add despite being told not to. Backticks
// inside the text are left alone, since a description can legitimately
// start or end with inline code.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	_, body, ok := strings.Cut(s, "\n")
	if !ok {
		return s
	}
	body = strings.TrimSuffix(body, "```")
	return strings.TrimSpace(body)
}

// GenerateChangeDescription writes a description of what head changed
// relative to base, using a tool-free internal agent. changes is what
// the caller gathered about the comparison: the commit subjects, the
// diffstat and the diff of `git diff base...head`, in that order so a
// cut at the rune budget from ConfigStore.CommitDiffRunes drops the
// tail of the diff and keeps the overview. It never reads or writes
// sessionID's message history; sessionID only lets the internal agent
// inherit that session's model where the two share a slot.
func (c *coordinator) GenerateChangeDescription(ctx context.Context, sessionID, base, head, changes string) (string, error) {
	changes = strings.TrimSpace(changes)
	if changes == "" {
		return "", errors.New("no changes to describe")
	}
	changes = truncateRunes(changes, c.cfg.CommitDiffRunes())

	host := c.instanceFor(ctx, sessionID)
	if host.Agent.ID == "" {
		slog.Warn("Failed to resolve the session's agent for change description; using the configured describe model",
			"sessionID", sessionID)
	}
	active, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentDescribe, host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the describe agent: %w", err)
	}
	providerCfg, _ := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	systemPromptPrefix := providerCfg.SystemPromptPrefix

	agent := fantasy.NewAgent(
		model.Model,
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithMaxOutputTokens(titleMaxTokens(active.Agent, model)),
		fantasy.WithUserAgent(userAgent),
	)
	resp, err := agent.Stream(ctx, fantasy.AgentStreamCall{
		Prompt:  fmt.Sprintf("Write a description of the changes on %q relative to %q.\n\n%s", head, base, changes),
		Headers: sessionHeaders(sessionID),
		PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = opts.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(systemPromptPrefix)}, prepared.Messages...)
			}
			return callCtx, prepared, nil
		},
	})
	if err != nil {
		return "", err
	}

	description := stripCodeFence(resp.Response.Content.Text())
	if description == "" {
		return "", errors.New("change description generation returned an empty description")
	}
	return description, nil
}
