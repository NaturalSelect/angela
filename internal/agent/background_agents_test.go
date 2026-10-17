package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

func testBackgroundInfo(id, parent string) backgroundAgentInfo {
	return backgroundAgentInfo{
		ID:              id,
		ParentSessionID: parent,
		ChildSessionID:  "child-" + id,
		StartedAt:       time.Now(),
	}
}

// cancelSpy is a cancel func that records whether it was called.
type cancelSpy struct{ called bool }

func (s *cancelSpy) cancel() { s.called = true }

func TestBackgroundAgentsStartRejectsDuplicatesAndEnforcesTheLimitPerParent(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	for i := range maxBackgroundAgentsPerSession {
		require.NoError(t, r.start(testBackgroundInfo(fmt.Sprintf("a%d", i), "p1"), func() {}))
	}

	require.ErrorIs(t, r.start(testBackgroundInfo("over", "p1"), func() {}), errBackgroundAgentLimit)
	require.Error(t, r.start(testBackgroundInfo("a0", "p1"), func() {}), "an id launches once")
	require.NoError(t, r.start(testBackgroundInfo("other", "p2"), func() {}), "the limit is per parent session")

	require.True(t, r.finish("a0", tools.BackgroundAgentCompleted))
	require.NoError(t, r.start(testBackgroundInfo("again", "p1"), func() {}), "a finished agent frees its slot")
}

func TestBackgroundAgentsAbandonFreesAFailedLaunch(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	require.NoError(t, r.start(testBackgroundInfo("a", "p"), func() {}))
	r.abandon("a")

	_, ok := r.get("p", "a")
	require.False(t, ok)
	require.NoError(t, r.start(testBackgroundInfo("a", "p"), func() {}), "the id is free to launch again")
}

func TestBackgroundAgentsGetHidesAnotherSessionsAgent(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	require.NoError(t, r.start(testBackgroundInfo("a", "p1"), func() {}))

	info, ok := r.get("p1", "a")
	require.True(t, ok)
	require.Equal(t, tools.BackgroundAgentRunning, info.Status)

	_, ok = r.get("p2", "a")
	require.False(t, ok, "a session cannot see an agent it did not launch")
}

func TestBackgroundAgentsListIsOldestFirst(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	now := time.Now()
	for _, tc := range []struct {
		id string
		at time.Time
	}{
		{"late", now.Add(2 * time.Second)},
		{"b-tie", now},
		{"a-tie", now},
	} {
		info := testBackgroundInfo(tc.id, "p")
		info.StartedAt = tc.at
		require.NoError(t, r.start(info, func() {}))
	}
	require.NoError(t, r.start(testBackgroundInfo("elsewhere", "other"), func() {}))

	var ids []string
	for _, info := range r.list("p") {
		ids = append(ids, info.ID)
	}
	require.Equal(t, []string{"a-tie", "b-tie", "late"}, ids)
}

func TestBackgroundAgentsRunningExcludesFinishedAgents(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	require.NoError(t, r.start(testBackgroundInfo("a", "p"), func() {}))
	require.NoError(t, r.start(testBackgroundInfo("b", "p"), func() {}))
	require.True(t, r.finish("a", tools.BackgroundAgentFailed))

	running := r.running("p")
	require.Len(t, running, 1)
	require.Equal(t, "b", running[0].ID)
}

func TestBackgroundAgentsStop(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	spy := &cancelSpy{}
	require.NoError(t, r.start(testBackgroundInfo("a", "p"), spy.cancel))

	require.Equal(t, stopUnknown, r.stop("p", "missing"))
	require.Equal(t, stopUnknown, r.stop("other", "a"), "another session cannot stop it")
	require.False(t, spy.called)

	require.Equal(t, stopRequested, r.stop("p", "a"))
	require.True(t, spy.called)

	info, _ := r.get("p", "a")
	require.Equal(t, tools.BackgroundAgentRunning, info.Status,
		"stop only asks; the agent records its own ending")

	require.True(t, r.finish("a", tools.BackgroundAgentStopped))
	require.Equal(t, stopNotRunning, r.stop("p", "a"))
}

