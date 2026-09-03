package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSummarizeRoutesToTheSubAgentExecutor pins the wiring inside
// coordinator.Summarize itself, not just the routing helper it calls: a
// session that belongs to a sub-agent must have its Summarize call reach
// that sub-agent's own executor. Before this was fixed, Summarize always
// ran on currentAgent regardless of which executor owned the session,
// racing whatever turn the sub-agent's own executor had in flight on it.
//
// currentAgent here is a real executor pointed at an unreachable provider
// URL. If Summarize fell back to it, the call would fail trying to reach
// that provider instead of returning the nil the mock child executor gives
// immediately, and the mock would never record the call.
func TestSummarizeRoutesToTheSubAgentExecutor(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	childID := coord.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	_, err = coord.sessions.CreateTaskSession(t.Context(), childID, parent.ID, "explore run")
	require.NoError(t, err)

	child := newMockSessionAgent(t, config.AgentExplore, nil)
	coord.registerSubagentRoute(childID, config.AgentExplore, child)

	require.NoError(t, coord.Summarize(t.Context(), childID))
	require.Equal(t, []string{childID}, child.summarized)
}
