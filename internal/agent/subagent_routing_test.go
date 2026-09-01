package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routingFixture is a coordinator holding one parent session and one child
// session produced by a sub-agent dispatch, with distinct executors for each
// so a misrouted call is visible.
type routingFixture struct {
	coord    *coordinator
	main     *mockSessionAgent
	child    *mockSessionAgent
	parentID string
	childID  string
}

func newRoutingFixture(t *testing.T) routingFixture {
	t.Helper()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})

	main := newMockSessionAgent(t, "coder", nil)
	coord.currentAgent = main

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	childID := env.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	_, err = env.sessions.CreateTaskSession(t.Context(), childID, parent.ID, "explore run")
	require.NoError(t, err)

	child, _ := newMockAgent(t, providerID, 4096, nil)
	child.agentID = "explore"

	return routingFixture{
		coord: coord, main: main, child: child,
		parentID: parent.ID, childID: childID,
	}
}

func (f routingFixture) register() {
	f.coord.registerSubagentRoute(f.childID, "explore", f.child)
}

// Cancelling a child session must reach the executor actually running it.
// currentAgent never ran that session, so addressing it there is a silent
// no-op — which is what happened before routing existed.
func TestCancelReachesTheSubAgentExecutor(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.register()

	f.coord.Cancel(f.childID)

	require.Equal(t, []string{f.childID}, f.child.cancelled)
	require.Empty(t, f.main.cancelled, "the main agent was asked to cancel a session it never ran")
}

func TestCancelOnANormalSessionStaysOnTheMainAgent(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.register()

	f.coord.Cancel(f.parentID)

	require.Equal(t, []string{f.parentID}, f.main.cancelled)
	require.Empty(t, f.child.cancelled)
}

// A sub-agent streaming into its session must read as busy, or the UI shows
// idle while output is arriving and Esc appears to do nothing.
func TestIsSessionBusyConsultsTheSubAgentExecutor(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.register()
	f.child.busy = true

	require.True(t, f.coord.IsSessionBusy(f.childID))
	require.False(t, f.coord.IsSessionBusy(f.parentID),
		"busyness leaked from the sub-agent to the parent session")
}

func TestQueueOperationsRouteToTheSubAgentExecutor(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.register()
	f.child.queued = []string{"follow up"}

	require.Equal(t, 1, f.coord.QueuedPrompts(f.childID))
	require.Equal(t, []string{"follow up"}, f.coord.QueuedPromptsList(f.childID))
	require.Equal(t, 0, f.coord.QueuedPrompts(f.parentID))

	f.coord.ClearQueue(f.childID)
	require.Equal(t, []string{f.childID}, f.child.cleared)
	require.Empty(t, f.main.cleared)
}

// An unrouted child session belongs to no executor in this process. It must
// not fall through to currentAgent, which would then write the same
// transcript as the sub-run that produced it. This fixture's child session
// records no agent, so its route cannot be rebuilt either.
func TestUnroutedChildSessionIsNeverHandledByTheMainAgent(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)

	f.coord.Cancel(f.childID)
	f.coord.ClearQueue(f.childID)

	require.Empty(t, f.main.cancelled)
	require.Empty(t, f.main.cleared)
	require.False(t, f.coord.IsSessionBusy(f.childID))
	require.Equal(t, 0, f.coord.QueuedPrompts(f.childID))
	require.Nil(t, f.coord.QueuedPromptsList(f.childID))
	require.Nil(t, f.coord.BeginAccepted(t.Context(), f.childID))
}

