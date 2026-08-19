package agent

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSecondPrimaryStaysOutOfTheDispatchTable pins the boundary between
// the two ways an agent is reached. A primary agent is what a session
// runs on — you switch to it. A subagent is what the agent tool
// delegates to. Letting a second primary into the dispatch table would
// offer the LLM a session driver as a delegation target, which is not
// what declaring "mode: primary" asks for.
func TestSecondPrimaryStaysOutOfTheDispatchTable(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	require.Equal(t, config.AgentModePrimary,
		coord.cfg.Config().Agents[testReviewerAgent].Mode,
		"sanity check: the second primary kept its mode")

	require.NotContains(t, coord.subagents.IDs(), testReviewerAgent,
		"a primary agent must not be offered as a delegation target")
	require.NotContains(t, coord.subagents.IDs(), config.AgentCoder,
		"the coder is a primary too and must stay out for the same reason")
	require.Contains(t, coord.subagents.IDs(), config.AgentExplore,
		"subagents must still be dispatchable")

	// It is switchable, which is the whole point of declaring primary.
	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))
}

// TestSecondPrimaryGetsDelegationToolsWhenDriving pins the other half:
// a second primary drives sessions, so when it runs as one it must get
// the same delegation tools the coder gets. Dispatch depth is still
// pinned at 1 by the isSubAgent flag, not by which agent it is.
func TestSecondPrimaryGetsDelegationToolsWhenDriving(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	resolved, err := coord.resolveAgent(context.Background(),
		instantiate(t, coord, testReviewerAgent), false)
	require.NoError(t, err)

	require.Contains(t, toolNames(resolved), AgentToolName,
		"a primary agent driving a session must be able to delegate")
}