func TestBackgroundAgentsFinishRecordsTheTimeAndReportsAMissingAgent(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	require.NoError(t, r.start(testBackgroundInfo("a", "p"), func() {}))
	require.True(t, r.finish("a", tools.BackgroundAgentCompleted))

	info, _ := r.get("p", "a")
	require.Equal(t, tools.BackgroundAgentCompleted, info.Status)
	require.False(t, info.FinishedAt.IsZero())

	require.False(t, r.finish("missing", tools.BackgroundAgentCompleted))
}

func TestBackgroundAgentsSessionDeleted(t *testing.T) {
	t.Parallel()

	t.Run("a deleted parent drops its agents and stops the running ones", func(t *testing.T) {
		t.Parallel()

		r := newBackgroundAgents()
		running, done := &cancelSpy{}, &cancelSpy{}
		require.NoError(t, r.start(testBackgroundInfo("running", "p"), running.cancel))
		require.NoError(t, r.start(testBackgroundInfo("done", "p"), done.cancel))
		require.True(t, r.finish("done", tools.BackgroundAgentCompleted))
		require.NoError(t, r.start(testBackgroundInfo("kept", "other"), func() {}))

		r.sessionDeleted("p")

		require.True(t, running.called)
		require.False(t, done.called, "a finished agent has nothing left to stop")
		require.Empty(t, r.list("p"))
		require.Len(t, r.list("other"), 1)
		require.False(t, r.finish("running", tools.BackgroundAgentStopped),
			"nobody is left to be told once the parent is gone")
	})

	t.Run("a deleted child stops its agent but keeps the entry", func(t *testing.T) {
		t.Parallel()

		r := newBackgroundAgents()
		spy := &cancelSpy{}
		require.NoError(t, r.start(testBackgroundInfo("a", "p"), spy.cancel))

		r.sessionDeleted("child-a")

		require.True(t, spy.called)
		info, ok := r.get("p", "a")
		require.True(t, ok, "the parent is still there to be told")
		require.Equal(t, tools.BackgroundAgentRunning, info.Status)
	})
}

func TestBackgroundAgentsShutdownStopsEverythingAndRefusesNewLaunches(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	a, b := &cancelSpy{}, &cancelSpy{}
	require.NoError(t, r.start(testBackgroundInfo("a", "p1"), a.cancel))
	require.NoError(t, r.start(testBackgroundInfo("b", "p2"), b.cancel))

	r.shutdown()

	require.True(t, a.called)
	require.True(t, b.called)
	require.ErrorIs(t, r.start(testBackgroundInfo("c", "p1"), func() {}), errBackgroundAgentsClosed)
	require.False(t, r.finish("a", tools.BackgroundAgentStopped),
		"an agent unwinding during shutdown has no one to wake")
	info, _ := r.get("p1", "a")
	require.Equal(t, tools.BackgroundAgentStopped, info.Status, "the status is still recorded")
}

func TestNilBackgroundAgentsIsSafeForTheCallsTheCoordinatorAlwaysMakes(t *testing.T) {
	t.Parallel()

	var r *backgroundAgents
	require.Empty(t, r.running("p"))
	require.NotPanics(t, func() {
		r.shutdown()
		r.sessionDeleted("p")
	})
}

func TestClassifyBackgroundOutcome(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		outcome subAgentOutcome
		want    tools.BackgroundAgentStatus
	}{
		{"an answer completes", t.Context(), subAgentOutcome{Text: "done"}, tools.BackgroundAgentCompleted},
		{"an answer wins over a stop that landed with it", canceled, subAgentOutcome{Text: "done"}, tools.BackgroundAgentCompleted},
		{"a stopped context stops", canceled, subAgentOutcome{Failure: "x", Err: errors.New("boom")}, tools.BackgroundAgentStopped},
		{"a cancelled run stops even when the context was untouched", t.Context(), subAgentOutcome{Failure: "x", Err: fmt.Errorf("wrapped: %w", context.Canceled)}, tools.BackgroundAgentStopped},
		{"any other failure fails", t.Context(), subAgentOutcome{Failure: "x", Err: errors.New("boom")}, tools.BackgroundAgentFailed},
		{"a failure without an error fails", t.Context(), subAgentOutcome{Failure: "no output"}, tools.BackgroundAgentFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, classifyBackgroundOutcome(tc.ctx, tc.outcome))
		})
	}
}