// persistedChildSession creates the state a child session dispatched by an
// earlier process would leave behind: a session row naming the agent that
// produced it, and nothing at all in the in-memory route index.
func persistedChildSession(t *testing.T, coord *coordinator, agentID string) string {
	t.Helper()

	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	childID := coord.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	_, err = coord.sessions.CreateTaskSession(t.Context(), childID, parent.ID, "explore run")
	require.NoError(t, err)

	// Agent is a projection of ActiveAgent, so this is the only way to
	// record it — the same one the original dispatch used.
	require.NoError(t, coord.sessions.UpdateActiveAgent(t.Context(), childID,
		config.ActiveAgentState{Agent: agentID}))

	stored, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, agentID, stored.Agent, "test premise: the session must name its agent")

	_, routed := coord.subagentRoutes.Get(childID)
	require.False(t, routed, "test premise: the route must not already be in memory")

	return childID
}

// Accepting a prompt for a child session that outlived the process which
// dispatched it must rebuild that session's route, not just fail to reserve.
// Cancel resolves through the memory-only index, so until the route is there
// every cancel is a silent no-op — which is exactly the window between
// accepting the prompt and the run reaching routeFor.
func TestBeginAcceptedRebuildsAPersistedChildRoute(t *testing.T) {
	t.Parallel()
	coord := newGateTestCoordinator(t, false)
	childID := persistedChildSession(t, coord, config.AgentExplore)

	accept := coord.BeginAccepted(t.Context(), childID)
	require.NotNil(t, accept, "a persisted child session was dispatched with no accept reservation")

	route, routed := coord.subagentRoutes.Get(childID)
	require.True(t, routed, "the index Cancel resolves through was not rebuilt")
	require.Equal(t, config.AgentExplore, route.agentID)
	require.NotSame(t, coord.currentAgent, route.executor,
		"the child session fell through to the main agent")

	// The cancel lands in the window this closes: after the prompt was
	// accepted, before the run registers itself in activeRequests.
	coord.Cancel(childID)

	executor, ok := route.executor.(*sessionAgent)
	require.True(t, ok)
	dispatchLock := executor.sessionMu(childID)
	dispatchLock.Lock()
	covered := executor.canceledBySeq(childID, accept.seq)
	dispatchLock.Unlock()
	require.True(t, covered, "a cancel arriving right after the accept was dropped")
}

// A child session whose route cannot be rebuilt has no executor to reserve
// on. Accepting reports that by returning nil; the run that follows is what
// reports why.
func TestBeginAcceptedOnAnUnresumableChildSessionReservesNothing(t *testing.T) {
	t.Parallel()
	coord := newGateTestCoordinator(t, false)
	childID := persistedChildSession(t, coord, "an-agent-that-was-deleted")

	require.Nil(t, coord.BeginAccepted(t.Context(), childID))

	_, routed := coord.subagentRoutes.Get(childID)
	require.False(t, routed, "a route was cached for a session that cannot be resumed")
}

// Accepting a prompt for an ordinary session must stay on the main agent and
// must not consult the route index at all.
func TestBeginAcceptedOnANormalSessionStaysOnTheMainAgent(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)

	accept := f.coord.BeginAccepted(t.Context(), f.parentID)
	require.NotNil(t, accept)
	require.Equal(t, f.parentID, accept.SessionID())
}

// Quitting has to stop sub-agents too, and each executor should be told once
// however many of its sessions are routed to it.
func TestCancelAllReachesEveryExecutorExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.register()

	second := f.coord.sessions.CreateAgentToolSessionID("msg-1", "call-2")
	f.coord.registerSubagentRoute(second, "explore", f.child)

	f.coord.CancelAll()

	require.Equal(t, 1, f.main.cancelAll)
	require.Equal(t, 1, f.child.cancelAll,
		"one executor serving two child sessions was cancelled twice")
}

func TestIsBusyIncludesSubAgents(t *testing.T) {
	t.Parallel()
	f := newRoutingFixture(t)
	f.register()

	require.False(t, f.coord.IsBusy())

	f.child.busy = true
	require.True(t, f.coord.IsBusy(), "a running sub-agent left the coordinator reading as idle")
}

