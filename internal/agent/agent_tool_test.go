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

	tool, err := coord.agentTool(0, config.Agent{}, config.ActiveAgent{})
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

	tool, err := coord.agentTool(0, config.Agent{}, config.ActiveAgent{})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":"hello"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, "agent message id missing from context", resp.Content)
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

	tool, err := coord.agentTool(0, config.Agent{}, config.ActiveAgent{})
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

	tool, err := coord.agentTool(0, config.Agent{}, config.ActiveAgent{})
	require.NoError(t, err)

	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":"hello"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, "session id missing from context", resp.Content)
}

// TestAgentToolUnknownSubagentTypeListsAvailable pins M10: an unknown
// subagent_type must return an error response naming the available
// types instead of a bare failure.
func TestAgentToolUnknownSubagentTypeListsAvailable(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0, config.Agent{}, config.ActiveAgent{})
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

	first, err := renderAgentToolDescription(reg.Metadata(), false)
	require.NoError(t, err)
	second, err := renderAgentToolDescription(reg.Metadata(), false)
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

	desc, err := renderAgentToolDescription(reg.Metadata(), false)
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

// TestAgentToolDescriptionTellsWhereAResultGoes pins that the description
// never promises a notification the caller cannot receive, and that a
// caller who can background an agent is told neither result reaches the
// user on its own.
func TestAgentToolDescriptionTellsWhereAResultGoes(t *testing.T) {
	t.Parallel()

	reg := newSubagentRegistry()
	reg.Reconcile(map[string]config.Agent{
		"research": {Description: "reads code", Mode: config.AgentModeSubagent},
	}, nil)

	foreground, err := renderAgentToolDescription(reg.Metadata(), false)
	require.NoError(t, err)
	require.Contains(t, foreground, "returned to you as the tool result")
	require.NotContains(t, foreground, "notification")
	require.NotContains(t, foreground, "run_in_background")

	background, err := renderAgentToolDescription(reg.Metadata(), true)
	require.NoError(t, err)
	require.Contains(t, background, "arrives later in a notification")
	require.Contains(t, background, "Neither is shown to the user")
	require.Contains(t, background, "end your turn")
	require.Contains(t, background, toolnames.AgentStatus)
	require.Contains(t, background, toolnames.AgentStop)
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

	for _, background := range []bool{false, true} {
		desc, err := renderAgentToolDescription(reg.Metadata(), background)
		require.NoError(t, err)

		require.NotContains(t, desc, "Branch agents:")
		require.NotContains(t, strings.ToLower(desc), "branch agent",
			"a caller that cannot fork must not read about branches at all")
		require.Contains(t, desc, "research")
	}
}

// TestAgentToolAllowedAgentsFiltersDescriptionAndDispatch pins the
// allowed_agents restriction at the tool level: a caller whose
// AllowedAgents narrows the dispatch table must see only those IDs in
// its own description, and dispatching anything else must be refused
// rather than silently falling through to the full registry.
func TestAgentToolAllowedAgentsFiltersDescriptionAndDispatch(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0, config.Agent{AllowedAgents: &config.AllowedAgentSet{Kind: config.ToolSetScope, Agents: []string{config.AgentExplore}}}, config.ActiveAgent{})
	require.NoError(t, err)
	require.Contains(t, tool.Info().Description, config.AgentExplore)
	require.NotContains(t, tool.Info().Description, config.AgentGeneral)

	ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "session-1")
	ctx = context.WithValue(ctx, tools.MessageIDContextKey, "msg-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":"look into this","subagent_type":"general"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError, "a subagent_type outside allowed_agents must be refused")
	require.Contains(t, resp.Content, "not available")
	require.Contains(t, resp.Content, config.AgentExplore, "the refusal must name what is available instead")
}

