package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

const testReviewerAgent = "reviewer"

// addReviewerAgent registers a second primary agent so switching has
// somewhere to go.
func addReviewerAgent(t *testing.T, coord *coordinator) {
	t.Helper()
	coder := coord.cfg.Config().Agents[config.AgentCoder]
	coord.cfg.Config().Agents[testReviewerAgent] = config.Agent{
		ID:           testReviewerAgent,
		Name:         "Reviewer",
		Description:  "reviews code",
		Mode:         config.AgentModePrimary,
		Model:        config.ModelMain,
		Prompt:       "you review",
		AllowedTools: coder.AllowedTools,
		AllowedMCP:   coder.AllowedMCP,
	}
	coord.reconcileSubagents()
}

// TestSwitchAgentUpdatesSessionRecordAndLeavesTrail pins the contract a
// mid-session switch has to honor: the session record is what later
// turns read, so it must be updated durably, and the transcript must
// explain the change — otherwise the assistant's behavior shifts with
// nothing in the history accounting for it.
func TestSwitchAgentUpdatesSessionRecordAndLeavesTrail(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// A session with no recorded agent runs the coder.
	agentCfg, err := coord.sessionAgentConfig(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, agentCfg.ID)

	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))

	stored, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, stored.Agent,
		"the switch must be recorded on the session, not just held in memory")
	require.Equal(t, "large-model", stored.Model.Model,
		"the recorded model must follow the new agent's own model preference")

	agentCfg, err = coord.sessionAgentConfig(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, agentCfg.ID,
		"the next turn must resolve the agent the session was switched to")

	msgs, err := coord.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "a switch must leave exactly one trail message")
	require.Equal(t, message.System, msgs[0].Role,
		"the trail is transcript-only; a System role keeps it out of the model's context")
	require.Equal(t, testReviewerAgent, msgs[0].Agent)
	require.Contains(t, msgs[0].Content().Text, "Reviewer")

	// Selecting the agent already in effect changes nothing and adds no
	// second trail.
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))
	msgs, err = coord.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "re-selecting the active agent must not litter the transcript")
}

// TestSwitchAgentRejectsUndispatchableTargets pins that only primary
// agents are switchable. Pointing a session at a subagent or at a name
// that does not exist would leave every later turn falling back to the
// coder with no explanation, so it fails loudly at the switch instead.
func TestSwitchAgentRejectsUndispatchableTargets(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	require.ErrorIs(t, coord.SwitchAgent(t.Context(), sess.ID, "no-such-agent"), errAgentNotAvailable)
	require.ErrorIs(t, coord.SwitchAgent(t.Context(), sess.ID, config.AgentExplore), errAgentNotAvailable)

	stored, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, stored.Agent, "a rejected switch must not touch the session record")
}

// TestSessionAgentConfigFallsBackWhenAgentDisappears covers a config
// edit that removes the agent a session was switched to. Erroring out
// would strand the session: the user cannot type the command that fixes
// it if no turn can run. The coder takes over instead.
func TestSessionAgentConfigFallsBackWhenAgentDisappears(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sess.ID, testReviewerAgent))

	delete(coord.cfg.Config().Agents, testReviewerAgent)

	agentCfg, err := coord.sessionAgentConfig(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, config.AgentCoder, agentCfg.ID)
}

// TestConsecutiveTurnsRecordTheirOwnAgent is the end of the chain: two
// turns on one session, each resolving a different agent, must produce
// two assistant messages each stamped with the agent that actually ran
// it. Reading the stamp off the executor instead of the turn made both
// messages claim whichever agent the coordinator was built with.
func TestConsecutiveTurnsRecordTheirOwnAgent(t *testing.T) {
	sa, env, resolved := newStreamTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	for _, agentID := range []string{config.AgentCoder, testReviewerAgent} {
		turn := resolved
		turn.ID = agentID
		_, err := sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			Agent:     turn,
			Prompt:    "hello",
		})
		require.NoError(t, err)
	}

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var agents []string
	for _, m := range msgs {
		if m.Role == message.Assistant {
			agents = append(agents, m.Agent)
		}
	}
	require.Equal(t, []string{config.AgentCoder, testReviewerAgent}, agents,
		"each assistant message must carry the agent that ran its own turn")
}
