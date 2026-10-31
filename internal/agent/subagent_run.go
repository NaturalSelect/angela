package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/session"
)

// subAgentParams holds the parameters for running a sub-agent.
type subAgentParams struct {
	Agent          SessionAgent
	SessionID      string
	AgentMessageID string
	ToolCallID     string
	Prompt         string
	SessionTitle   string
	Resolved       resolvedAgent
}

// runSubAgent runs a sub-agent and handles session management and cost accumulation.
// It creates a sub-session, runs the agent with the given prompt, and propagates
// the cost to the parent session.
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) tools.Result {
	sess, err := c.prepareSubAgentSession(ctx, params)
	if err != nil {
		return tools.FailErr("create session", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.trackSubSession(ctx, sess.ID, params, tools.SubSessionKindForeground, cancel)

	outcome := c.executeSubAgent(runCtx, sess, params)
	c.subSessions.end(sess.ID, tools.SubSessionStatusOf(classifyBackgroundOutcome(runCtx, outcome)))
	if outcome.stoppedWhileParentLives(ctx) {
		return tools.Fail(subAgentStoppedMessage)
	}
	return outcome.result()
}

// subAgentStoppedMessage is what a parent model reads when the user stopped
// the sub-agent it was waiting on, so it neither retries nor reports a fault.
const subAgentStoppedMessage = "The user stopped this agent before it finished. Do not dispatch it again unless the user asks."

// subAgentOutcome is how a sub-agent run ended. Failure is the text a
// parent model reads when it did not produce a usable answer, and is empty
// exactly when Text is the answer. Err is the run's own error, kept apart
// so a caller can tell a cancelled run from a failed one.
type subAgentOutcome struct {
	Text    string
	Failure string
	Err     error
}

func (o subAgentOutcome) result() tools.Result {
	if o.Failure != "" {
		return tools.Fail(o.Failure)
	}
	return tools.Ok(o.Text)
}

// stoppedWhileParentLives reports whether the run was cancelled on its own
// while the context of the call that dispatched it is still alive. That is a
// stop aimed at the sub-agent. A cancel of the parent cancels both contexts
// and is not one.
func (o subAgentOutcome) stoppedWhileParentLives(parent context.Context) bool {
	return o.Failure != "" && errors.Is(o.Err, context.Canceled) && parent.Err() == nil
}

// prepareSubAgentSession creates the sub-session a sub-agent runs in and
// wires up everything a later turn in it relies on: its route, its
// recorded owner, and its permission chain.
func (c *coordinator) prepareSubAgentSession(ctx context.Context, params subAgentParams) (session.Session, error) {
	agentToolSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	sess, err := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return session.Session{}, err
	}

	c.registerSubagentRoute(sess.ID, params.Resolved.ID, params.Agent)

	// Record which sub-agent owns this session. Without it the session is
	// only addressable for as long as this process lives, and a later one
	// has no identity to resume it under.
	if err := c.sessions.UpdateActiveAgent(ctx, sess.ID, dispatchedState(params.Resolved.Host)); err != nil {
		slog.Warn(
			"Failed to record the sub-agent on its session",
			"session", sess.ID,
			"agent", params.Resolved.ID,
			"error", err,
		)
	}

	// A sub-agent never gets more than the session that dispatched it
	// already had: no blanket permission bypass, so a gated action it
	// takes is judged exactly as it would be at the top level.
	// RegisterChild only lets that judgment resolve grants and
	// attendedness by walking up to this session, rather than copying
	// them once here and leaving the copy to go stale.
	c.permissions.RegisterChild(sess.ID, params.SessionID)
	return sess, nil
}

// executeSubAgent runs params.Prompt in sess to completion and rolls the
// cost up to the parent. It owns the sub-session's scratch space, so it
// must run exactly once per prepared session.
func (c *coordinator) executeSubAgent(ctx context.Context, sess session.Session, params subAgentParams) subAgentOutcome {
	defer removeWebFetchScratch(c.cfg.Config().Options.DataDirectory, sess.ID)

	model := params.Resolved.Model
	maxTokens := params.Resolved.MaxTokens

	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return subAgentOutcome{Failure: errModelProviderNotConfigured.Error(), Err: errModelProviderNotConfigured}
	}

	// Run the agent
	compact := c.compactFor(ctx, params.SessionID, params.Resolved.Host)
	run := func() (*fantasy.AgentResult, error) {
		return params.Agent.Run(ctx, SessionAgentCall{
			SessionID:                sess.ID,
			Agent:                    params.Resolved,
			Prompt:                   params.Prompt,
			MaxOutputTokens:          maxTokens,
			ProviderOptions:          getProviderOptions(model, providerCfg, buildPromptCacheKey(params.SessionID, params.Resolved.ID)),
			SummarizeProviderOptions: compact.options,
			Compact:                  compact.agent,
			SummarizeOnAuthRefresh:   compact.onAuthRefresh,
			Temperature:              model.CatwalkCfg.Options.Temperature,
			TopP:                     model.CatwalkCfg.Options.TopP,
			TopK:                     model.CatwalkCfg.Options.TopK,
			FrequencyPenalty:         model.CatwalkCfg.Options.FrequencyPenalty,
			PresencePenalty:          model.CatwalkCfg.Options.PresencePenalty,
			NonInteractive:           true,
			OnAuthRefresh:            c.makeAuthRefreshCallback(providerCfg),
		})
	}
	result, err := run()
	if err != nil {
		return subAgentOutcome{Failure: fmt.Sprintf("Failed to generate response: %s", err), Err: err}
	}

	// Update parent session cost on a best-effort basis. A failure here must
	// not discard the sub-agent output that was already produced.
	if err := c.updateParentSessionCost(ctx, sess.ID, params.SessionID); err != nil {
		slog.Warn(
			"Failed to update parent session cost",
			"child_session", sess.ID,
			"parent_session", params.SessionID,
			"error", err,
		)
	}

	output := subAgentOutput(result)
	if OutputTruncated(result) {
		return subAgentOutcome{Failure: subAgentTruncatedNotice(output)}
	}
	if output == "" {
		return subAgentOutcome{Failure: "Sub-agent completed but produced no text output."}
	}
	return subAgentOutcome{Text: output}
}

// OutputTruncated reports whether a run ended because the model hit its
// output token limit, so its text stops short of what the model meant to say.
func OutputTruncated(result *fantasy.AgentResult) bool {
	if result == nil || len(result.Steps) == 0 {
		return false
	}
	// NOTE: Read the last step, not result.Response, which falls back to an
	// earlier step's text when the last step produced none.
	return result.Steps[len(result.Steps)-1].FinishReason == fantasy.FinishReasonLength
}

// subAgentTruncatedNotice is what the parent model reads when a sub-agent
// stopped on the output token limit, with whatever text it did produce.
func subAgentTruncatedNotice(partial string) string {
	const notice = "Sub-agent output was cut off by the output token limit; narrow the task or raise max tokens for its model."
	if partial == "" {
		return notice
	}
	return notice + " Partial output follows.\n\n" + partial
}

// subAgentOutput extracts the final text answer from a sub-agent run.
func subAgentOutput(result *fantasy.AgentResult) string {
	if result == nil {
		return ""
	}
	return result.Response.Content.Text()
}