// TestAgentToolAllowedAgentsEmptyOmitsTheTool pins the degenerate case:
// an agent whose allowed_agents resolves to nothing dispatchable gets no
// agent tool at all, the same as an agent with no subagents configured.
func TestAgentToolAllowedAgentsEmptyOmitsTheTool(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	tool, err := coord.agentTool(0, config.Agent{AllowedAgents: &config.AllowedAgentSet{Kind: config.ToolSetScope}}, config.ActiveAgent{})
	require.NoError(t, err)
	require.Nil(t, tool)
}

// TestBuiltinBranchesOnlyDispatchExplore pins the allowed_agents
// restriction on the built-in branch agents: a plan, a co-designed plan or a
// root-cause finding is only as trustworthy as the read-only legwork
// behind it, so none should be able to reach general, or hand the
// decision off to another branch, through its own agent tool.
func TestBuiltinBranchesOnlyDispatchExplore(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	for _, id := range []string{config.AgentPlan, config.AgentCoDesign, config.AgentDeepResearch} {
		t.Run(id, func(t *testing.T) {
			agentCfg := coord.cfg.Config().Agents[id]
			toolList, err := coord.buildTools(agentCfg, config.ActiveAgent{}, "", 0)
			require.NoError(t, err)

			var agentTool fantasy.AgentTool
			for _, tool := range toolList {
				if tool.Info().Name == toolnames.Agent {
					agentTool = tool
				}
			}
			require.NotNil(t, agentTool, "%s must still hold the agent tool", id)
			require.Contains(t, agentTool.Info().Description, "- "+config.AgentExplore+":")
			// Substring checks on the bare IDs would false-positive:
			// explore's own description mentions "plan" and
			// "deep_research" in prose. Check for the rendered list
			// item instead, which is what dispatchability looks like.
			require.NotContains(t, agentTool.Info().Description, "- "+config.AgentGeneral+":")
			require.NotContains(t, agentTool.Info().Description, "- "+config.AgentPlan+":")
			require.NotContains(t, agentTool.Info().Description, "- "+config.AgentDeepResearch+":")
			require.NotContains(t, agentTool.Info().Description, "- "+config.AgentCoDesign+":")

			ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "session-1")
			ctx = context.WithValue(ctx, tools.MessageIDContextKey, "msg-1")
			resp, err := agentTool.Run(ctx, fantasy.ToolCall{
				ID:    "call-1",
				Name:  toolnames.Agent,
				Input: `{"prompt":"look into this","subagent_type":"general"}`,
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "dispatching general must be refused")
			require.Contains(t, resp.Content, "not available")
		})
	}
}

// callerLike returns the coder's config recast as another kind of caller, so
// buildTools sees a resolved tool whitelist that allows the agent tool.
func callerLike(coord *coordinator, mode config.AgentMode, allowed *config.AllowedAgentSet) config.Agent {
	caller := coord.cfg.Config().Agents[config.AgentCoder]
	caller.Mode = mode
	caller.AllowedAgents = allowed
	return caller
}

func scopeOf(ids ...string) *config.AllowedAgentSet {
	return &config.AllowedAgentSet{Kind: config.ToolSetScope, Agents: ids}
}

// buildAgentTool builds caller's tools at depth and returns the agent tool,
// or nil when it was left out. It also pins that the background management
// tools never outlive the agent tool they serve.
func buildAgentTool(t *testing.T, coord *coordinator, caller config.Agent, depth int) fantasy.AgentTool {
	t.Helper()

	toolList, err := coord.buildTools(caller, config.ActiveAgent{}, "", depth)
	require.NoError(t, err)

	var agentTool fantasy.AgentTool
	var names []string
	for _, tool := range toolList {
		names = append(names, tool.Info().Name)
		if tool.Info().Name == toolnames.Agent {
			agentTool = tool
		}
	}
	if agentTool == nil {
		require.NotContains(t, names, toolnames.AgentStatus)
		require.NotContains(t, names, toolnames.AgentStop)
	}
	return agentTool
}

