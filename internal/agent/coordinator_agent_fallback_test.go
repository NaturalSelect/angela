package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// sessionWhoseAgentLeft returns a session that ran the reviewer agent
// and held a pick for reviewer and one for explore, after a config edit
// removed reviewer, together with a child it dispatched.
func sessionWhoseAgentLeft(t *testing.T, coord *coordinator) (parentID, childID string) {
	t.Helper()
	addReviewerAgent(t, coord)
	parentID, childID = exploreChild(t, coord)

	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")
	require.NoError(t, coord.SwitchAgent(t.Context(), parentID, testReviewerAgent))
	pickModelFor(t, coord, parentID, testReviewerAgent, "small-model")

	delete(coord.cfg.Config().Agents, testReviewerAgent)
	return parentID, childID
}

// TestFallbackSessionKeepsThePicksItHolds pins that a session which fell
// back to the default agent still carries the whole pick table: the
// picks for other agents are what a dispatch from it reads, so losing
// them in the fallback silently reverts those agents to the config.
func TestFallbackSessionKeepsThePicksItHolds(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := sessionWhoseAgentLeft(t, coord)

	active, err := coord.activeAgentFor(t.Context(), parentID)
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, active.Agent.ID)
	require.Contains(t, active.AgentModels, config.AgentExplore)
	require.Contains(t, active.AgentModels, testReviewerAgent)

	resolved, err := coord.resolveSubagent(t.Context(), config.AgentExplore, childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", resolved.Model.ModelCfg.Model,
		"a dispatch from a fallback session must still see the pick made for it")
}

// TestEditingAFallbackSessionKeepsThePicksItHolds pins that the first
// edit after the fallback does not rewrite the record from an instance
// that never saw the table, which would erase every pick the session
// held, including those for agents that are still configured.
func TestEditingAFallbackSessionKeepsThePicksItHolds(t *testing.T) {
	for name, edit := range map[string]config.ActiveAgentEdit{
		"toggle thinking": {ToggleThink: true},
		"switch model":    switchModelEdit("large-model"),
	} {
		t.Run(name, func(t *testing.T) {
			coord := newModelPrefTestCoordinator(t, nil)
			parentID, _ := sessionWhoseAgentLeft(t, coord)

			_, err := coord.EditActiveAgent(t.Context(), parentID, edit)
			require.NoError(t, err)

			stored, err := coord.sessions.Get(t.Context(), parentID)
			require.NoError(t, err)
			require.Contains(t, stored.ActiveAgent.AgentModels, config.AgentExplore,
				"the pick for an agent that is still configured must survive the edit")
			require.Contains(t, stored.ActiveAgent.AgentModels, testReviewerAgent,
				"the pick for the agent that left must survive too, for when it returns")
		})
	}
}
