package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// TestAgentToolRejectsAnEmptyPrompt pins that a blank prompt is
// rejected before any session/message context or subagent_type lookup
// happens — the cheapest possible validation failure.
func TestAgentToolRejectsAnEmptyPrompt(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0)
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":""}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, "prompt is required", resp.Content)
}

// TestAgentToolRequiresAgentMessageID pins the second context check: a
// session id alone is not enough to dispatch, since the report banner
// and reply routing both need the originating message id too.
func TestAgentToolRequiresAgentMessageID(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":"hello"}`,
	})
	require.Error(t, err)
	require.Equal(t, "agent message id missing from context", err.Error())
	require.Empty(t, resp)
}

// TestAgentToolReportsWhenDispatchFailsAfterTheTypeResolves pins that
// a subagent_type which resolves in the dispatch table but no longer
// exists in config (a stale entry, e.g. mid-reload) comes back as a
// tool error naming the type, rather than a coordinator-level panic
// or an opaque failure.
func TestAgentToolReportsWhenDispatchFailsAfterTheTypeResolves(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	coord.subagents.Reconcile(map[string]config.Agent{
		"ghost": {ID: "ghost", Mode: config.AgentModeSubagent, Description: "no longer configured"},
	}, nil)

	tool, err := coord.agentTool(0)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "session-1")
	ctx = context.WithValue(ctx, tools.MessageIDContextKey, "msg-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":"hello","subagent_type":"ghost"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "ghost")
	require.Contains(t, resp.Content, "unavailable")
}

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
		Name:  toolnames.Agent,
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
		Name:  toolnames.Agent,
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

// TestReportHeaderFrontsSuccessfulOutput pins the banner shape: the id
// and the loading instruction have to survive into the tool result,
// because after a compaction they are the only route back to the text.
func TestReportHeaderFrontsSuccessfulOutput(t *testing.T) {
	t.Parallel()

	resp := withReportHeader(
		fantasy.NewTextResponse("the report body"),
		"rpt_1a2b3c4d", "deep-research", "websocket vs sse",
	)

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "rpt_1a2b3c4d")
	require.Contains(t, resp.Content, "deep-research")
	require.Contains(t, resp.Content, "websocket vs sse")
	require.Contains(t, resp.Content, toolnames.LoadReport)
	require.True(t, strings.HasSuffix(resp.Content, "\n\nthe report body"),
		"the original output must be preserved verbatim behind a blank line")
}

// TestReportHeaderOmitsTheTaskWhenUnlabelled keeps the banner from
// carrying an empty task="" that reads as a missing value.
func TestReportHeaderOmitsTheTaskWhenUnlabelled(t *testing.T) {
	t.Parallel()

	resp := withReportHeader(fantasy.NewTextResponse("body"), "rpt_1a2b3c4d", "explore", "")

	require.Contains(t, resp.Content, "rpt_1a2b3c4d")
	require.NotContains(t, resp.Content, "task=")
}

// TestReportHeaderLeavesFailuresAlone: a dispatch that errored has no
// stored report, so advertising an id for it would send the model after
// something that cannot be loaded.
func TestReportHeaderLeavesFailuresAlone(t *testing.T) {
	t.Parallel()

	failed := withReportHeader(
		fantasy.NewTextErrorResponse("Subagent \"explore\" is unavailable"),
		"rpt_1a2b3c4d", "explore", "find it",
	)
	require.True(t, failed.IsError)
	require.Equal(t, `Subagent "explore" is unavailable`, failed.Content)

	empty := withReportHeader(fantasy.NewTextResponse(""), "rpt_1a2b3c4d", "explore", "find it")
	require.Empty(t, empty.Content)
}
