package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
)

// ErrSubSessionNotResumable is returned when a child session cannot be tied
// back to the sub-agent that produced it. There is then no executor to run it
// on and no model or tool set to run it as, and falling back to the default
// agent would continue someone else's conversation under the wrong identity.
var ErrSubSessionNotResumable = errors.New("sub-session cannot be resumed: originating agent is unknown")

// subagentRoute is the executor a child session's traffic lands on, and the
// agent it belongs to. It holds the executor itself rather than resolving it
// from the ID each time: a config reload replaces the registry entry, and an
// ID would then resolve to the new executor while the run being cancelled is
// still on the old one.
//
// It deliberately does not cache a resolvedAgent. The executor holds only run
// state — queues, active requests, accept bookkeeping — and must survive a
// reload; the model, tool set and prompt are config-derived and must not,
// because replacing an entry is the only way revoked tools stop being
// reachable. Each turn resolves its own identity from agentID.
type subagentRoute struct {
	agentID  string
	executor SessionAgent
}

// turnTarget is where a session's turn runs and what it runs as.
type turnTarget struct {
	executor SessionAgent
	resolved resolvedAgent
	resolve  func(context.Context) (resolvedAgent, error)
	// child reports that the turn runs inside a sub-agent's session.
	child bool
}

// registerSubagentRoute records where a child session's later traffic goes.
func (c *coordinator) registerSubagentRoute(sessionID, agentID string, executor SessionAgent) {
	c.subagentRoutes.Set(sessionID, subagentRoute{
		agentID:  agentID,
		executor: executor,
	})
}

// forgetSubagentRoute drops a child session's route. The executor it points
// at is shared with the agent's other sessions and outlives this entry.
func (c *coordinator) forgetSubagentRoute(sessionID string) {
	c.subagentRoutes.Del(sessionID)
}

// executorForSession resolves which executor a synchronous, session-addressed
// call belongs to, consulting only the in-memory index: these callers carry no
// context to rebuild a route with, and a cancel must not wait on the database.
//
// A child session is never currentAgent's to touch even when its route is
// gone, because the two executors would write the same transcript.
func (c *coordinator) executorForSession(sessionID string) (SessionAgent, bool) {
	if !c.sessions.IsAgentToolSession(sessionID) {
		return c.currentAgent, true
	}
	route, ok := c.subagentRoutes.Get(sessionID)
	if !ok {
		return nil, false
	}
	return route.executor, true
}

// acceptExecutorFor resolves the executor an accept reservation belongs to.
// It mirrors executorForSession but is allowed to rebuild a child session's
// route from the database, because it runs on the synchronous send path and
// carries a context to do it with.
//
// Rebuilding here rather than leaving it to the run is what closes the
// cancel window: Cancel resolves through the memory-only executorForSession,
// so a cancel is only ever recorded once the route is in subagentRoutes. A
// persisted child session addressed for the first time after a restart would
// otherwise have no route until the run reached routeFor, and every cancel
// arriving before that point would be dropped.
func (c *coordinator) acceptExecutorFor(ctx context.Context, sessionID string) (SessionAgent, bool) {
	if !c.sessions.IsAgentToolSession(sessionID) {
		return c.currentAgent, true
	}

	// Refresh the dispatch table so an agent dropped from config is not
	// resurrected from a stale entry. The identity a turn runs as is not
	// settled here; every turn resolves its own from the route's agent ID.
	c.reconcileSubagents()

	route, _, err := c.routeFor(ctx, sessionID)
	if err != nil {
		// The run reports this authoritatively a moment later; logging it
		// louder here would duplicate that.
		slog.Debug("Failed to rebuild the sub-session route for an accept",
			"session", sessionID, "error", err)
		return nil, false
	}
	return route.executor, true
}

// turnExecutorFor resolves the executor and agent identity for a new turn.
//
// A child session must land on the sub-agent's own executor: the default path
// resolves an unknown session to coder and runs it on currentAgent, which
// would continue an explore transcript with coder's model and full tool set,
// concurrently with the sub-run that may still be streaming into it.
func (c *coordinator) turnExecutorFor(ctx context.Context, sessionID string) (turnTarget, error) {
	route, routed, err := c.routeFor(ctx, sessionID)
	if err != nil {
		return turnTarget{}, err
	}
	if routed {
		resolve := func(ctx context.Context) (resolvedAgent, error) {
			return c.resolveSubagent(ctx, route.agentID, sessionID)
		}
		resolved, err := resolve(ctx)
		if err != nil {
			return turnTarget{}, err
		}
		return turnTarget{
			executor: route.executor,
			resolved: resolved,
			resolve:  resolve,
			child:    true,
		}, nil
	}

	resolve := func(ctx context.Context) (resolvedAgent, error) {
		active, err := c.activeAgentFor(ctx, sessionID)
		if err != nil {
			return resolvedAgent{}, err
		}
		return c.resolveAgent(ctx, active, 0)
	}
	resolved, err := resolve(ctx)
	if err != nil {
		return turnTarget{}, fmt.Errorf("failed to resolve the agent: %w", err)
	}
	return turnTarget{executor: c.currentAgent, resolved: resolved, resolve: resolve}, nil
}

