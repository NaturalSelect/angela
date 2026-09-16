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

// maxCommitDiffChars caps the staged diff text fed to the commit
// message model. The model only needs enough of the change to
// describe it, and an unbounded diff on a large staged changeset
// would blow well past context limits.
const maxCommitDiffChars = 12000

// GenerateCommitMessage writes a commit message describing diff (the
// output of `git diff --cached`), using a tool-free internal agent.
// It never reads or writes sessionID's message history — sessionID
// only lets the internal agent inherit that session's model where
// the two share a slot, matching generateSessionTitle. The
// attribution trailer always credits sessionID's own agent, since
// that is what actually wrote the staged changes — not the cheap
// internal agent drafting the message text.
func (c *coordinator) GenerateCommitMessage(ctx context.Context, sessionID, diff string) (string, error) {
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return "", errors.New("no staged changes to describe")
	}
	if len(diff) > maxCommitDiffChars {
		diff = diff[:maxCommitDiffChars] + "\n… (diff truncated)"
	}

	host, err := c.activeAgentFor(ctx, sessionID)
	if err != nil {
		slog.Warn("Failed to resolve the session's agent for commit message generation; using the configured commit model",
			"error", err, "sessionID", sessionID)
	}
	active, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentCommit, host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the commit agent: %w", err)
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
		Prompt:  fmt.Sprintf("Write a commit message for the following staged changes (git diff --cached):\n\n%s", diff),
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

	message := strings.TrimSpace(resp.Response.Content.Text())
	message = strings.Trim(message, "`")
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("commit message generation returned an empty message")
	}

	attribution := c.cfg.Config().Options.Attribution
	if attribution == nil {
		attribution = &config.Attribution{}
	}
	// The trailer credits host, the agent that actually wrote the
	// staged changes, not model, which only drafted the message text.
	// Fall back to model when host failed to resolve above or names
	// no model of its own.
	attributionModel := modelDisplayName(model)
	if host.Model.Provider != "" && host.Model.Model != "" {
		attributionModel = c.selectedModelDisplayName(host.Model)
	}
	message += attribution.CommitTrailer(attributionModel, "")
	return message, nil
}
