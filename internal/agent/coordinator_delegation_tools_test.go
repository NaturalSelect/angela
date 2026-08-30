package agent

import (
	"fmt"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// TestSubagentsHaveNoDelegationTools pins M1 and the H6 execution-layer
// half: a sub-agent must never receive the agent tool, regardless of
// what its AllowedTools configuration says, because that would let
// dispatch depth grow past the configured options.subagent_depth
// budget (1 by default). The primary coder agent, which runs at depth
// 0, must still get it when its AllowedTools allows it.
func TestSubagentsHaveNoDelegationTools(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	toolNames := func(t *testing.T, agentID string, depth int) []string {
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

	for _, id := range []string{config.AgentExplore, config.AgentGeneral} {
		t.Run(id, func(t *testing.T) {
			names := toolNames(t, id, 1)
			require.NotContains(t, names, toolnames.Agent, "sub-agent %q must not hold the agent tool", id)
		})
	}

	t.Run("explore has no bash", func(t *testing.T) {
		names := toolNames(t, config.AgentExplore, 1)
		require.NotContains(t, names, toolnames.Bash)
	})

	t.Run(config.AgentCoder, func(t *testing.T) {
		names := toolNames(t, config.AgentCoder, 0)
		require.Contains(t, names, toolnames.Agent, "coder must hold the agent tool")
	})
}

// TestBuildToolsOmitsAgentToolWhenNoSubagents covers the configuration
// where the user disabled every subagent. Building the agent tool used
// to fail hard in that case, which took down the coder's whole tool
// list; the tool is simply left out instead.
func TestBuildToolsOmitsAgentToolWhenNoSubagents(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	agents := coord.cfg.Config().Agents
	for id, agentCfg := range agents {
		if agentCfg.Mode != config.AgentModePrimary {
			delete(agents, id)
		}
	}
	coord.reconcileSubagents()
	require.Zero(t, coord.subagents.Len(), "test premise: no dispatchable subagents remain")

	toolList, err := coord.buildTools(agents[config.AgentCoder], "", 0)
	require.NoError(t, err, "an empty dispatch table must not fail the coder's tool build")

	var names []string
	for _, tool := range toolList {
		names = append(names, tool.Info().Name)
	}
	require.NotContains(t, names, toolnames.Agent)
	require.Contains(t, names, toolnames.Bash, "the rest of the tool list must survive")
}

// TestSubagentToolsAreHookWrapped pins the security fix: a sub-agent's
// tool calls used to skip PreToolUse hooks entirely, so a delegated
// `write` reached the disk without ever facing the user's policy. Both
// the coder and its sub-agents run through the same wrapper now.
func TestSubagentToolsAreHookWrapped(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	cfg := coord.cfg.Config()
	cfg.Hooks = map[string][]config.HookConfig{
		hooks.EventPreToolUse: {{Matcher: toolnames.Bash, Command: `exit 2`}},
	}
	require.NoError(t, cfg.ValidateHooks())

	for _, tc := range []struct {
		agentID string
		depth   int
	}{
		{config.AgentCoder, 0},
		{config.AgentGeneral, 1},
	} {
		t.Run(tc.agentID, func(t *testing.T) {
			toolList, err := coord.buildTools(cfg.Agents[tc.agentID], "", tc.depth)
			require.NoError(t, err)

			var bash fantasy.AgentTool
			for _, tool := range toolList {
				if tool.Info().Name == toolnames.Bash {
					bash = tool
				}
			}
			require.NotNil(t, bash, "bash must be present to be wrapped")
			require.IsType(t, &hookedTool{}, bash, "every tool must face PreToolUse hooks")

			resp, err := bash.Run(t.Context(), fantasy.ToolCall{
				ID: "call-1", Name: toolnames.Bash, Input: `{"command":"echo hi"}`,
			})
			require.NoError(t, err)
			require.True(t, resp.IsError, "a denying hook must block the call")
		})
	}
}

// TestHookRunnerCarriesAgentIdentity pins what a hook is told about its
// caller, which is the only way a policy can treat a delegated call
// differently from a top-level one.
func TestHookRunnerCarriesAgentIdentity(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	cfg := coord.cfg.Config()
	cfg.Hooks = map[string][]config.HookConfig{
		hooks.EventPreToolUse: {{Command: `printf '{"decision":"deny","reason":"%s/%s"}' "$ANGELA_AGENT_ID" "$ANGELA_AGENT_DEPTH"`}},
	}
	require.NoError(t, cfg.ValidateHooks())

	run := func(t *testing.T, agentID string, depth int) string {
		t.Helper()
		toolList, err := coord.buildTools(cfg.Agents[agentID], "", depth)
		require.NoError(t, err)
		for _, tool := range toolList {
			if tool.Info().Name != toolnames.View {
				continue
			}
			resp, err := tool.Run(t.Context(), fantasy.ToolCall{
				ID: "call-1", Name: toolnames.View, Input: `{"file_path":"/tmp/x"}`,
			})
			require.NoError(t, err)
			return resp.Content
		}
		t.Fatal("view tool not found")
		return ""
	}

	require.Contains(t, run(t, config.AgentCoder, 0), config.AgentCoder+"/0")
	require.Contains(t, run(t, config.AgentGeneral, 1), config.AgentGeneral+"/1")

	// A raised budget must let the hook see depths beyond the binary
	// 0/1 this used to be pinned to.
	cfg.Options.SubagentDepth = ptrTo(2)
	require.Contains(t, run(t, config.AgentGeneral, 2), config.AgentGeneral+"/2")
}

// TestSubagentDepthBudget pins the configurable delegation budget: a
// turn holds the agent tool only while its dispatch depth is still
// under Options.SubagentDepth, not just at the historical fixed depth
// of 1.
func TestSubagentDepthBudget(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	coderCfg := coord.cfg.Config().Agents[config.AgentCoder]

	for _, tc := range []struct {
		maxDepth int
		depth    int
		delegate bool
	}{
		{maxDepth: 0, depth: 0, delegate: false},
		{maxDepth: 1, depth: 0, delegate: true},
		{maxDepth: 1, depth: 1, delegate: false},
		{maxDepth: 2, depth: 0, delegate: true},
		{maxDepth: 2, depth: 1, delegate: true},
		{maxDepth: 2, depth: 2, delegate: false},
	} {
		t.Run(fmt.Sprintf("max=%d/depth=%d", tc.maxDepth, tc.depth), func(t *testing.T) {
			coord.cfg.Config().Options.SubagentDepth = ptrTo(tc.maxDepth)

			toolList, err := coord.buildTools(coderCfg, "", tc.depth)
			require.NoError(t, err)

			var names []string
			for _, tool := range toolList {
				names = append(names, tool.Info().Name)
			}
			if tc.delegate {
				require.Contains(t, names, toolnames.Agent)
			} else {
				require.NotContains(t, names, toolnames.Agent)
			}
		})
	}
}