// newBackgroundTestCoordinator builds an interactive coordinator that
// offers background agents.
func newBackgroundTestCoordinator(t *testing.T) *coordinator {
	t.Helper()

	coord := newHermeticTestCoordinator(t, true, false)
	coord.background = newBackgroundAgents()
	coord.notices = newNoticeInbox()
	t.Cleanup(func() {
		coord.background.shutdown()
		require.Eventually(t, func() bool {
			return !anyBackgroundAgentRunning(coord.background)
		}, 5*time.Second, 10*time.Millisecond, "background agents must unwind before the test ends")
	})
	return coord
}

func anyBackgroundAgentRunning(r *backgroundAgents) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.byID {
		if e.info.Status == tools.BackgroundAgentRunning {
			return true
		}
	}
	return false
}

// backgroundLaunch is one launched agent under test.
type backgroundLaunch struct {
	parentID string
	agentID  string
	response fantasy.ToolResponse
}

func launchTestAgent(
	t *testing.T,
	coord *coordinator,
	callID string,
	run func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error),
) backgroundLaunch {
	t.Helper()

	parent, err := coord.sessions.Create(t.Context(), "Parent "+callID)
	require.NoError(t, err)
	return launchTestAgentFrom(t, coord, parent.ID, callID, run)
}

func launchTestAgentFrom(
	t *testing.T,
	coord *coordinator,
	parentID, callID string,
	run func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error),
) backgroundLaunch {
	t.Helper()

	agent, resolved := newMockAgent(t, "mock", 4096, run)
	params := subAgentParams{
		Agent:          agent,
		Resolved:       resolved,
		SessionID:      parentID,
		AgentMessageID: "msg-" + callID,
		ToolCallID:     callID,
		Prompt:         "look into it",
		SessionTitle:   "Look into it",
	}
	resp := coord.launchBackgroundAgent(t.Context(), params, config.AgentExplore, "Look into it").Response()
	return backgroundLaunch{
		parentID: parentID,
		agentID:  tools.AgentID(coord.sessions, params.AgentMessageID, callID),
		response: resp,
	}
}

func (l backgroundLaunch) status(t *testing.T, coord *coordinator) tools.BackgroundAgentStatus {
	t.Helper()
	info, ok := coord.background.get(l.parentID, l.agentID)
	require.True(t, ok)
	return info.Status
}

func (l backgroundLaunch) awaitNotice(t *testing.T, coord *coordinator) tools.BackgroundAgentNotice {
	t.Helper()

	require.Eventually(t, func() bool { return coord.notices.Has(l.parentID) },
		5*time.Second, 10*time.Millisecond, "the finished agent must tell its parent")
	notices := tools.ParseBackgroundAgentNotices(joinNotices(coord.notices.Take(l.parentID)))
	require.Len(t, notices, 1)
	return notices[0]
}

func joinNotices(notices []string) string {
	return strings.Join(notices, "\n")
}

func TestLaunchBackgroundAgentReturnsAtOnceAndNotifiesOnCompletion(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	started := make(chan struct{})
	release := make(chan struct{})
	launch := launchTestAgent(t, coord, "call-1", func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		<-release
		return agentResultWithText("the findings"), nil
	})

	require.False(t, launch.response.IsError)
	require.Contains(t, launch.response.Content, "Async agent launched successfully")
	require.Contains(t, launch.response.Content, launch.agentID)
	require.Contains(t, launch.response.Content, "You know nothing about its results",
		"the receipt must say the model has nothing yet")
	meta := tools.ParseAgentResponseMetadata(launch.response.Metadata)
	require.Equal(t, tools.AgentResponseMetadata{Background: true, AgentID: launch.agentID}, meta)

	<-started
	require.Equal(t, tools.BackgroundAgentRunning, launch.status(t, coord))
	require.False(t, coord.notices.Has(launch.parentID), "nothing to report while it runs")

	close(release)
	notice := launch.awaitNotice(t, coord)

	require.Equal(t, launch.agentID, notice.AgentID)
	require.Equal(t, tools.BackgroundAgentCompleted, notice.Status)
	require.Contains(t, notice.Summary, "Look into it")
	require.Contains(t, notice.Result, "the findings")
	require.Contains(t, notice.Result, "agent="+config.AgentExplore, "the result carries the report banner")
	require.Equal(t, tools.BackgroundAgentCompleted, launch.status(t, coord))
}