// A turn on a child session must run as that sub-agent. The default path
// resolves an unknown session to coder, which would continue an explore
// transcript with coder's model and full tool set.
func TestTurnOnAChildSessionRunsAsTheSubAgent(t *testing.T) {
	t.Parallel()
	coord := newGateTestCoordinator(t, false)
	childID := persistedChildSession(t, coord, config.AgentExplore)

	target, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)

	require.True(t, target.child)
	require.Equal(t, config.AgentExplore, target.resolved.ID)
	require.NotSame(t, coord.currentAgent, target.executor,
		"the child session's turn landed on the main agent")

	route, routed := coord.subagentRoutes.Get(childID)
	require.True(t, routed)
	require.True(t, route.executor == target.executor,
		"the turn ran on a different executor than the one cancellation addresses")
}

// A cached route must not freeze the identity the session was first resolved
// with. Replacing a registry entry on a config change is the only way a
// revoked tool stops being reachable, so a turn reusing a frozen resolution
// would keep serving it until the process restarts.
func TestTurnOnAChildSessionResolvesAgainstCurrentConfig(t *testing.T) {
	t.Parallel()

	t.Run("a narrowed tool set takes effect on the next turn", func(t *testing.T) {
		t.Parallel()
		coord := newGateTestCoordinator(t, false)
		childID := persistedChildSession(t, coord, config.AgentExplore)

		before, err := coord.turnExecutorFor(t.Context(), childID)
		require.NoError(t, err)
		require.NotEmpty(t, before.resolved.Tools, "test premise: explore starts with tools")

		cfg := coord.cfg.Config()
		narrowed := cfg.Agents[config.AgentExplore]
		narrowed.AllowedTools = &config.AllowedToolSet{Kind: config.ToolSetScope}
		cfg.Agents[config.AgentExplore] = narrowed
		coord.reconcileSubagents()

		after, err := coord.turnExecutorFor(t.Context(), childID)
		require.NoError(t, err)
		require.Less(t, len(after.resolved.Tools), len(before.resolved.Tools),
			"the turn kept the tool set its route was first resolved with")
	})

	t.Run("an agent dropped from config fails the turn", func(t *testing.T) {
		t.Parallel()
		coord := newGateTestCoordinator(t, false)
		childID := persistedChildSession(t, coord, config.AgentExplore)

		_, err := coord.turnExecutorFor(t.Context(), childID)
		require.NoError(t, err)

		delete(coord.cfg.Config().Agents, config.AgentExplore)
		coord.reconcileSubagents()

		_, err = coord.turnExecutorFor(t.Context(), childID)
		require.ErrorIs(t, err, ErrSubSessionNotResumable,
			"a cached route kept answering for an agent that left the config")
	})
}

// A deleted session must not leave its route behind: the entry pins an
// executor for a transcript that no longer exists, and nothing else ever
// removes it, so a long-lived process accumulates them.
func TestDeletingASessionForgetsItsRoute(t *testing.T) {
	f := newRoutingFixture(t)
	f.register()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go f.coord.forgetDeletedSessions(ctx, f.coord.sessions.Subscribe(ctx))

	_, routed := f.coord.subagentRoutes.Get(f.childID)
	require.True(t, routed, "precondition: the route is registered")

	require.NoError(t, f.coord.sessions.Delete(t.Context(), f.childID))

	require.Eventually(t, func() bool {
		_, routed := f.coord.subagentRoutes.Get(f.childID)
		return !routed
	}, 2*time.Second, 5*time.Millisecond,
		"a deleted child session kept its route")
}

