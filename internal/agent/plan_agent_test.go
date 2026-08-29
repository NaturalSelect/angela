package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

func toolNamesFor(t *testing.T, coord *coordinator, agentID string, depth int) []string {
	t.Helper()
	agentCfg, ok := coord.cfg.Config().Agents[agentID]
	require.True(t, ok, "agent %q must be configured", agentID)
	toolList, err := coord.buildTools(agentCfg, "", depth)
	require.NoError(t, err)
	names := make([]string, len(toolList))
	for i, tool := range toolList {
		names[i] = tool.Info().Name
	}
	return names
}

// TestPlanAgentIsReadOnly pins the boundary that makes plan safe to hand a
// user's repository: it can look at anything and change nothing. A plan that
// could edit would stop being a plan.
func TestPlanAgentIsReadOnly(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	names := toolNamesFor(t, coord, config.AgentPlan, 1)

	for _, tool := range []string{"view", "grep", "glob", "ls", "lsp_definition", "lsp_references"} {
		require.Contains(t, names, tool, "plan must be able to read the codebase")
	}
	for _, tool := range []string{"bash", "edit", "multiedit", "write", "download", "lsp_rename", "lsp_replace_symbol", "todos"} {
		require.NotContains(t, names, tool, "plan must not be able to change anything")
	}
}

// TestPlanAgentGetsTheBranchTools covers the forced injection in buildTools:
// merge and the proposal trio arrive past the whitelist, so plan can draft and
// hand back a result without listing them among its allowed tools.
func TestPlanAgentGetsTheBranchTools(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	names := toolNamesFor(t, coord, config.AgentPlan, 1)

	for _, tool := range []string{"merge", "proposal_write", "proposal_edit", "proposal_read"} {
		require.Contains(t, names, tool, "a branch must be able to draft its result and end")
	}
}

// TestQuestionToolReachesBranchesButNotSubagents pins the gate a branch depends
// on. Branches are dispatched like sub-agents, so the plain depth > 0 test used
// to withhold the question tool from them — but a branch talks to the user
// directly, and its preamble tells it to ask. An ordinary sub-agent has nobody
// to ask and must still be refused.
func TestQuestionToolReachesBranchesButNotSubagents(t *testing.T) {
	t.Run("branch is asked to talk to the user, so it gets the tool", func(t *testing.T) {
		coord := newGateTestCoordinator(t, true)
		require.Contains(t, toolNamesFor(t, coord, config.AgentPlan, 1), "question")
	})

	t.Run("ordinary subagent has no user to ask", func(t *testing.T) {
		coord := newGateTestCoordinator(t, true)
		require.NotContains(t, toolNamesFor(t, coord, config.AgentExplore, 1), "question")
	})

	t.Run("non-interactive has no user at all", func(t *testing.T) {
		coord := newGateTestCoordinator(t, false)
		require.NotContains(t, toolNamesFor(t, coord, config.AgentPlan, 1), "question")
	})
}

// TestPlanAgentIsDispatchableAsABranch pins that plan reaches the model as a
// branch. Listed among the ordinary subagents it would look like a call that
// simply never returns, so the Branch flag is what puts it in the section of
// the agent tool description that explains the suspension.
func TestPlanAgentIsDispatchableAsABranch(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	entry, ok := coord.subagents.Get(config.AgentPlan)
	require.True(t, ok, "plan must be dispatchable through the agent tool")
	require.Equal(t, config.AgentModeBranch, entry.cfg.Mode)

	var found bool
	for _, meta := range coord.subagents.Metadata() {
		if meta.ID != config.AgentPlan {
			continue
		}
		found = true
		require.True(t, meta.Branch, "plan must be described to the model as a branch")
		require.NotEmpty(t, meta.Description, "the description is the only dispatch contract plan has")
	}
	require.True(t, found, "plan must appear in the agent tool description")
}