func TestLaunchBackgroundAgentSurvivesTheLaunchingCallEnding(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	release := make(chan struct{})
	callCtx, endCall := context.WithCancel(t.Context())

	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	agent, resolved := newMockAgent(t, "mock", 4096, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		<-release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return agentResultWithText("still alive"), nil
	})
	params := subAgentParams{
		Agent: agent, Resolved: resolved, SessionID: parent.ID,
		AgentMessageID: "msg-1", ToolCallID: "call-1", Prompt: "p", SessionTitle: "t",
	}
	resp := coord.launchBackgroundAgent(callCtx, params, config.AgentExplore, "Survive").Response()
	require.False(t, resp.IsError)
	launch := backgroundLaunch{parentID: parent.ID, agentID: tools.AgentID(coord.sessions, "msg-1", "call-1")}

	endCall()
	close(release)

	notice := launch.awaitNotice(t, coord)
	require.Equal(t, tools.BackgroundAgentCompleted, notice.Status,
		"ending the parent's turn or tool call must not take the agent with it")
}

func TestLaunchBackgroundAgentStopReportsStopped(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	started := make(chan struct{})
	launch := launchTestAgent(t, coord, "call-1", func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-started

	require.Equal(t, stopRequested, coord.background.stop(launch.parentID, launch.agentID))

	notice := launch.awaitNotice(t, coord)
	require.Equal(t, tools.BackgroundAgentStopped, notice.Status)
	require.Contains(t, notice.Summary, "stopped")
	require.Equal(t, tools.BackgroundAgentStopped, launch.status(t, coord))
}

func TestLaunchBackgroundAgentFailureCarriesTheError(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	launch := launchTestAgent(t, coord, "call-1", func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return nil, errors.New("provider exploded")
	})

	notice := launch.awaitNotice(t, coord)
	require.Equal(t, tools.BackgroundAgentFailed, notice.Status)
	require.Contains(t, notice.Result, "provider exploded")
	require.NotContains(t, notice.Result, "agent="+config.AgentExplore, "a failure has no report to reload")
}

func TestLaunchBackgroundAgentRefusesPastTheLimitWithoutLeavingASession(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	block := func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	for i := range maxBackgroundAgentsPerSession {
		launch := launchTestAgentFrom(t, coord, parent.ID, fmt.Sprintf("call-%d", i), block)
		require.False(t, launch.response.IsError)
	}

	over := launchTestAgentFrom(t, coord, parent.ID, "call-over", block)

	require.True(t, over.response.IsError)
	require.Contains(t, over.response.Content, "too many background agents")
	_, err = coord.sessions.Get(t.Context(), coord.sessions.CreateAgentToolSessionID("msg-call-over", "call-over"))
	require.Error(t, err, "a refused launch must not leave a sub-session behind")
}

func TestLaunchBackgroundAgentRefusesOnceShutDown(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	coord.CancelAll()

	launch := launchTestAgent(t, coord, "call-1", func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		t.Error("a refused launch must not run the agent")
		return nil, nil
	})

	require.True(t, launch.response.IsError)
	require.Contains(t, launch.response.Content, "shutting down")
}

func TestCancelOfTheChildSessionStopsTheBackgroundAgent(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	started := make(chan struct{})
	launch := launchTestAgent(t, coord, "call-1", func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-started
	childID := coord.sessions.CreateAgentToolSessionID("msg-call-1", "call-1")

	coord.Cancel(childID)

	notice := launch.awaitNotice(t, coord)
	require.Equal(t, tools.BackgroundAgentStopped, notice.Status,
		"a stop aimed at the sub-session must end the agent the way AgentStop does")
}

func TestCancelOfTheParentLeavesTheBackgroundAgentRunning(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	started := make(chan struct{})
	release := make(chan struct{})
	launch := launchTestAgent(t, coord, "call-1", func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return agentResultWithText("done anyway"), nil
	})
	<-started

	coord.Cancel(launch.parentID)

	require.Equal(t, tools.BackgroundAgentRunning, launch.status(t, coord),
		"Esc on the parent's turn must not take its background agents down")
	close(release)
	require.Equal(t, tools.BackgroundAgentCompleted, launch.awaitNotice(t, coord).Status)
}

