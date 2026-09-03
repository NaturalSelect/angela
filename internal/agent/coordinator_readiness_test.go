package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestCallerCancellationDoesNotBreakLaterTurns guards a client/server
// hang that used to strand every session on a workspace.
//
// Agent construction ran its setup — system prompt, tool list — on
// goroutines in a coordinator-wide errgroup, seeded with the caller's
// context. The server builds the coder from a short-lived HTTP request
// context, so when that handler returned, the cancellation landed in
// the shared errgroup and every later run failed at its Wait() before
// emitting anything.
//
// Resolution is now per turn and runs on the turn's own context, so a
// dead caller cannot leave anything behind. This test pins that: cancel
// the context that built the agent, then require a later turn to
// resolve cleanly.
func TestCallerCancellationDoesNotBreakLaterTurns(t *testing.T) {
	env := testEnv(t)

	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"),
		[]byte(`{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"mock": {"id": "mock", "name": "Mock", "type": "openai",
    "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
    "models": [{"id": "m", "name": "M", "context_window": 8192, "default_max_tokens": 128}]}},
  "slots": {"main": {"provider": "mock", "model": "m"}}
}`), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	coord := &coordinator{
		cfg:            cfg,
		sessions:       env.sessions,
		messages:       env.messages,
		permissions:    env.permissions,
		history:        env.history,
		filetracker:    *env.filetracker,
		subagents:      newSubagentRegistry(),
		branches:       newBranchController(),
		subagentRoutes: csync.NewMap[string, subagentRoute](),
	}
	coord.reconcileSubagents()

	// Arm the MCP init gate and never complete it. Resolution builds the
	// tool list from the registry as it stands, so it must not wait.
	mcp.ArmInit()
	t.Cleanup(mcp.DisarmInit)

	agentCfg := cfg.Config().Agents[config.AgentCoder]

	ctx, cancel := context.WithCancel(context.Background())
	coord.currentAgent = coord.buildAgent(agentCfg.ID, false)
	// The caller goes away, mirroring an HTTP handler returning and
	// canceling its request context.
	cancel()
	require.Error(t, ctx.Err(), "sanity check: the building caller's context is dead")

	_, err = coord.resolveAgent(context.Background(), instantiate(t, coord, config.AgentCoder), 0)
	require.NoError(t, err,
		"a later turn must resolve cleanly after the building caller was canceled")
}
