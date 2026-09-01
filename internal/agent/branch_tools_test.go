package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// mergeCall carries no arguments: what crosses back is the proposal in
// the store, not anything the model puts in the call.
func mergeCall() fantasy.ToolCall {
	return fantasy.ToolCall{ID: "call-1", Name: toolnames.Merge, Input: "{}"}
}

// mergeCoordinator is the smallest coordinator the merge tool needs: it
// reaches nothing but the branch controller and the proposal store.
func mergeCoordinator() *coordinator {
	return &coordinator{
		branches:  newBranchController(),
		proposals: tools.NewProposalStore(),
	}
}

func TestMergeToolResolvesTheBranch(t *testing.T) {
	t.Parallel()

	c := mergeCoordinator()
	done := c.branches.Register("s1", "parent-1")
	c.proposals.Set("s1", "found the leak")

	resp, err := c.mergeTool().Run(sessionCtx(t.Context()), mergeCall())
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.True(t, resp.StopTurn,
		"a merged branch must not keep taking turns after it has ended")

	out := <-done
	require.True(t, out.Merged)
	require.Equal(t, "found the leak", out.Payload)
}

// An empty proposal is the model's mistake to fix, not a decision to put
// to the user: the call settles before the gate, and the branch stays
// alive so it can draft and try again.
func TestMergeToolRejectsAnEmptyProposal(t *testing.T) {
	t.Parallel()

	c := mergeCoordinator()
	done := c.branches.Register("s1", "parent-1")

	resp, err := c.mergeTool().Run(sessionCtx(t.Context()), mergeCall())
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Empty(t, done, "an empty proposal must not resolve the branch")
	require.True(t, c.branches.Waiting("s1"))
}

// TestMergeToolPlans pins that merge reaches the gate as a planner. The
// decorator picks the plan path by type assertion, so a merge that
// stopped satisfying the interface would ask the user to approve a
// proposal it never showed them.
func TestMergeToolPlans(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*tools.Planner)(nil), mergeCoordinator().mergeTool())
}

// The approval prompt has to carry the whole proposal: the user is
// deciding on this document, and PreviewOf cannot reach the store to
// build it from the call alone.
func TestMergePreviewCarriesTheProposal(t *testing.T) {
	t.Parallel()

	c := mergeCoordinator()
	c.branches.Register("s1", "parent-1")
	c.proposals.Set("s1", "# Plan\n\nStep one.")

	planner, ok := c.mergeTool().(tools.Planner)
	require.True(t, ok)

	plan, err := planner.Plan(sessionCtx(t.Context()), mergeCall())
	require.NoError(t, err)
	require.Nil(t, plan.Response)

	params, ok := plan.Preview.Params.(tools.MergePermissionsParams)
	require.True(t, ok, "the dialog renders a diff off this type")
	require.Equal(t, "# Plan\n\nStep one.", params.NewContent)
	require.Empty(t, params.OldContent,
		"the user approves the whole proposal, not the changes since a merge they declined")
	require.Equal(t, tools.ProposalDocumentName, params.Name)
}

// The user can abandon a branch while a merge sits at the approval
// prompt. The tool then has nothing to resolve, and must say so rather
// than fail the turn.
func TestMergeToolWithNoWaiter(t *testing.T) {
	t.Parallel()

	c := mergeCoordinator()
	c.proposals.Set("s1", "x")

	resp, err := c.mergeTool().Run(sessionCtx(t.Context()), mergeCall())
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "waiting on this branch")
}

// TestMergeToolDeniedNeverRuns is the load-bearing assertion behind
// "a rejected merge does not end the branch": approval happens in the
// decorator, before Apply, so a refusal cannot reach Signal. The branch
// stays alive and the parent stays suspended, which is what lets the
// user ask for a different proposal and merge again.
func TestMergeToolDeniedNeverRuns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)

	c := mergeCoordinator()
	done := c.branches.Register("s1", "parent-1")
	c.proposals.Set("s1", "found the leak")
	gated := newPermissionedTool(c.mergeTool(), svc, dir)

	events := svc.Subscribe(t.Context())
	result := make(chan fantasy.ToolResponse, 1)
	go func() {
		resp, err := gated.Run(sessionCtx(t.Context()), mergeCall())
		require.NoError(t, err)
		result <- resp
	}()

	svc.Deny((<-events).Payload)
	resp := <-result

	require.True(t, resp.IsError)
	require.Empty(t, done, "a refused merge must not reach the parent")
	require.True(t, c.branches.Waiting("s1"),
		"the branch must stay alive so the user can have it try again")

	doc, ok := c.proposals.Get("s1")
	require.True(t, ok)
	require.Equal(t, "found the leak", doc,
		"the refused proposal must survive so the model can revise it rather than retype it")
}

// And the retry really is possible: the refusal is not remembered, so a
// second merge prompts again rather than being short-circuited. The
// revision goes through proposal_edit, which is the whole point — the
// model sends the passage that changed, not the document again.
func TestMergeToolRetriesAfterDenial(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)

	c := mergeCoordinator()
	done := c.branches.Register("s1", "parent-1")
	c.proposals.Set("s1", "ship the first try")
	gated := newPermissionedTool(c.mergeTool(), svc, dir)
	events := svc.Subscribe(t.Context())

	run := func(approve bool) fantasy.ToolResponse {
		result := make(chan fantasy.ToolResponse, 1)
		go func() {
			resp, err := gated.Run(sessionCtx(t.Context()), mergeCall())
			require.NoError(t, err)
			result <- resp
		}()
		ev := <-events
		if approve {
			svc.Grant(ev.Payload)
		} else {
			svc.Deny(ev.Payload)
		}
		return <-result
	}

	require.True(t, run(false).IsError)

	edited, err := tools.NewProposalEditTool(c.proposals).Run(sessionCtx(t.Context()), fantasy.ToolCall{
		ID:    "call-edit",
		Name:  toolnames.ProposalEdit,
		Input: `{"old_string":"first","new_string":"second"}`,
	})
	require.NoError(t, err)
	require.False(t, edited.IsError)

	require.False(t, run(true).IsError)

	out := <-done
	require.Equal(t, "ship the second try", out.Payload,
		"the approved proposal must be the one that crosses back")
}

