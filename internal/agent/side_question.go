package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/reminder"
)

// SideQuestion answers a one-off question from a session's existing
// context without joining the turn queue, taking the per-session lock,
// or writing anything to the session's message history. Unlike Run and
// Summarize, it never registers itself in activeRequests, so it runs
// concurrently with a turn already in flight on the same session and
// Cancel cannot stop it.
func (a *sessionAgent) SideQuestion(ctx context.Context, sessionID, question string, resolved resolvedAgent, opts fantasy.ProviderOptions) (string, error) {
	if !resolved.Available() {
		if resolved.Err != nil {
			return "", fmt.Errorf("agent unavailable: %w", resolved.Err)
		}
		return "", errors.New("agent unavailable")
	}

	currentSession, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to get session: %w", err)
	}
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "", errors.New("no conversation context yet")
	}
	aiMsgs, _ := a.preparePrompt(msgs, resolved.Model.CatwalkCfg.SupportsImages)

	systemPromptPrefix := resolved.SystemPromptPrefix
	agentOpts := []fantasy.AgentOption{
		fantasy.WithSystemPrompt(resolved.SystemPrompt),
		fantasy.WithUserAgent(userAgent),
	}
	if resolved.MaxTokens > 0 {
		agentOpts = append(agentOpts, fantasy.WithMaxOutputTokens(resolved.MaxTokens))
	}
	sideAgent := fantasy.NewAgent(resolved.Model.Model, agentOpts...)

	resp, err := sideAgent.Stream(ctx, fantasy.AgentStreamCall{
		Prompt:          sideQuestionPrompt(question),
		Messages:        aiMsgs,
		Headers:         sessionHeaders(sessionID),
		ProviderOptions: opts,
		PrepareStep: func(callCtx context.Context, stepOpts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = stepOpts.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(systemPromptPrefix)}, prepared.Messages...)
			}
			return callCtx, prepared, nil
		},
	})
	if err != nil {
		return "", err
	}

	answer := resp.Response.Content.Text()
	if answer == "" {
		return "", errors.New("side question returned an empty answer")
	}
	return answer, nil
}

// sideQuestionPrompt wraps a side question in a system-reminder block so
// the model treats it as a one-shot, tool-free aside instead of the next
// step in whatever task is already in progress.
func sideQuestionPrompt(question string) string {
	return reminder.Wrap(`The user is asking a side question ("by the way") that stands apart from the task you are working on. Answer it directly and completely in plain text, in a single turn.

Rules for this answer:
- Do not call any tools. Answer only from what you already know and from the conversation above.
- Do not say you will look something up, check a file, or come back with an answer — you have exactly this one turn, with no tool access.
- If the context does not contain what is needed to answer, say so plainly instead of guessing.
- Do not mention being interrupted or paused — your main task, if any, keeps running independently and is unaffected by this question.
- Do not treat this as a new instruction for the main task; it is a separate, standalone question.`) + "\n\n" + question
}

// AskSideQuestion answers a one-off question from a session's existing
// context, concurrently with any turn already running on it, without
// adding the question or its answer to the session's message history.
//
// Executor and identity are resolved together through turnExecutorFor,
// the same resolution a real turn on sessionID would get: a child
// (sub-agent) session is answered by that sub-agent's own executor,
// model, system prompt and delegation depth, not the top-level active
// agent at depth zero. Resolving them separately — the executor routed
// to the child but the identity resolved as if sessionID were
// top-level — was a bug: it answered a /btw asked inside an explore (or
// other subagent) session with the coder's model and prompt.
func (c *coordinator) AskSideQuestion(ctx context.Context, sessionID, question string) (string, error) {
	target, err := c.turnExecutorFor(ctx, sessionID)
	if err != nil {
		return "", err
	}
	executor, resolved := target.executor, target.resolved

	providerCfg, ok := c.cfg.Config().Providers.Get(resolved.Model.ModelCfg.Provider)
	if !ok {
		return "", errModelProviderNotConfigured
	}
	if err := c.refreshTokenIfExpired(ctx, providerCfg); err != nil {
		slog.Error("Failed to refresh OAuth2 token before side question. Proceeding with existing token.", "error", err)
	}
	opts := getProviderOptions(resolved.Model, providerCfg, buildPromptCacheKey(sessionID, "side_question"))

	return executor.SideQuestion(ctx, sessionID, question, resolved, opts)
}
