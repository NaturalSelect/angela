package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/pubsub"
)

// runBranchAgent forks the caller's conversation into a session the user
// drives themselves, and suspends this tool call until they resolve it.
//
// It differs from runSubAgent in what it does not do. It does not run
// non-interactively, so it never marks its session unattended: a branch
// acts on the user's behalf in front of the user, and its prompts belong
// to them. And it does not read a result off the agent run at all: the
// first turn merely starts the conversation, and what crosses back is
// whatever the user eventually approves through the merge tool —
// possibly many turns later.
func (c *coordinator) runBranchAgent(ctx context.Context, params subAgentParams) tools.Result {
	branchSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	session, err := c.sessions.CreateTaskSession(ctx, branchSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return tools.FailErr("create branch session", err)
	}
	c.trackSubSession(ctx, session.ID, params, tools.SubSessionKindBranch, nil)
	branchStatus := tools.SubSessionFailed
	defer func() { c.subSessions.end(session.ID, branchStatus) }()
	defer removeWebFetchScratch(c.cfg.Config().Options.DataDirectory, session.ID)

	c.registerSubagentRoute(session.ID, params.Resolved.ID, params.Agent)
	c.permissions.RegisterChild(session.ID, params.SessionID)

	if err := c.sessions.UpdateActiveAgent(ctx, session.ID, dispatchedState(params.Resolved.Host)); err != nil {
		slog.Warn(
			"Failed to record the branch agent on its session",
			"session", session.ID,
			"agent", params.Resolved.ID,
			"error", err,
		)
	}

	parentSession, err := c.sessions.Get(ctx, params.SessionID)
	if err != nil {
		return tools.FailErr("look up parent session", err)
	}

	// Everything the caller had said and seen up to the call that forked
	// it. The forking message itself is excluded: the branch is told what
	// to do by the prompt below, so copying a tool call nobody will answer
	// would only leave a dangling call in its history.
	//
	// When the parent was compacted, its own turns never see anything
	// before its summary either — copying further back would hand the
	// branch exactly the history compaction was meant to discard.
	summaryID, err := c.messages.ForkSession(ctx, params.SessionID, session.ID, parentSession.SummaryMessageID, params.AgentMessageID)
	if err != nil {
		return tools.FailErr("fork conversation into branch", err)
	}
	copied, err := c.messages.List(ctx, session.ID)
	if err != nil {
		return tools.FailErr("read forked conversation", err)
	}
	if len(copied) > 0 {
		session.BaseMessageID = copied[len(copied)-1].ID
	}
	if summaryID != "" || session.BaseMessageID != "" {
		session.SummaryMessageID = summaryID
		if session, err = c.sessions.Save(ctx, session); err != nil {
			return tools.FailErr("record branch starting point", err)
		}
	}

	forkPrompt, err := renderBranchForkPrompt(branchForkPrompt{
		ParentTitle: parentSession.Title,
		Prompt:      params.Prompt,
	})
	if err != nil {
		return tools.FailErr("render branch prompt", err)
	}

	// Registered before the first turn, not after: that turn may call
	// merge, and a merge arriving before anyone is listening must still
	// land.
	done := c.branches.Register(session.ID, params.SessionID)
	defer c.branches.Forget(session.ID)
	defer c.proposals.Discard(session.ID)
	defer c.endBranchBackgroundAgents(session.ID)

	if c.notify != nil {
		c.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			Type:         notify.TypeBranchForked,
			SessionID:    session.ID,
			SessionTitle: session.Title,
		})
	}

	if err := c.startBranchTurn(ctx, session.ID, forkPrompt, params); err != nil && !errors.Is(err, context.Canceled) {
		// A branch only ever ends three ways: a successful merge, an
		// explicit AbandonBranch, or its parent's own cancel, and the
		// opening turn is no exception. Whatever it failed with here — a
		// dropped connection, a retry fantasy gave up on, any other error
		// that is not a cancellation — is none of those three, so it
		// says nothing about whether the branch is worth keeping, and
		// that call belongs to the user alone. Logging is all this does;
		// the branch is left alive and idle, with the failure already
		// visible as its own error banner in the branch's history, for
		// the user to retry from or give up on outright with
		// AbandonBranch.
		slog.Error("Branch first turn failed", "session", session.ID, "error", err)
	}

	select {
	case <-ctx.Done():
		// The third way a branch can end: the parent's own cancel shares
		// this context, so it resolves the wait right here without ever
		// touching Signal. The Result returned here is discarded either
		// way: this method's caller goes through tools.NewParallelTool,
		// whose adapter reads ctx.Err() itself once Run returns and
		// reports cancellation as a Go error instead of a tool result.
		branchStatus = tools.SubSessionStopped
		return tools.Fail("cancelled")
	case out := <-done:
		if out.Merged {
			branchStatus = tools.SubSessionCompleted
			return tools.Ok(out.Payload)
		}
		branchStatus = tools.SubSessionStopped
		return tools.Halt(out.Payload)
	}
}