// Drafting must not stop for approval. The user's decision is whether
// the finished proposal crosses back, and putting a prompt in front of
// every revision would ask them about a document that reaches nothing —
// and would stall a branch running where nobody can answer.
func TestProposalToolsNeverPrompt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	store := tools.NewProposalStore()
	events := svc.Subscribe(t.Context())

	write := newPermissionedTool(tools.NewProposalWriteTool(store), svc, dir)
	resp, err := write.Run(sessionCtx(t.Context()), fantasy.ToolCall{
		ID: "call-w", Name: toolnames.ProposalWrite,
		Input: `{"content":"first draft"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	edit := newPermissionedTool(tools.NewProposalEditTool(store), svc, dir)
	resp, err = edit.Run(sessionCtx(t.Context()), fantasy.ToolCall{
		ID: "call-e", Name: toolnames.ProposalEdit,
		Input: `{"old_string":"first","new_string":"second"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	read := newPermissionedTool(tools.NewProposalReadTool(store), svc, dir)
	resp, err = read.Run(sessionCtx(t.Context()), fantasy.ToolCall{
		ID: "call-r", Name: toolnames.ProposalRead, Input: `{}`,
	})
	require.NoError(t, err)
	require.Equal(t, "second draft", resp.Content)

	require.Empty(t, events, "drafting a proposal must not raise an approval prompt")
}

// TestBuildToolsGivesBranchesMerge pins both halves of the wiring: only a
// branch gets these tools, and a branch gets them even when the user's
// agent config narrows its tools — a branch that could draft but not
// merge, or merge but not draft, would strand the conversation it forked
// from just as surely as one with no merge at all.
func TestBuildToolsGivesBranchesMerge(t *testing.T) {
	t.Parallel()

	branchOnly := []string{
		toolnames.Merge,
		toolnames.ProposalWrite,
		toolnames.ProposalEdit,
		toolnames.ProposalRead,
	}

	built := func(t *testing.T, agent config.Agent) map[string]bool {
		t.Helper()
		env := testEnv(t)
		c := newTestCoordinator(t, env, "test", config.ProviderConfig{})

		tooling, err := c.buildTools(agent, "test-model", 0)
		require.NoError(t, err)

		names := map[string]bool{}
		for _, tool := range tooling {
			names[tool.Info().Name] = true
		}
		return names
	}

	requireAll := func(t *testing.T, names map[string]bool, want bool) {
		t.Helper()
		for _, name := range branchOnly {
			require.Equal(t, want, names[name], "tool %q", name)
		}
	}

	narrow := &config.AllowedToolSet{Kind: config.ToolSetScope, Tools: []string{toolnames.View}}

	t.Run("branch gets them", func(t *testing.T) {
		t.Parallel()

		requireAll(t, built(t, config.Agent{
			ID: "pairing", Mode: config.AgentModeBranch,
			AllowedTools: &config.AllowedToolSet{Kind: config.ToolSetInherited},
			AllowedMCP:   &config.AllowedMCPSet{Kind: config.ToolSetInherited},
		}), true)
	})

	t.Run("a narrowed branch still gets them", func(t *testing.T) {
		t.Parallel()

		requireAll(t, built(t, config.Agent{
			ID: "pairing", Mode: config.AgentModeBranch,
			AllowedTools: narrow,
			AllowedMCP:   &config.AllowedMCPSet{Kind: config.ToolSetScope},
		}), true)
	})

	t.Run("a subagent does not", func(t *testing.T) {
		t.Parallel()

		requireAll(t, built(t, config.Agent{
			ID: "general", Mode: config.AgentModeSubagent,
			AllowedTools: &config.AllowedToolSet{Kind: config.ToolSetInherited},
			AllowedMCP:   &config.AllowedMCPSet{Kind: config.ToolSetInherited},
		}), false)
	})

	t.Run("a primary does not", func(t *testing.T) {
		t.Parallel()

		requireAll(t, built(t, config.Agent{
			ID: "coder", Mode: config.AgentModePrimary,
			AllowedTools: &config.AllowedToolSet{Kind: config.ToolSetInherited},
			AllowedMCP:   &config.AllowedMCPSet{Kind: config.ToolSetInherited},
		}), false)
	})
}

// No tool may let the model end a branch on its own: that decision is
// the user's, and a model that could abandon would strand the parent
// with an answer nobody approved.
func TestBranchHasNoSelfTerminationTool(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	c := newTestCoordinator(t, env, "test", config.ProviderConfig{})

	built, err := c.buildTools(config.Agent{
		ID: "pairing", Mode: config.AgentModeBranch,
		AllowedTools: &config.AllowedToolSet{Kind: config.ToolSetInherited},
		AllowedMCP:   &config.AllowedMCPSet{Kind: config.ToolSetInherited},
	}, "test-model", 0)
	require.NoError(t, err)

	for _, tool := range built {
		require.NotContains(t, tool.Info().Name, "abort",
			"a branch must not be able to end itself")
	}
}
