package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/stretchr/testify/require"
)

// TestSubagentsHaveNoDelegationTools pins M1 and the H6 execution-layer
// half: a sub-agent must never receive a tool that can start another
// agent (agent, agentic_fetch), regardless of what its AllowedTools
// configuration says, because that would let dispatch depth grow past
// 1. The primary coder agent, which is not a sub-agent, must still get
// both when its AllowedTools allows them.
func TestSubagentsHaveNoDelegationTools(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	toolNames := func(t *testing.T, agentID string, isSubAgent bool) []string {
		t.Helper()
		agentCfg, ok := coord.cfg.Config().Agents[agentID]
		require.True(t, ok, "agent %q must be configured", agentID)
		toolList, err := coord.buildTools(context.Background(), agentCfg, isSubAgent)
		require.NoError(t, err)
		names := make([]string, len(toolList))
		for i, tool := range toolList {
			names[i] = tool.Info().Name
		}
		return names
	}

	for _, id := range []string{config.AgentExplore, config.AgentGeneral} {
		t.Run(id, func(t *testing.T) {
			names := toolNames(t, id, true)
			require.NotContains(t, names, AgentToolName, "sub-agent %q must not hold the agent tool", id)
			require.NotContains(t, names, tools.AgenticFetchToolName, "sub-agent %q must not hold agentic_fetch", id)
		})
	}

	t.Run("explore has no bash", func(t *testing.T) {
		names := toolNames(t, config.AgentExplore, true)
		require.NotContains(t, names, "bash")
	})

	t.Run(config.AgentCoder, func(t *testing.T) {
		names := toolNames(t, config.AgentCoder, false)
		require.Contains(t, names, AgentToolName, "coder must hold the agent tool")
		require.Contains(t, names, tools.AgenticFetchToolName, "coder must hold agentic_fetch")
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

	toolList, err := coord.buildTools(context.Background(), agents[config.AgentCoder], false)
	require.NoError(t, err, "an empty dispatch table must not fail the coder's tool build")

	var names []string
	for _, tool := range toolList {
		names = append(names, tool.Info().Name)
	}
	require.NotContains(t, names, AgentToolName)
	require.Contains(t, names, "bash", "the rest of the tool list must survive")
}

// TestSubagentToolsAreHookWrapped pins the security fix: a sub-agent's
// tool calls used to skip PreToolUse hooks entirely, so a delegated
// `write` reached the disk without ever facing the user's policy. Both
// the coder and its sub-agents run through the same wrapper now.
func TestSubagentToolsAreHookWrapped(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	cfg := coord.cfg.Config()
	cfg.Hooks = map[string][]config.HookConfig{
		hooks.EventPreToolUse: {{Matcher: "bash", Command: `exit 2`}},
	}
	require.NoError(t, cfg.ValidateHooks())

	for _, tc := range []struct {
		agentID    string
		isSubAgent bool
	}{
		{config.AgentCoder, false},
		{config.AgentGeneral, true},
	} {
		t.Run(tc.agentID, func(t *testing.T) {
			toolList, err := coord.buildTools(context.Background(), cfg.Agents[tc.agentID], tc.isSubAgent)
			require.NoError(t, err)

			var bash fantasy.AgentTool
			for _, tool := range toolList {
				if tool.Info().Name == "bash" {
					bash = tool
				}
			}
			require.NotNil(t, bash, "bash must be present to be wrapped")
			require.IsType(t, &hookedTool{}, bash, "every tool must face PreToolUse hooks")

			resp, err := bash.Run(t.Context(), fantasy.ToolCall{
				ID: "call-1", Name: "bash", Input: `{"command":"echo hi"}`,
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

	run := func(t *testing.T, agentID string, isSubAgent bool) string {
		t.Helper()
		toolList, err := coord.buildTools(context.Background(), cfg.Agents[agentID], isSubAgent)
		require.NoError(t, err)
		for _, tool := range toolList {
			if tool.Info().Name != "view" {
				continue
			}
			resp, err := tool.Run(t.Context(), fantasy.ToolCall{
				ID: "call-1", Name: "view", Input: `{"file_path":"/tmp/x"}`,
			})
			require.NoError(t, err)
			return resp.Content
		}
		t.Fatal("view tool not found")
		return ""
	}

	require.Contains(t, run(t, config.AgentCoder, false), config.AgentCoder+"/0")
	require.Contains(t, run(t, config.AgentGeneral, true), config.AgentGeneral+"/1")
}
