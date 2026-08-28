package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/config"
)

// TestAgentToolDefaultsToTaskWhenSubagentTypeOmitted pins the backward
// compatible fallback: an agent call with no subagent_type must resolve
// to the task agent rather than failing with "unknown subagent_type". A
// bare context (no session/message) makes the closure fail one step
// later, on the session-id check, which only happens after the type
// has already resolved successfully.
func TestAgentToolDefaultsToTaskWhenSubagentTypeOmitted(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0)
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  AgentToolName,
		Input: `{"prompt":"hello"}`,
	})
	require.Error(t, err)
	require.Equal(t, "session id missing from context", err.Error())
	require.Empty(t, resp)
}

// TestAgentToolUnknownSubagentTypeListsAvailable pins M10: an unknown
// subagent_type must return an error response naming the available
// types instead of a bare failure.
func TestAgentToolUnknownSubagentTypeListsAvailable(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0)
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  AgentToolName,
		Input: `{"prompt":"hello","subagent_type":"bogus-type"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "bogus-type")
	for _, id := range []string{config.AgentExplore, config.AgentGeneral} {
		require.Contains(t, resp.Content, id)
	}
}

// TestBuildSubagentsExcludesPrimaryMode pins the dispatch table
// invariant: a primary-mode agent (coder) must never be dispatchable
// through the agent tool, since that would let a sub-agent reach a
// primary agent's tool set.
func TestBuildSubagentsExcludesPrimaryMode(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	_, ok := coord.subagents.Get(config.AgentCoder)
	require.False(t, ok, "the primary agent must not be dispatchable")
	for _, id := range []string{config.AgentExplore, config.AgentGeneral} {
		_, ok := coord.subagents.Get(id)
		require.Truef(t, ok, "%s should be dispatchable", id)
	}
}

// TestSubagentRegistryMetadataIsStable pins sort stability: Go map
// iteration order is randomized per range, so without the sort in
// Metadata, two renders of the same subagent set could disagree on
// ordering and churn the agent tool's description.
func TestSubagentRegistryMetadataIsStable(t *testing.T) {
	t.Parallel()

	reg := newSubagentRegistry()
	reg.Reconcile(map[string]config.Agent{
		"zeta":  {Description: "zeta agent", Mode: config.AgentModeSubagent},
		"alpha": {Description: "alpha agent", Mode: config.AgentModeSubagent},
		"mid":   {Description: "mid agent", Mode: config.AgentModeSubagent},
	}, nil)

	require.Equal(t, []string{"alpha", "mid", "zeta"}, reg.IDs())

	first, err := renderAgentToolDescription(reg.Metadata())
	require.NoError(t, err)
	second, err := renderAgentToolDescription(reg.Metadata())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Less(t,
		strings.Index(first, "alpha"),
		strings.Index(first, "mid"),
		"alpha must be rendered before mid")
	require.Less(t,
		strings.Index(first, "mid"),
		strings.Index(first, "zeta"),
		"mid must be rendered before zeta")
}

// TestAgentToolDescriptionSeparatesBranches pins that a branch agent is not
// listed as an ordinary dispatch target. Mixed into the plain list, the
// model would call it expecting a result and instead block on a user who
// was never told to do anything.
func TestAgentToolDescriptionSeparatesBranches(t *testing.T) {
	t.Parallel()

	reg := newSubagentRegistry()
	reg.Reconcile(map[string]config.Agent{
		"research": {Description: "reads code", Mode: config.AgentModeSubagent},
		"pairing":  {Description: "thinks it through with you", Mode: config.AgentModeBranch},
	}, nil)

	desc, err := renderAgentToolDescription(reg.Metadata())
	require.NoError(t, err)

	branchAt := strings.Index(desc, "Branch agents:")
	require.Positive(t, branchAt, "a branch agent must get its own section")

	require.Less(t, strings.Index(desc, "research"), branchAt,
		"an ordinary subagent belongs in the plain list")
	require.Greater(t, strings.Index(desc, "pairing"), branchAt,
		"a branch agent must be listed under the branch section, not with the rest")

	section := desc[branchAt:]
	require.Contains(t, section, "suspends")
	require.Contains(t, section, "user")
	require.Contains(t, section, "merged")
}

// TestAgentToolDescriptionOmitsTheBranchSection pins that the explanation
// stays out of the prompt when no branch agent is configured. There is no
// builtin one, so this is the default shape.
func TestAgentToolDescriptionOmitsTheBranchSection(t *testing.T) {
	t.Parallel()

	reg := newSubagentRegistry()
	reg.Reconcile(map[string]config.Agent{
		"research": {Description: "reads code", Mode: config.AgentModeSubagent},
	}, nil)

	desc, err := renderAgentToolDescription(reg.Metadata())
	require.NoError(t, err)

	require.NotContains(t, desc, "Branch agents:")
	require.Contains(t, desc, "research")
}
