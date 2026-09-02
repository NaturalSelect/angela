package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed merge.md
var mergeDescription string

// mergeParams is empty on purpose. What crosses back is the proposal
// the branch has been drafting, so there is nothing left for the call
// itself to carry.
type mergeParams struct{}

// mergeTool ends a branch and hands its proposal back to the suspended
// conversation that forked it.
//
// It lives here rather than in package tools because it needs the
// coordinator's branch controller, and tools cannot import agent.
//
// It plans rather than simply running because the user approves the
// finished proposal, and the proposal is in the store rather than in
// the call — PreviewOf is handed the raw arguments alone and cannot
// reach it.
//
// Not a parallel tool: it resolves the branch, so running two at once
// would race to decide which proposal crosses back.
type mergeTool struct {
	fantasy.AgentTool

	c *coordinator
}

func (c *coordinator) mergeTool() fantasy.AgentTool {
	t := &mergeTool{c: c}
	t.AgentTool = fantasy.NewAgentTool(toolnames.Merge, mergeDescription, t.run)
	return t
}

func (t *mergeTool) run(ctx context.Context, params mergeParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	plan, err := t.plan(ctx)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if plan.Response != nil {
		return *plan.Response, nil
	}
	return plan.Apply(ctx)
}

func (t *mergeTool) Plan(ctx context.Context, call fantasy.ToolCall) (tools.Plan, error) {
	return t.plan(ctx)
}

func (t *mergeTool) plan(ctx context.Context) (tools.Plan, error) {
	sessionID := tools.GetSessionFromContext(ctx)
	if sessionID == "" {
		return tools.Plan{}, errors.New("session id missing from context")
	}

	doc, ok := t.c.proposals.Get(sessionID)
	if !ok || doc == "" {
		// Settled before the gate: an empty proposal is the model's
		// mistake to fix, not a decision to put to the user, and
		// leaving the branch unresolved lets it draft and try again.
		resp := fantasy.NewTextErrorResponse(
			"There is no proposal to merge. Draft it with " + toolnames.ProposalWrite + " first.",
		)
		return tools.Plan{Response: &resp}, nil
	}

	return tools.Plan{
		Preview: permission.Preview{
			Description: "Merge this branch and hand back its proposal:",
			Params: tools.MergePermissionsParams{
				Name: tools.ProposalDocumentName,
				// The whole proposal reads as an addition. The user is
				// approving this document, not the revisions made to it
				// since a merge they already declined.
				OldContent: "",
				NewContent: doc,
			},
		},
		Apply: func(ctx context.Context) (fantasy.ToolResponse, error) {
			return t.apply(sessionID, doc)
		},
	}, nil
}

func (t *mergeTool) apply(sessionID, doc string) (fantasy.ToolResponse, error) {
	// False means the rendezvous is already resolved — the user
	// abandoned the branch while this call was being approved.
	// Reporting it as a tool error rather than an error keeps the
	// branch usable instead of tearing down its turn.
	if !t.c.branches.Signal(sessionID, branchOutcome{Merged: true, Payload: doc}) {
		return fantasy.NewTextErrorResponse(
			"No conversation is waiting on this branch any more, so there is nothing to merge into.",
		), nil
	}

	// The full proposal goes into this branch's own result, not just the
	// parent's: the proposal store is discarded once the branch ends, so
	// this is the only place it survives for the user to reopen later.
	resp := fantasy.NewTextResponse(fmt.Sprintf(
		"Merged. The conversation this branch was forked from has resumed.\n\n## %s\n\n%s",
		tools.ProposalDocumentName, doc,
	))
	resp.StopTurn = true
	return resp, nil
}