// A child session this process never dispatched cannot be resumed: there is
// no executor for it and no identity to run it as. Falling back to a default
// agent would answer as the wrong agent on someone else's transcript.
func TestTurnOnAnUnresumableChildSessionFails(t *testing.T) {
	t.Parallel()

	t.Run("session records no agent", func(t *testing.T) {
		t.Parallel()
		f := newRoutingFixture(t)

		_, err := f.coord.turnExecutorFor(t.Context(), f.childID)
		require.ErrorIs(t, err, ErrSubSessionNotResumable)
	})

	t.Run("agent is no longer configured", func(t *testing.T) {
		t.Parallel()
		f := newRoutingFixture(t)

		sess, err := f.coord.sessions.Get(t.Context(), f.childID)
		require.NoError(t, err)
		sess.Agent = "an-agent-that-was-deleted"
		_, err = f.coord.sessions.Save(t.Context(), sess)
		require.NoError(t, err)

		_, err = f.coord.turnExecutorFor(t.Context(), f.childID)
		require.ErrorIs(t, err, ErrSubSessionNotResumable)
	})
}

// Dispatching a sub-agent is what makes its session addressable afterwards.
func TestRunSubAgentRegistersItsRoute(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	coord.currentAgent = newMockSessionAgent(t, "coder", nil)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	child, resolved := newMockAgent(t, providerID, 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("done"), nil
	})
	resolved.ID = "explore"

	_, err = coord.runSubAgent(t.Context(), subAgentParams{
		Agent:          child,
		Resolved:       resolved,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do something",
		SessionTitle:   "explore run",
	})
	require.NoError(t, err)

	childID := env.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	coord.Cancel(childID)
	require.Equal(t, []string{childID}, child.cancelled,
		"the dispatched sub-agent's session was not addressable after the run")
}

// A route that only lives in memory dies with the process. Recording the
// sub-agent on the session row is what lets a later process resume it.
func TestRunSubAgentRecordsItsAgentOnTheSession(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	coord.currentAgent = newMockSessionAgent(t, "coder", nil)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	child, resolved := newMockAgent(t, providerID, 4096, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("done"), nil
	})
	resolved.ID = "explore"
	resolved.Host = config.ActiveAgent{Agent: config.Agent{ID: "explore"}}

	_, err = coord.runSubAgent(t.Context(), subAgentParams{
		Agent:          child,
		Resolved:       resolved,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do something",
		SessionTitle:   "explore run",
	})
	require.NoError(t, err)

	childID := env.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	stored, err := env.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "explore", stored.Agent,
		"the child session does not say which agent produced it, so it cannot be resumed")
}

// Cost has to climb the whole chain, one level at a time. Stats count only
// top-level sessions, so a grandchild's turn that stops at its parent is a
// turn nobody is billed for.
func TestCostRollsUpEveryAncestor(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})

	root, err := env.sessions.Create(t.Context(), "Root")
	require.NoError(t, err)

	childID := env.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	_, err = env.sessions.CreateTaskSession(t.Context(), childID, root.ID, "child")
	require.NoError(t, err)

	grandchildID := env.sessions.CreateAgentToolSessionID("msg-2", "call-2")
	_, err = env.sessions.CreateTaskSession(t.Context(), grandchildID, childID, "grandchild")
	require.NoError(t, err)

	require.NoError(t, coord.addCostToAncestors(t.Context(), grandchildID, 0.25))

	child, err := env.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.InDelta(t, 0.25, child.Cost, 1e-9)

	updatedRoot, err := env.sessions.Get(t.Context(), root.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.25, updatedRoot.Cost, 1e-9, "the cost stopped short of the top-level session")

	grandchild, err := env.sessions.Get(t.Context(), grandchildID)
	require.NoError(t, err)
	require.InDelta(t, 0, grandchild.Cost, 1e-9, "the originating session was billed twice")
}

// A top-level session has nobody to roll up to; walking must simply stop.
func TestCostRollUpStopsAtATopLevelSession(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})

	root, err := env.sessions.Create(t.Context(), "Root")
	require.NoError(t, err)

	require.NoError(t, coord.addCostToAncestors(t.Context(), root.ID, 0.5))

	updated, err := env.sessions.Get(t.Context(), root.ID)
	require.NoError(t, err)
	require.InDelta(t, 0, updated.Cost, 1e-9)
}