// TestBranchTypesAreOfferedOnlyToCallersThatMayFork pins that the agent
// tool's description lists branch agents to exactly the callers that can
// fork one: the top-level conversation and a branch, in an interactive
// session. Anyone else would be offered a call that is always refused.
func TestBranchTypesAreOfferedOnlyToCallersThatMayFork(t *testing.T) {
	interactive := newGateTestCoordinator(t, true)

	// The default budget of 2 leaves a branch at depth 2 with no agent tool
	// at all, so a branch nested under a branch needs a budget of 3.
	deeper := newGateTestCoordinator(t, true)
	budget := 3
	deeper.cfg.Config().Options.SubagentDepth = &budget

	tests := []struct {
		name   string
		coord  *coordinator
		caller config.Agent
		depth  int
		branch bool
	}{
		{"the top-level conversation", interactive, callerLike(interactive, config.AgentModePrimary, nil), 0, true},
		{"a branch", interactive, callerLike(interactive, config.AgentModeBranch, nil), 1, true},
		{"a branch under a branch", deeper, callerLike(deeper, config.AgentModeBranch, nil), 2, true},
		{"a subagent", interactive, callerLike(interactive, config.AgentModeSubagent, nil), 1, false},
		{"an agent with no mode", interactive, callerLike(interactive, "", nil), 1, false},
		{"a headless top-level conversation", newGateTestCoordinator(t, false), callerLike(interactive, config.AgentModePrimary, nil), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentTool := buildAgentTool(t, tt.coord, tt.caller, tt.depth)
			require.NotNil(t, agentTool, "ordinary subagents are still reachable")

			desc := agentTool.Info().Description
			require.Contains(t, desc, "- "+config.AgentExplore+":")
			if tt.branch {
				require.Contains(t, desc, "Branch agents:")
				require.Contains(t, desc, "- "+config.AgentPlan+":")
			} else {
				require.NotContains(t, desc, "Branch agents:")
				require.NotContains(t, desc, "- "+config.AgentPlan+":")
				require.NotContains(t, strings.ToLower(desc), "branch agent")
			}
		})
	}
}

// TestAgentToolIsOmittedWhenNothingIsDispatchable pins that the agent tool
// exists only for a caller with something it can really dispatch. A tool
// whose every target is refused at call time is worse than no tool: the
// model keeps trying it.
func TestAgentToolIsOmittedWhenNothingIsDispatchable(t *testing.T) {
	interactive := newGateTestCoordinator(t, true)
	headless := newGateTestCoordinator(t, false)

	onlyBranches := scopeOf(config.AgentPlan, config.AgentCoDesign)

	omitted := []struct {
		name   string
		coord  *coordinator
		caller config.Agent
		depth  int
	}{
		{"a subagent allowed only branches", interactive, callerLike(interactive, config.AgentModeSubagent, onlyBranches), 1},
		{"an agent with no mode allowed only branches", interactive, callerLike(interactive, "", onlyBranches), 1},
		{"a headless top-level conversation allowed only branches", headless, callerLike(headless, config.AgentModePrimary, onlyBranches), 0},
	}
	for _, tt := range omitted {
		t.Run(tt.name, func(t *testing.T) {
			require.Nil(t, buildAgentTool(t, tt.coord, tt.caller, tt.depth))

			tool, err := tt.coord.agentTool(tt.depth, tt.caller, config.ActiveAgent{})
			require.NoError(t, err)
			require.Nil(t, tool)
		})
	}

	t.Run("the same top-level conversation keeps it when interactive", func(t *testing.T) {
		agentTool := buildAgentTool(t, interactive, callerLike(interactive, config.AgentModePrimary, onlyBranches), 0)
		require.NotNil(t, agentTool)
		require.Contains(t, agentTool.Info().Description, "- "+config.AgentPlan+":")
	})

	t.Run("a subagent that is also allowed explore keeps it, minus the branches", func(t *testing.T) {
		agentTool := buildAgentTool(t, interactive,
			callerLike(interactive, config.AgentModeSubagent, scopeOf(config.AgentExplore, config.AgentPlan)), 1)
		require.NotNil(t, agentTool)
		require.Contains(t, agentTool.Info().Description, "- "+config.AgentExplore+":")
		require.NotContains(t, agentTool.Info().Description, "- "+config.AgentPlan+":")
	})
}

