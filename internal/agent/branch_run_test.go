package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

const branchProviderID = "test-provider"

// branchAgent returns a mock whose run blocks until released, which is what
// a real branch does: its opening turn ends, and the session then sits
// waiting for the user rather than for anything this dispatch controls.
func idleBranchAgent(t *testing.T, seen chan<- string) (SessionAgent, resolvedAgent) {
	t.Helper()
	return newMockAgent(branchProviderID, 4096, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		if seen != nil {
			seen <- call.Prompt
		}
		return agentResultWithText("hello"), nil
	})
}

func branchCoordinator(t *testing.T, env fakeEnv) *coordinator {
	t.Helper()
	c := newTestCoordinator(t, env, branchProviderID, config.ProviderConfig{ID: branchProviderID})
	c.interactive = true
	return c
}

func branchParams(agent SessionAgent, resolved resolvedAgent, parentID, agentMessageID string) subAgentParams {
	return subAgentParams{
		Agent:          agent,
		Resolved:       resolved,
		SessionID:      parentID,
		AgentMessageID: agentMessageID,
		ToolCallID:     "call-1",
		Prompt:         "decide with me which sessions to invalidate",
		SessionTitle:   "Pairing",
	}
}

// TestRunBranchAgentForkBoundary pins what the branch inherits. It must see
// the conversation as it stood when the fork was requested, and nothing
// after: the forking message carries a tool call the branch will never
// answer, and a sibling's result belongs to a call it never made.
func TestRunBranchAgentForkBoundary(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)

	parent, err := env.sessions.Create(t.Context(), "Refactor the auth layer")
	require.NoError(t, err)

	first, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.User})
	require.NoError(t, err)
	second, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	// The forking message. Its parts stay empty on purpose: a tool call
	// reaches the row through a debounced update, so at the moment the
	// tool runs the row often holds nothing. Locating the fork point by
	// ID rather than by content is what makes that harmless.
	forking, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	// A parallel sibling's result, landing after the fork point.
	sibling, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Tool})
	require.NoError(t, err)

	seen := make(chan string, 1)
	agent, resolved := idleBranchAgent(t, seen)

	go func() {
		_, _ = c.runBranchAgent(t.Context(), branchParams(agent, resolved, parent.ID, forking.ID))
	}()

	prompt := requireBranchStarted(t, seen)
	require.Contains(t, prompt, "decide with me which sessions to invalidate")
	require.Contains(t, prompt, "Refactor the auth layer",
		"the fork prompt must name the conversation it came from")

	branchID := requireBranchSession(t, c, parent.ID)
	msgs, err := env.messages.List(t.Context(), branchID)
	require.NoError(t, err)

	// Only the two messages that preceded the fork. The opening turn's own
	// message is absent because the agent here is a mock and never writes
	// one; what it was asked is asserted through the prompt above.
	require.Len(t, msgs, 2)
	for _, m := range msgs {
		require.NotEqual(t, forking.ID, m.ID, "the forking message must not be copied")
		require.NotEqual(t, sibling.ID, m.ID, "a message after the fork point must not be copied")
		require.NotEqual(t, first.ID, m.ID, "copied messages must get fresh IDs")
		require.NotEqual(t, second.ID, m.ID, "copied messages must get fresh IDs")
	}
	require.Equal(t, message.User, msgs[0].Role)
	require.Equal(t, message.Assistant, msgs[1].Role)
}

func requireBranchStarted(t *testing.T, seen <-chan string) string {
	t.Helper()
	select {
	case prompt := <-seen:
		return prompt
	case <-time.After(5 * time.Second):
		t.Fatal("the branch never ran its opening turn")
		return ""
	}
}

func requireBranchSession(t *testing.T, c *coordinator, parentID string) string {
	t.Helper()
	return requireBranchSessions(t, c, parentID, 1)[0]
}