// Sibling sub-sessions under one parent finish at the same time, and a
// nested chain adds a second writer per level. Each level is added
// independently, so no roll-up may swallow another's amount.
func TestConcurrentSiblingRollUpsKeepEveryCost(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})

	root, err := env.sessions.Create(t.Context(), "Root")
	require.NoError(t, err)

	const siblings = 8
	var wg sync.WaitGroup
	for i := range siblings {
		childID := env.sessions.CreateAgentToolSessionID("msg-1", fmt.Sprintf("call-%d", i))
		_, err := env.sessions.CreateTaskSession(t.Context(), childID, root.ID, "child")
		require.NoError(t, err)

		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, coord.addCostToAncestors(t.Context(), childID, 0.01))
		}()
	}
	wg.Wait()

	updated, err := env.sessions.Get(t.Context(), root.ID)
	require.NoError(t, err)
	require.InDelta(t, siblings*0.01, updated.Cost, 1e-9,
		"concurrent sibling roll-ups overwrote each other on the shared parent")
}

// updateParentSessionCost is the one-level roll-up every finishing sub-run
// performs, and it lands on the same shared parent as its siblings.
func TestConcurrentParentCostUpdatesKeepEveryCost(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	const siblings = 8
	childIDs := make([]string, 0, siblings)
	for i := range siblings {
		childID := env.sessions.CreateAgentToolSessionID("msg-1", fmt.Sprintf("call-%d", i))
		child, err := env.sessions.CreateTaskSession(t.Context(), childID, parent.ID, "child")
		require.NoError(t, err)
		child.Cost = 0.01
		_, err = env.sessions.Save(t.Context(), child)
		require.NoError(t, err)
		childIDs = append(childIDs, child.ID)
	}

	var wg sync.WaitGroup
	for _, childID := range childIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, coord.updateParentSessionCost(t.Context(), childID, parent.ID))
		}()
	}
	wg.Wait()

	updated, err := env.sessions.Get(t.Context(), parent.ID)
	require.NoError(t, err)
	require.InDelta(t, siblings*0.01, updated.Cost, 1e-9,
		"concurrent sub-run roll-ups overwrote each other on the shared parent")
}

// A grandchild session lives at dispatch depth 2. With
// subagent_max_depth = 2, depths 0 and 1 may delegate but depth 2 may
// not — so the grandchild must not receive the agent tool. This is the
// discriminating budget: the old code hard-coded depth 1, which is
// under the limit and therefore wrongly granted delegation.
func TestResumedGrandchildRespectsDepthBudget(t *testing.T) {
	t.Parallel()
	coord := newGateTestCoordinator(t, false)
	coord.cfg.Config().Options.SubagentDepth = ptrTo(2)

	agentToolNames := func(sessionID string) []string {
		target, err := coord.turnExecutorFor(t.Context(), sessionID)
		require.NoError(t, err)
		var names []string
		for _, tool := range target.resolved.Tools {
			names = append(names, tool.Info().Name)
		}
		return names
	}

	newChild := func(parentID, msgID, callID string) string {
		id := coord.sessions.CreateAgentToolSessionID(msgID, callID)
		_, err := coord.sessions.CreateTaskSession(t.Context(), id, parentID, "run")
		require.NoError(t, err)
		require.NoError(t, coord.sessions.UpdateActiveAgent(t.Context(), id,
			config.ActiveAgentState{Agent: config.AgentGeneral}))
		return id
	}

	root, err := coord.sessions.Create(t.Context(), "Root")
	require.NoError(t, err)
	childID := newChild(root.ID, "msg-1", "call-1")
	grandchildID := newChild(childID, "msg-2", "call-2")

	require.Contains(t, agentToolNames(childID), toolnames.Agent,
		"test premise: at depth 1 the budget still permits delegation")

	require.NotContains(t, agentToolNames(grandchildID), toolnames.Agent,
		"a grandchild at depth 2 regained the agent tool despite subagent_max_depth=2")
}
