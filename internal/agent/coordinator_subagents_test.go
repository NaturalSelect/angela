package agent

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// toolNames lists the tools a built agent ended up with, so tests can
// assert on the permission set that actually reached it rather than on
// the config it was supposed to come from.
func toolNames(a SessionAgent) []string {
	sa, ok := a.(*sessionAgent)
	if !ok {
		return nil
	}
	var names []string
	for _, tool := range sa.tools.Copy() {
		names = append(names, tool.Info().Name)
	}
	return names
}

// TestUpdateModelsReconcilesSubagentsAfterConfigChange pins the
// hot-reload fix. Subagents used to be built once and never rebuilt, so
// a config reload that revoked a tool left the cached agent running with
// the tool still in its list — the user's edit appeared to take effect
// and did not. Narrowing an agent's whitelist must replace its entry.
func TestUpdateModelsReconcilesSubagentsAfterConfigChange(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())

	before, ok := coord.subagents.Get(config.AgentGeneral)
	require.True(t, ok)

	// Force the entry to be built so a stale agent would be observable.
	_, err := before.resolve(func(cfg config.Agent) (SessionAgent, error) {
		return coord.buildSubAgentSync(context.Background(), cfg)
	})
	require.NoError(t, err)
	require.Contains(t, toolNames(before.agent), "bash", "sanity check: general starts with bash")

	agents := coord.cfg.Config().Agents
	narrowed := agents[config.AgentGeneral]
	narrowed.AllowedTools = &config.AllowedToolSet{Kind: config.ToolSetScope, Tools: []string{"view"}}
	agents[config.AgentGeneral] = narrowed

	require.NoError(t, coord.UpdateModels(context.Background()))

	after, ok := coord.subagents.Get(config.AgentGeneral)
	require.True(t, ok)
	require.NotSame(t, before, after, "a permission change must replace the cached entry")

	rebuilt, err := after.resolve(func(cfg config.Agent) (SessionAgent, error) {
		return coord.buildSubAgentSync(context.Background(), cfg)
	})
	require.NoError(t, err)
	require.NotContains(t, toolNames(rebuilt), "bash",
		"a revoked tool must be gone from the rebuilt subagent")
}

// TestUpdateModelsKeepsUnchangedSubagents is the other half of
// reconciliation: rebuilding indiscriminately would re-parse prompt
// templates and reconstruct providers on the run hot path.
func TestUpdateModelsKeepsUnchangedSubagents(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())

	before := map[string]*subagentEntry{}
	for _, id := range coord.subagents.IDs() {
		e, ok := coord.subagents.Get(id)
		require.True(t, ok)
		before[id] = e
	}
	require.NotEmpty(t, before)

	require.NoError(t, coord.UpdateModels(context.Background()))
	require.NoError(t, coord.UpdateModels(context.Background()))

	require.Len(t, before, coord.subagents.Len())
	for id, entry := range before {
		after, ok := coord.subagents.Get(id)
		require.Truef(t, ok, "subagent %q must still be present", id)
		require.Samef(t, entry, after, "unchanged subagent %q must not be replaced", id)
	}
}

// TestUpdateModelsDropsDisabledSubagent covers the removal direction:
// an agent the user disables at runtime must stop being dispatchable.
func TestUpdateModelsDropsDisabledSubagent(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())

	_, ok := coord.subagents.Get(config.AgentExplore)
	require.True(t, ok, "sanity check: explore starts dispatchable")

	delete(coord.cfg.Config().Agents, config.AgentExplore)
	require.NoError(t, coord.UpdateModels(context.Background()))

	_, ok = coord.subagents.Get(config.AgentExplore)
	require.False(t, ok, "a disabled agent must leave the dispatch table")
}

// TestDispatchIsolatesExecuteTimeTemplateError pins the blast radius of
// a broken subagent. Its prompt parses, so the failure only surfaces
// when the template is executed — which now happens on its own dispatch
// path. It must fail that one call and leave the coordinator's readiness
// group and every other subagent untouched.
func TestDispatchIsolatesExecuteTimeTemplateError(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())

	coord.cfg.Config().Agents["broken"] = config.Agent{
		ID:           "broken",
		Name:         "Broken",
		Description:  "parses fine, fails at execute time",
		Mode:         config.AgentModeSubagent,
		Prompt:       "{{.NoSuchField}}",
		AllowedTools: &config.AllowedToolSet{Kind: config.ToolSetScope, Tools: []string{"view"}},
		AllowedMCP:   &config.AllowedMCPSet{Kind: config.ToolSetScope},
	}
	coord.reconcileSubagents()

	broken, ok := coord.subagents.Get("broken")
	require.True(t, ok, "a broken agent is still registered; it fails on dispatch, not on reconcile")

	_, err := broken.resolve(func(cfg config.Agent) (SessionAgent, error) {
		return coord.buildSubAgentSync(context.Background(), cfg)
	})
	require.Error(t, err, "an execute-time template error must surface to its own dispatch")

	// The cached failure is returned again rather than retried.
	_, again := broken.resolve(func(cfg config.Agent) (SessionAgent, error) {
		t.Fatal("a failed subagent must not be rebuilt on the next dispatch")
		return nil, nil
	})
	require.Equal(t, err, again)

	require.NoError(t, coord.readyWg.Wait(),
		"a broken subagent must not poison the coordinator's readiness group")

	healthy, ok := coord.subagents.Get(config.AgentTask)
	require.True(t, ok)
	_, err = healthy.resolve(func(cfg config.Agent) (SessionAgent, error) {
		return coord.buildSubAgentSync(context.Background(), cfg)
	})
	require.NoError(t, err, "an unrelated subagent must still build")
}

// TestReconcileReplacesEntriesWhenHooksChange pins the second staleness
// axis: hooks are baked into a built agent's tool wrappers, so adding a
// PreToolUse hook at runtime has to invalidate cached subagents too.
func TestReconcileReplacesEntriesWhenHooksChange(t *testing.T) {
	t.Parallel()

	reg := newSubagentRegistry()
	agents := map[string]config.Agent{
		"reviewer": {ID: "reviewer", Mode: config.AgentModeSubagent},
	}

	reg.Reconcile(agents, nil)
	before, ok := reg.Get("reviewer")
	require.True(t, ok)

	reg.Reconcile(agents, nil)
	unchanged, _ := reg.Get("reviewer")
	require.Same(t, before, unchanged, "an unchanged hook set must not churn entries")

	reg.Reconcile(agents, []config.HookConfig{{Command: "exit 2"}})
	after, _ := reg.Get("reviewer")
	require.NotSame(t, before, after, "a new hook must invalidate cached subagents")
}

func TestReconcileExcludesPrimaryAgents(t *testing.T) {
	t.Parallel()

	reg := newSubagentRegistry()
	reg.Reconcile(map[string]config.Agent{
		config.AgentCoder: {ID: config.AgentCoder, Mode: config.AgentModePrimary},
		"reviewer":        {ID: "reviewer", Mode: config.AgentModeSubagent},
	}, nil)

	require.Equal(t, []string{"reviewer"}, reg.IDs())

	// An agent promoted to primary must leave the table, not linger.
	reg.Reconcile(map[string]config.Agent{
		"reviewer": {ID: "reviewer", Mode: config.AgentModePrimary},
	}, nil)
	require.Empty(t, reg.IDs())
}