// requireBranchSessions waits until the parent has exactly want branches
// registered and returns their IDs. Registration happens after a session
// create and a transcript fork, so a dispatch is visible here well after
// its goroutine started.
func requireBranchSessions(t *testing.T, c *coordinator, parentID string, want int) []string {
	t.Helper()
	for range 100 {
		var ids []string
		for id, w := range c.branches.waiters.Seq2() {
			if w.parentSessionID == parentID {
				ids = append(ids, id)
			}
		}
		if len(ids) >= want {
			return ids
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the parent never registered %d branches", want)
	return nil
}

// Forking several branches at once is what lets the user hold alternative
// directions apart and resolve each on its own, so both dispatches have to
// land: one dropped for having lost the race would leave the model suspended
// on a call nothing can resolve, since the branch it waits on never existed.
func TestParallelBranchDispatchesBothLand(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	forking, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	seen := make(chan string, 2)
	agent, resolved := idleBranchAgent(t, seen)

	responses := make(chan fantasy.ToolResponse, 2)
	start := make(chan struct{})
	for _, callID := range []string{"call-1", "call-2"} {
		go func() {
			<-start
			if refusal := c.branchDispatchRefusal(t.Context(), parent.ID); refusal != "" {
				t.Errorf("a parallel branch dispatch was refused: %s", refusal)
				return
			}
			p := branchParams(agent, resolved, parent.ID, forking.ID)
			p.ToolCallID = callID
			resp, err := c.runBranchAgent(t.Context(), p)
			if err != nil {
				t.Errorf("branch dispatch failed: %v", err)
				return
			}
			responses <- resp
		}()
	}
	close(start)

	requireBranchStarted(t, seen)
	requireBranchStarted(t, seen)

	branches := requireBranchSessions(t, c, parent.ID, 2)
	require.Len(t, branches, 2, "each call must fork its own session")

	// Resolved separately, because each suspended call is waiting on its
	// own rendezvous: a summary must reach the dispatch that forked it
	// rather than whichever one happens to be listening.
	want := make([]string, 0, 2)
	for _, id := range branches {
		payload := "summary of " + id
		want = append(want, payload)
		require.True(t, c.branches.Signal(id, branchOutcome{Merged: true, Payload: payload}))
	}

	got := []string{
		requireBranchResponse(t, responses).Content,
		requireBranchResponse(t, responses).Content,
	}
	require.ElementsMatch(t, want, got)
}

func requireBranchResponse(t *testing.T, responses <-chan fantasy.ToolResponse) fantasy.ToolResponse {
	t.Helper()
	select {
	case resp := <-responses:
		return resp
	case <-time.After(5 * time.Second):
		t.Fatal("a merged branch never resolved its dispatch")
		return fantasy.ToolResponse{}
	}
}

// A merge is what the suspended call has been waiting for, and the summary
// crosses back verbatim.
func TestRunBranchAgentReturnsTheMergedSummary(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	forking, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	seen := make(chan string, 1)
	agent, resolved := idleBranchAgent(t, seen)

	var resp fantasy.ToolResponse
	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, runErr = c.runBranchAgent(t.Context(), branchParams(agent, resolved, parent.ID, forking.ID))
	}()

	requireBranchStarted(t, seen)
	branchID := requireBranchSession(t, c, parent.ID)
	require.True(t, c.branches.Signal(branchID, branchOutcome{Merged: true, Payload: "invalidate all but the current"}))

	wg.Wait()
	require.NoError(t, runErr)
	require.False(t, resp.IsError)
	require.Equal(t, "invalidate all but the current", resp.Content)
}

// Abandoning is not merging: the caller has to be able to tell that no
// result was approved, and its turn ends rather than looping on a branch
// that no longer exists.
func TestRunBranchAgentReportsAbandonment(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	forking, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	seen := make(chan string, 1)
	agent, resolved := idleBranchAgent(t, seen)

	var resp fantasy.ToolResponse
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, _ = c.runBranchAgent(t.Context(), branchParams(agent, resolved, parent.ID, forking.ID))
	}()

	requireBranchStarted(t, seen)
	branchID := requireBranchSession(t, c, parent.ID)

	// Through Cancel rather than Signal directly, so this covers the path
	// Esc and /abort actually take.
	c.Cancel(branchID)

	wg.Wait()
	require.True(t, resp.IsError)
	require.True(t, resp.StopTurn, "an abandoned branch must not leave the caller retrying")
	require.Contains(t, resp.Content, "ended this branch")
}