func TestBackgroundAgentsStopChild(t *testing.T) {
	t.Parallel()

	r := newBackgroundAgents()
	spy := &cancelSpy{}
	require.NoError(t, r.start(testBackgroundInfo("a", "p"), spy.cancel))

	require.False(t, r.stopChild("child-other"))
	require.False(t, spy.called)

	require.True(t, r.stopChild("child-a"))
	require.True(t, spy.called)

	require.True(t, r.finish("a", tools.BackgroundAgentStopped))
	require.False(t, r.stopChild("child-a"), "a finished agent has nothing left to stop")

	var none *backgroundAgents
	require.False(t, none.stopChild("child-a"), "the registry is optional for the coordinator")
}

func TestShutdownCancelAllStopsBackgroundAgentsWithoutWakingAnyone(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	started := make(chan struct{})
	launch := launchTestAgent(t, coord, "call-1", func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-started

	coord.CancelAll()

	require.Eventually(t, func() bool {
		return launch.status(t, coord) == tools.BackgroundAgentStopped
	}, 5*time.Second, 10*time.Millisecond)
	require.False(t, coord.notices.Has(launch.parentID), "a session that is going away is not told")
}

func TestWakeForNoticesRunsANoticeOnlyTurnOnTheCoordinatorLifetime(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	coord.lifetime = t.Context()

	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	woken := make(chan SessionAgentCall, 1)
	coord.currentAgent = newMockSessionAgent(t, config.AgentCoder, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		woken <- call
		return agentResultWithText("noted"), nil
	})

	coord.wakeForNotices(parent.ID)

	select {
	case call := <-woken:
		require.True(t, call.noticeWake, "the turn exists only to deliver notices")
		require.Empty(t, call.Prompt)
		require.Equal(t, parent.ID, call.SessionID)
	case <-time.After(5 * time.Second):
		t.Fatal("the wake never reached the session's executor")
	}
}

func TestWakeForNoticesWithoutALifetimeDoesNothing(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	coord.lifetime = nil

	require.NotPanics(t, func() { coord.wakeForNotices("any-session") })
}

func agentToolInfo(t *testing.T, coord *coordinator, depth int) fantasy.ToolInfo {
	t.Helper()

	tool, err := coord.agentTool(depth, nil, config.ActiveAgent{})
	require.NoError(t, err)
	require.NotNil(t, tool)
	return tool.Info()
}

func TestAgentToolOffersRunInBackgroundOnlyWhereItCanWork(t *testing.T) {
	interactive := newBackgroundTestCoordinator(t)
	nonInteractive := newHermeticTestCoordinator(t, false, false)
	nonInteractive.background = newBackgroundAgents()
	nonInteractive.notices = newNoticeInbox()
	handBuilt := newHermeticTestCoordinator(t, true, false)

	tests := []struct {
		name    string
		coord   *coordinator
		depth   int
		offered bool
	}{
		{"top-level turn of an interactive coordinator", interactive, 0, true},
		{"a sub-agent has no user to wait on", interactive, 1, false},
		{"angela run exits when its turn does", nonInteractive, 0, false},
		{"a coordinator with nowhere to track an agent", handBuilt, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := agentToolInfo(t, tc.coord, tc.depth)

			_, hasParam := info.Parameters[runInBackgroundParameter]
			require.Equal(t, tc.offered, hasParam, "schema")
			require.NotContains(t, info.Required, runInBackgroundParameter)
			require.Equal(t, tc.offered, tc.coord.backgroundAgentsAvailable(tc.depth), "gate")
			require.Equal(t, tc.offered, containsAll(info.Description, "Background agents", toolnames.AgentStatus, toolnames.AgentStop),
				"description")
			require.Contains(t, info.Parameters, "prompt", "hiding one parameter leaves the rest")
		})
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func TestWithoutParameterDoesNotMutateTheInnerSchema(t *testing.T) {
	coord := newHermeticTestCoordinator(t, false, false)

	tool, err := coord.agentTool(0, nil, config.ActiveAgent{})
	require.NoError(t, err)
	hidden, ok := tool.(withoutParameter)
	require.True(t, ok)

	_ = hidden.Info()
	_, stillThere := hidden.AgentTool.Info().Parameters[runInBackgroundParameter]
	require.True(t, stillThere, "the wrapper hides the parameter from the model, not from the tool")
}
