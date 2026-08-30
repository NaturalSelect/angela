package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
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

// builtinBranches are the branch agents Angela ships. Everything in this file
// that is not about one of them specifically applies to both.
var builtinBranches = []string{config.AgentPlan, config.AgentDeepResearch}

// TestBranchAgentsNeverWrite pins the boundary that makes a branch safe to
// approve on merge: both of them investigate and hand back a document, so
// neither may touch the working tree. It is also why their merge summary can
// be trusted — nothing changed while they ran.
func TestBranchAgentsNeverWrite(t *testing.T) {
	for _, id := range builtinBranches {
		t.Run(id, func(t *testing.T) {
			coord := newGateTestCoordinator(t, true)
			names := toolNamesFor(t, coord, id, 1)

			for _, tool := range []string{toolnames.View, toolnames.Grep, toolnames.Glob, toolnames.LS, toolnames.LSPDefinition, toolnames.LSPReferences} {
				require.Contains(t, names, tool, "a branch must be able to read the codebase")
			}
			for _, tool := range []string{toolnames.Edit, toolnames.MultiEdit, toolnames.Write, toolnames.Download, toolnames.LSPRename, toolnames.LSPReplaceSymbol, toolnames.Todos} {
				require.NotContains(t, names, tool, "a branch must not be able to change anything")
			}
		})
	}
}

// TestBranchAgentsGetTheBranchTools covers the forced injection in buildTools:
// merge and the proposal trio arrive past the whitelist, so a branch can draft
// its result and end without listing them among its allowed tools.
func TestBranchAgentsGetTheBranchTools(t *testing.T) {
	for _, id := range builtinBranches {
		t.Run(id, func(t *testing.T) {
			coord := newGateTestCoordinator(t, true)
			names := toolNamesFor(t, coord, id, 1)

			for _, tool := range []string{toolnames.Merge, toolnames.ProposalWrite, toolnames.ProposalEdit, toolnames.ProposalRead} {
				require.Contains(t, names, tool, "a branch must be able to draft its result and end")
			}
		})
	}
}

// TestBranchAgentsAreDispatchableAsBranches pins that both reach the model as
// branches. Listed among the ordinary subagents they would look like calls
// that simply never return, so the Branch flag is what puts them in the
// section of the agent tool description that explains the suspension.
func TestBranchAgentsAreDispatchableAsBranches(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	for _, id := range builtinBranches {
		t.Run(id, func(t *testing.T) {
			entry, ok := coord.subagents.Get(id)
			require.True(t, ok, "%s must be dispatchable through the agent tool", id)
			require.Equal(t, config.AgentModeBranch, entry.cfg.Mode)

			var found bool
			for _, meta := range coord.subagents.Metadata() {
				if meta.ID != id {
					continue
				}
				found = true
				require.True(t, meta.Branch, "%s must be described to the model as a branch", id)
				require.NotEmpty(t, meta.Description, "the description is the only dispatch contract it has")
			}
			require.True(t, found, "%s must appear in the agent tool description", id)
		})
	}
}

// TestBranchExecutionBoundaryDiffers pins the one place the two branches part
// company. A plan is a claim about what should be done and can be reasoned
// out from reading; a root cause is a claim about what is already true, and
// settling it needs a reproduction or a git history.
func TestBranchExecutionBoundaryDiffers(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	require.NotContains(t, toolNamesFor(t, coord, config.AgentPlan, 1), toolnames.Bash,
		"plan reasons from what it reads")

	deepResearch := toolNamesFor(t, coord, config.AgentDeepResearch, 1)
	for _, tool := range []string{toolnames.Bash, toolnames.JobOutput, toolnames.JobKill} {
		require.Contains(t, deepResearch, tool,
			"deep-research must be able to run a command and collect its output")
	}
}

// TestQuestionToolReachesBranchesButNotSubagents pins the gate a branch depends
// on. Branches are dispatched like sub-agents, so the plain depth > 0 test used
// to withhold the question tool from them — but a branch talks to the user
// directly, and its preamble tells it to ask. An ordinary sub-agent has nobody
// to ask and must still be refused.
func TestQuestionToolReachesBranchesButNotSubagents(t *testing.T) {
	t.Run("branches are asked to talk to the user, so they get the tool", func(t *testing.T) {
		coord := newGateTestCoordinator(t, true)
		for _, id := range builtinBranches {
			require.Contains(t, toolNamesFor(t, coord, id, 1), toolnames.Question, id)
		}
	})

	t.Run("ordinary subagent has no user to ask", func(t *testing.T) {
		coord := newGateTestCoordinator(t, true)
		require.NotContains(t, toolNamesFor(t, coord, config.AgentExplore, 1), toolnames.Question)
	})

	t.Run("non-interactive has no user at all", func(t *testing.T) {
		coord := newGateTestCoordinator(t, false)
		require.NotContains(t, toolNamesFor(t, coord, config.AgentPlan, 1), toolnames.Question)
	})
}