// startBranchTurn runs the branch's opening turn, the one seeded with the
// fork prompt. Every turn after it is driven by the user through the normal
// session path.
func (c *coordinator) startBranchTurn(ctx context.Context, sessionID, prompt string, params subAgentParams) error {
	model := params.Resolved.Model
	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return errModelProviderNotConfigured
	}

	compact := c.compactFor(ctx, params.SessionID, params.Resolved.Host)
	run := func() (*fantasy.AgentResult, error) {
		return params.Agent.Run(ctx, SessionAgentCall{
			SessionID:                sessionID,
			Agent:                    params.Resolved,
			Prompt:                   prompt,
			MaxOutputTokens:          params.Resolved.MaxTokens,
			ProviderOptions:          getProviderOptions(model, providerCfg, buildPromptCacheKey(sessionID, params.Resolved.ID)),
			SummarizeProviderOptions: compact.options,
			Compact:                  compact.agent,
			SummarizeOnAuthRefresh:   compact.onAuthRefresh,
			Temperature:              model.CatwalkCfg.Options.Temperature,
			TopP:                     model.CatwalkCfg.Options.TopP,
			TopK:                     model.CatwalkCfg.Options.TopK,
			FrequencyPenalty:         model.CatwalkCfg.Options.FrequencyPenalty,
			PresencePenalty:          model.CatwalkCfg.Options.PresencePenalty,
			OnAuthRefresh:            c.makeAuthRefreshCallback(providerCfg),
		})
	}
	// NOTE: This turn bypasses the session path that rolls cost up, so
	// nothing else would bill it to the parent. Only the parent is billed:
	// when it is a sub-agent, its own finish rolls its whole total up to
	// the next level, this turn included.
	_, err := c.billedTurn(ctx, sessionID, run, func(delta float64) error {
		return c.sessions.AddCost(ctx, params.SessionID, delta)
	})()
	return err
}

// NOTE: branchForkRefusal is the one branch gate: it reports why caller, at
// the given dispatch depth, may not fork a branch, or "" when it may. The
// agent tool's description filter and the call-time refusal both read it, so
// the two cannot disagree. The chain it allows is primary -> branch* ->
// subagent*.
//
// A subagent cannot fork because a branch hands the conversation to the user,
// and a subagent's turn usually runs where a forked branch would go unnoticed.
// The test is Mode == branch rather than Mode != subagent, since an agent
// with no Mode is dispatched as a subagent too. Being suspended on a branch
// already is not a refusal: several outstanding branches let the user hold
// alternative directions side by side. Every refusal names the alternative so
// the model can act on it. Budget is not checked here: dispatchDepth counts a
// branch hop like any other, and buildTools already withholds the agent tool
// once options.subagent_depth is used up. backgroundAgentsAvailable reads
// this same gate, so a caller that can fork a branch can also background.
func (c *coordinator) branchForkRefusal(caller config.Agent, depth int) string {
	if !c.interactive {
		return "A branch hands the conversation to the user, so it needs an interactive session. " +
			"Dispatch a regular subagent instead."
	}
	if c.noBranchAgents {
		return "This front end cannot hand the conversation to a branch. " +
			"Dispatch a regular subagent instead."
	}
	if depth > 0 && caller.Mode != config.AgentModeBranch {
		return "Only the top-level conversation or another branch can fork a branch; a subagent can only dispatch subagents. " +
			"Dispatch a regular subagent instead."
	}
	return ""
}

// updateParentSessionCost accumulates the cost from a child session to its
// parent session. The add is atomic because sibling sub-runs under one
// parent finish concurrently.
func (c *coordinator) updateParentSessionCost(ctx context.Context, childSessionID, parentSessionID string) error {
	childSession, err := c.sessions.Get(ctx, childSessionID)
	if err != nil {
		return fmt.Errorf("get child session: %w", err)
	}

	if err := c.sessions.AddCost(ctx, parentSessionID, childSession.Cost); err != nil {
		return fmt.Errorf("add cost to parent session: %w", err)
	}

	return nil
}