// Cancelling the conversation that is suspended reaches through to the
// branch. Without it the caller is freed while the branch runs on, working
// for a result nobody will read.
func TestCancelOnTheParentAbandonsTheBranch(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	forking, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	seen := make(chan string, 1)
	agent, resolved := idleBranchAgent(t, seen)

	var resp fantasy.ToolResponse
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, _ = c.runBranchAgent(t.Context(), branchParams(agent, resolved, parent.ID, forking.ID))
	}()

	requireBranchStarted(t, seen)
	requireBranchSession(t, c, parent.ID)

	c.Cancel(parent.ID)

	wg.Wait()
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "ended this branch")
}

// A cancel that names neither a branch nor a suspended conversation must be
// left entirely to the pre-existing path.
func TestCancelOnAnOrdinarySessionIsUnchanged(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	c := branchCoordinator(t, env)

	require.False(t, c.abortBranchFor("nobody"),
		"an ordinary session must fall through to the normal cancel path")

	// And a live branch does not make its neighbours look like one.
	c.branches.Register("branch-1", "parent-1")
	require.False(t, c.abortBranchFor("unrelated"))
	require.True(t, c.branches.Waiting("branch-1"))
}

// The opening turn failing must not strand the caller. It is reported
// through the same rendezvous everything else uses, so a user who gave up
// while it was failing still sees their own outcome rather than this one.
func TestRunBranchAgentReportsAStartupFailure(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	forking, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{Role: message.Assistant})
	require.NoError(t, err)

	agent, resolved := newMockAgent(branchProviderID, 4096,
		func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
			return nil, context.DeadlineExceeded
		})

	resp, err := c.runBranchAgent(t.Context(), branchParams(agent, resolved, parent.ID, forking.ID))
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "could not be started")
}

// TestBranchDispatchRefusals covers the two ways a fork is turned down, and
// the case that looks like a third but is not. Each refusal has to explain
// itself: the model can only act on it if it says what to do instead.
func TestBranchDispatchRefusals(t *testing.T) {
	t.Parallel()

	t.Run("a headless run cannot fork", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		c := branchCoordinator(t, env)
		c.interactive = false

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		refusal := c.branchDispatchRefusal(t.Context(), parent.ID)
		require.Contains(t, refusal, "interactive")
		require.Contains(t, strings.ToLower(refusal), "subagent instead")
	})

	t.Run("only a top-level conversation can fork", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		c := branchCoordinator(t, env)

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		child, err := env.sessions.CreateTaskSession(t.Context(), "child-1", parent.ID, "Child")
		require.NoError(t, err)

		refusal := c.branchDispatchRefusal(t.Context(), child.ID)
		require.Contains(t, refusal, "top-level")
		require.Contains(t, strings.ToLower(refusal), "subagent instead")
	})

	t.Run("an outstanding branch does not block another", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		c := branchCoordinator(t, env)

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		c.branches.Register("branch-1", parent.ID)

		require.Empty(t, c.branchDispatchRefusal(t.Context(), parent.ID))
	})

	t.Run("a top-level interactive conversation may fork", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		c := branchCoordinator(t, env)

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		require.Empty(t, c.branchDispatchRefusal(t.Context(), parent.ID))
	})
}

// A branch stands in for the conversation it forked, so it must keep that
// conversation's delegation budget rather than spending a level on the hop.
func TestDispatchDepthSkipsTheBranchHop(t *testing.T) {
	env := testEnv(t)
	c := branchCoordinator(t, env)
	c.cfg.Config().Agents = map[string]config.Agent{
		"pairing": {ID: "pairing", Mode: config.AgentModeBranch},
		"general": {ID: "general", Mode: config.AgentModeSubagent},
	}

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	branch, err := env.sessions.CreateTaskSession(t.Context(), "branch-1", parent.ID, "Branch")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateActiveAgent(t.Context(), branch.ID, config.ActiveAgentState{Agent: "pairing"}))

	sub, err := env.sessions.CreateTaskSession(t.Context(), "sub-1", branch.ID, "Sub")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateActiveAgent(t.Context(), sub.ID, config.ActiveAgentState{Agent: "general"}))

	require.Equal(t, 0, c.dispatchDepth(t.Context(), parent.ID))
	require.Equal(t, 0, c.dispatchDepth(t.Context(), branch.ID),
		"a branch continues its parent rather than nesting under it")
	require.Equal(t, 1, c.dispatchDepth(t.Context(), sub.ID),
		"a real delegation under a branch still costs a level")
}