// TestAgentToolIsOmittedWhenOnlyBranchesAreRegistered covers the same rule
// when the registry itself, not allowed_agents, is what leaves only branch
// agents: a headless session has nobody to hand a branch to.
func TestAgentToolIsOmittedWhenOnlyBranchesAreRegistered(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "interactive"}[interactive], func(t *testing.T) {
			coord := newGateTestCoordinator(t, interactive)
			agents := coord.cfg.Config().Agents
			for id, agentCfg := range agents {
				if agentCfg.Mode != config.AgentModePrimary && agentCfg.Mode != config.AgentModeBranch {
					delete(agents, id)
				}
			}
			coord.reconcileSubagents()
			require.NotZero(t, coord.subagents.Len(), "test premise: branch agents remain registered")

			agentTool := buildAgentTool(t, coord, agents[config.AgentCoder], 0)
			if interactive {
				require.NotNil(t, agentTool)
			} else {
				require.Nil(t, agentTool)
			}
		})
	}
}

// TestSubagentCallingABranchIsRefusedWithoutDispatching pins the call-time
// half of the rule: a model that names a branch it was never offered gets a
// refusal that points at ordinary subagents, and no child session is made.
func TestSubagentCallingABranchIsRefusedWithoutDispatching(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	agentTool := buildAgentTool(t, coord, coord.cfg.Config().Agents[config.AgentGeneral], 1)
	require.NotNil(t, agentTool)

	ctx := context.WithValue(context.Background(), tools.SessionIDContextKey, "session-1")
	ctx = context.WithValue(ctx, tools.MessageIDContextKey, "msg-1")
	resp, err := agentTool.Run(ctx, fantasy.ToolCall{
		ID:    "call-1",
		Name:  toolnames.Agent,
		Input: `{"prompt":"x","subagent_type":"plan"}`,
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "subagent")
	require.Contains(t, resp.Content, "Only the top-level conversation or another branch")

	sessions, err := coord.sessions.List(t.Context())
	require.NoError(t, err)
	require.Empty(t, sessions, "a refused fork must not create a child session")
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

// TestReportHeaderRoundTripsThroughBody: the export path strips the
// banner by structure, so whatever withReportHeader writes must come
// back out as exactly the original output.
func TestReportHeaderRoundTripsThroughBody(t *testing.T) {
	t.Parallel()

	const id = "rpt_1a2b3c4d"
	resp := withReportHeader(fantasy.NewTextResponse("the report body\n\nsecond paragraph"), id, "explore", "find it")

	report := tools.Report{ID: id, Content: resp.Content}
	require.Equal(t, "the report body\n\nsecond paragraph", report.Body())
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

// TestAgentToolDescriptionHasSubagent pins HasSubagent, the
// counterpart check to HasBranch: it must report false when every
// configured agent is a branch, since the template then has nothing
// to list under the "Available agent types:" section.
func TestAgentToolDescriptionHasSubagent(t *testing.T) {
	t.Parallel()

	allBranch := agentToolDescription{Agents: []agentToolDescriptionAgent{
		{ID: "plan", Branch: true},
		{ID: "deep-research", Branch: true},
	}}
	require.False(t, allBranch.HasSubagent(), "an all-branch registry has no plain dispatch target")

	mixed := agentToolDescription{Agents: []agentToolDescriptionAgent{
		{ID: "plan", Branch: true},
		{ID: "explore", Branch: false},
	}}
	require.True(t, mixed.HasSubagent())

	require.False(t, agentToolDescription{}.HasSubagent(), "an empty registry has no plain dispatch target")
}