// resolveSubagent builds the identity a child session's turn runs as, from
// the config as it stands right now.
//
// This is resolved per turn rather than cached on the route because that is
// the only thing that makes a revoked tool actually go away: the registry
// replaces an entry wholesale on a config change precisely so its old tool
// list and prompt are discarded, and a resolution frozen at routing time
// would keep serving them until the process restarts. An agent that has left
// config fails the turn instead of running under its former identity.
//
// The dispatch depth is reconstructed from the parent chain rather than
// hard-coded: a child session created at depth 2 must be resolved at
// depth 2, not depth 1, or it would regain the agent tool and bypass
// the configured subagent_max_depth limit.
func (c *coordinator) resolveSubagent(ctx context.Context, agentID string, sessionID string) (resolvedAgent, error) {
	active, ok := c.cfg.Config().InstantiateAgent(agentID)
	if !ok {
		return resolvedAgent{}, fmt.Errorf("%w: agent %q is no longer configured", ErrSubSessionNotResumable, agentID)
	}
	depth := c.dispatchDepth(ctx, sessionID)
	return c.resolveAgent(ctx, active, depth)
}

// dispatchDepth counts how many parent_session_id hops separate sessionID
// from the root. A top-level session has depth 0; a direct child has 1.
func (c *coordinator) dispatchDepth(ctx context.Context, sessionID string) int {
	depth := 0
	for {
		sess, err := c.sessions.Get(ctx, sessionID)
		if err != nil || sess.ParentSessionID == "" {
			return depth
		}
		depth++
		sessionID = sess.ParentSessionID
	}
}

// routeFor looks up a child session's route, rebuilding it from the agent
// recorded on the session row when this process did not dispatch the original
// sub-run. The second result reports whether sessionID is a child session at
// all; a false with no error means the caller should take the normal path.
func (c *coordinator) routeFor(ctx context.Context, sessionID string) (subagentRoute, bool, error) {
	if !c.sessions.IsAgentToolSession(sessionID) {
		return subagentRoute{}, false, nil
	}
	if route, ok := c.subagentRoutes.Get(sessionID); ok {
		return route, true, nil
	}

	sess, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return subagentRoute{}, false, fmt.Errorf("%w: %w", ErrSubSessionNotResumable, err)
	}
	if sess.Agent == "" {
		return subagentRoute{}, false, fmt.Errorf("%w: session %q records no agent", ErrSubSessionNotResumable, sessionID)
	}
	entry, ok := c.subagents.Get(sess.Agent)
	if !ok {
		return subagentRoute{}, false, fmt.Errorf("%w: agent %q is no longer configured", ErrSubSessionNotResumable, sess.Agent)
	}

	executor := c.subagentExecutor(entry)
	c.registerSubagentRoute(sessionID, sess.Agent, executor)
	return subagentRoute{agentID: sess.Agent, executor: executor}, true, nil
}

// eachExecutor calls fn once per distinct executor, currentAgent included.
// Child sessions of the same sub-agent share one executor, so the routing
// index yields duplicates.
func (c *coordinator) eachExecutor(fn func(SessionAgent)) {
	fn(c.currentAgent)
	seen := map[SessionAgent]struct{}{c.currentAgent: {}}
	for route := range c.subagentRoutes.Seq() {
		if _, dup := seen[route.executor]; dup {
			continue
		}
		seen[route.executor] = struct{}{}
		fn(route.executor)
	}
}

// rollingUpCost wraps a turn so its cost reaches the top-level session.
//
// Stats only count top-level sessions, and each sub-run rolls its child's cost
// up one level as it finishes. A turn the user starts inside a child session
// has no outer sub-run to do that, so it would vanish from the totals.
//
// The baseline is read inside the returned closure — right before the
// turn runs — so that queued turns each read the cost their predecessor
// left. Reading it outside would let two queued turns share a baseline
// and double-count the first one's cost.
func (c *coordinator) rollingUpCost(ctx context.Context, sessionID string, turn func() (*fantasy.AgentResult, error)) func() (*fantasy.AgentResult, error) {
	return func() (*fantasy.AgentResult, error) {
		before, err := c.sessions.Get(ctx, sessionID)
		if err != nil {
			slog.Warn(
				"Failed to read the sub-session cost before the turn; the turn will not be billed to its parents",
				"session", sessionID,
				"error", err,
			)
			return turn()
		}
		result, runErr := turn()
		after, err := c.sessions.Get(ctx, sessionID)
		if err != nil {
			slog.Warn("Failed to read the sub-session cost after the turn", "session", sessionID, "error", err)
			return result, runErr
		}
		if delta := after.Cost - before.Cost; delta > 0 {
			if err := c.addCostToAncestors(ctx, sessionID, delta); err != nil {
				slog.Warn("Failed to roll the sub-session cost up to its parents", "session", sessionID, "error", err)
			}
		}
		return result, runErr
	}
}

// addCostToAncestors adds delta to every session above sessionID.
//
// Walking the whole chain is only correct for a cost no sub-run will account
// for. A finishing sub-run rolls up exactly one level, and nested runs close
// the chain by each doing so in turn; adding a full walk there would double
// count every level above the first.
//
// Each level is added independently through AddCost, because sibling
// sub-sessions reach a shared ancestor concurrently.
func (c *coordinator) addCostToAncestors(ctx context.Context, sessionID string, delta float64) error {
	for {
		sess, err := c.sessions.Get(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("get session %q: %w", sessionID, err)
		}
		if sess.ParentSessionID == "" {
			return nil
		}
		if err := c.sessions.AddCost(ctx, sess.ParentSessionID, delta); err != nil {
			return fmt.Errorf("add cost to parent session %q: %w", sess.ParentSessionID, err)
		}
		sessionID = sess.ParentSessionID
	}
}
