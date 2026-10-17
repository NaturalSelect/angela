package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

func runBackgroundTool(t *testing.T, tool fantasy.AgentTool, sessionID, input string) fantasy.ToolResponse {
	t.Helper()

	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, sessionID)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-tool", Name: tool.Info().Name, Input: input})
	require.NoError(t, err)
	return resp
}

func blockedAgent(release <-chan struct{}) func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
	return func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		select {
		case <-release:
			return agentResultWithText("secret findings"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func TestAgentStatusToolReportsOneAgentWithoutItsOutput(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	release := make(chan struct{})
	launch := launchTestAgent(t, coord, "call-1", blockedAgent(release))
	status := coord.agentStatusTool()
	input := `{"agent_id":"` + launch.agentID + `"}`

	running := runBackgroundTool(t, status, launch.parentID, input)
	require.False(t, running.IsError)
	require.Contains(t, running.Content, "agentId: "+launch.agentID)
	require.Contains(t, running.Content, "status: running")
	require.Contains(t, running.Content, "type: "+config.AgentExplore)
	require.Contains(t, running.Content, "Look into it")
	require.Contains(t, running.Content, "running for:")

	close(release)
	launch.awaitNotice(t, coord)

	done := runBackgroundTool(t, status, launch.parentID, input)
	require.False(t, done.IsError)
	require.Contains(t, done.Content, "status: completed")
	require.Contains(t, done.Content, "ran for:")
	require.NotContains(t, done.Content, "secret findings",
		"the result reaches the model in the notification only, or status becomes a way to poll for it")
}

func TestAgentStatusToolListsEveryAgentOfTheConversation(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	status := coord.agentStatusTool()

	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	empty := runBackgroundTool(t, status, parent.ID, `{}`)
	require.False(t, empty.IsError)
	require.Equal(t, "No background agents have been launched from this conversation.", empty.Content)

	release := make(chan struct{})
	first := launchTestAgentFrom(t, coord, parent.ID, "call-1", blockedAgent(release))
	second := launchTestAgentFrom(t, coord, parent.ID, "call-2", blockedAgent(release))
	elsewhere := launchTestAgent(t, coord, "call-3", blockedAgent(release))
	t.Cleanup(func() { close(release) })

	listed := runBackgroundTool(t, status, parent.ID, `{}`)
	require.False(t, listed.IsError)
	require.Contains(t, listed.Content, first.agentID)
	require.Contains(t, listed.Content, second.agentID)
	require.NotContains(t, listed.Content, elsewhere.agentID, "another conversation's agents are not listed")
}

func TestAgentStatusToolRefusesUnknownAndForeignAgents(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	owner := launchTestAgent(t, coord, "call-1", blockedAgent(release))
	stranger, err := coord.sessions.Create(t.Context(), "Stranger")
	require.NoError(t, err)
	status := coord.agentStatusTool()

	unknown := runBackgroundTool(t, status, owner.parentID, `{"agent_id":"nope"}`)
	require.True(t, unknown.IsError)
	require.Contains(t, unknown.Content, `"nope"`)
	require.Contains(t, unknown.Content, toolnames.AgentStatus, "the message says how to find the right id")

	foreign := runBackgroundTool(t, status, stranger.ID, `{"agent_id":"`+owner.agentID+`"}`)
	require.True(t, foreign.IsError, "an agent id from another conversation must not resolve")
	require.NotContains(t, foreign.Content, "status:")
}

func TestAgentStopToolStopsARunningAgent(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	launch := launchTestAgent(t, coord, "call-1", blockedAgent(make(chan struct{})))

	resp := runBackgroundTool(t, coord.agentStopTool(), launch.parentID, `{"agent_id":"`+launch.agentID+`"}`)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Stop requested for agent "+launch.agentID)

	notice := launch.awaitNotice(t, coord)
	require.Equal(t, tools.BackgroundAgentStopped, notice.Status)
}

func TestAgentStopToolOnAFinishedAgentChangesNothing(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	release := make(chan struct{})
	close(release)
	launch := launchTestAgent(t, coord, "call-1", blockedAgent(release))
	launch.awaitNotice(t, coord)

	resp := runBackgroundTool(t, coord.agentStopTool(), launch.parentID, `{"agent_id":"`+launch.agentID+`"}`)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "is not running")
	require.Contains(t, resp.Content, string(tools.BackgroundAgentCompleted))
	require.Equal(t, tools.BackgroundAgentCompleted, launch.status(t, coord))
	require.False(t, coord.notices.Has(launch.parentID), "a no-op stop must not queue another notice")
}

func TestAgentStopToolRefusesBadAndForeignIDs(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	owner := launchTestAgent(t, coord, "call-1", blockedAgent(release))
	stranger, err := coord.sessions.Create(t.Context(), "Stranger")
	require.NoError(t, err)
	stop := coord.agentStopTool()

	missing := runBackgroundTool(t, stop, owner.parentID, `{}`)
	require.True(t, missing.IsError)
	require.Contains(t, missing.Content, "agent_id is required")

	unknown := runBackgroundTool(t, stop, owner.parentID, `{"agent_id":"nope"}`)
	require.True(t, unknown.IsError)

	foreign := runBackgroundTool(t, stop, stranger.ID, `{"agent_id":"`+owner.agentID+`"}`)
	require.True(t, foreign.IsError, "one conversation cannot stop another's agent")
	require.Equal(t, tools.BackgroundAgentRunning, owner.status(t, coord))
}

// storeLaunchedAgent persists a background launch the way a finished session
// leaves it, and returns the agent id. The registry is not told, which is
// what a restart looks like.
func storeLaunchedAgent(t *testing.T, coord *coordinator, parentID, callID string) string {
	t.Helper()

	assistant, err := coord.messages.Create(t.Context(), parentID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{
				ID:       callID,
				Name:     toolnames.Agent,
				Input:    `{"subagent_type":"explore","description":"find callers","run_in_background":true}`,
				Finished: true,
			},
		},
	})
	require.NoError(t, err)

	agentID := tools.AgentID(coord.sessions, assistant.ID, callID)
	_, err = coord.messages.Create(t.Context(), parentID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{
				ToolCallID: callID,
				Name:       toolnames.Agent,
				Content:    tools.BackgroundAgentLaunchReceipt(agentID),
				Metadata:   `{"background":true,"agent_id":"` + agentID + `"}`,
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, coord.messages.FlushAll(t.Context()))
	return agentID
}

// storeNotice persists a notice the way the inbox delivers it: as a
// reminder message in the parent session.
func storeNotice(t *testing.T, coord *coordinator, parentID string, notice tools.BackgroundAgentNotice) {
	t.Helper()

	_, err := coord.messages.Create(t.Context(), parentID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: reminder.Wrap(tools.RenderBackgroundAgentNotice(notice))}},
	})
	require.NoError(t, err)
	require.NoError(t, coord.messages.FlushAll(t.Context()))
}

func TestAgentStatusToolReportsAnAgentLostToARestartAsInterrupted(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	agentID := storeLaunchedAgent(t, coord, parent.ID, "call-1")

	resp := runBackgroundTool(t, coord.agentStatusTool(), parent.ID, `{"agent_id":"`+agentID+`"}`)

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "agentId: "+agentID)
	require.Contains(t, resp.Content, "status: interrupted")
	require.Contains(t, resp.Content, "type: "+config.AgentExplore)
	require.Contains(t, resp.Content, "find callers")
	require.Contains(t, resp.Content, "Angela restarted before it finished")
}

func TestAgentStatusToolReportsTheOutcomeOfAnAgentThatReportedBeforeARestart(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	agentID := storeLaunchedAgent(t, coord, parent.ID, "call-1")
	storeNotice(t, coord, parent.ID, tools.BackgroundAgentNotice{
		AgentID: agentID,
		Status:  tools.BackgroundAgentStopped,
		Summary: "Agent stopped on request",
	})

	resp := runBackgroundTool(t, coord.agentStatusTool(), parent.ID, `{"agent_id":"`+agentID+`"}`)

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "status: stopped")
	require.NotContains(t, resp.Content, "interrupted")
	require.Contains(t, resp.Content, "Agent stopped on request")
}

func TestAgentStatusToolPointsACompletedAgentAtItsReportWithoutRepeatingIt(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	agentID := storeLaunchedAgent(t, coord, parent.ID, "call-1")
	reportID := "rpt_" + strings.TrimPrefix(agentID, "agt_")
	storeNotice(t, coord, parent.ID, tools.BackgroundAgentNotice{
		AgentID: agentID,
		Status:  tools.BackgroundAgentCompleted,
		Summary: "Agent completed",
		Result:  tools.ReportBanner(reportID, config.AgentExplore, "find callers") + "secret findings",
	})

	resp := runBackgroundTool(t, coord.agentStatusTool(), parent.ID, `{"agent_id":"`+agentID+`"}`)

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "status: completed")
	require.Contains(t, resp.Content, toolnames.LoadReport+": "+reportID)
	require.NotContains(t, resp.Content, "secret findings",
		"the transcript fallback must not become a way to poll for the output either")
}

func TestAgentStatusToolDoesNotResolveAnotherConversationsStoredAgent(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	owner, err := coord.sessions.Create(t.Context(), "Owner")
	require.NoError(t, err)
	stranger, err := coord.sessions.Create(t.Context(), "Stranger")
	require.NoError(t, err)
	agentID := storeLaunchedAgent(t, coord, owner.ID, "call-1")

	resp := runBackgroundTool(t, coord.agentStatusTool(), stranger.ID, `{"agent_id":"`+agentID+`"}`)

	require.True(t, resp.IsError, "a stored agent belongs to the conversation that launched it")
	require.NotContains(t, resp.Content, "status:")
}

func TestBackgroundAgentToolsNeedASession(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)

	for _, tool := range []fantasy.AgentTool{coord.agentStatusTool(), coord.agentStopTool()} {
		resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "c", Name: tool.Info().Name, Input: `{"agent_id":"x"}`})
		require.NoError(t, err)
		require.True(t, resp.IsError, tool.Info().Name)
	}
}

func builtToolNames(t *testing.T, coord *coordinator, agent config.Agent, depth int) []string {
	t.Helper()

	built, err := coord.buildTools(agent, config.ActiveAgent{}, "", depth)
	require.NoError(t, err)
	names := make([]string, len(built))
	for i, tool := range built {
		names[i] = tool.Info().Name
	}
	return names
}

func TestDisablingTheAgentToolRemovesTheBackgroundAgentTools(t *testing.T) {
	coord := newBackgroundTestCoordinator(t)
	coder := func() config.Agent { return coord.cfg.Config().Agents[config.AgentCoder] }
	require.Contains(t, builtToolNames(t, coord, coder(), 0), toolnames.AgentStatus)

	coord.cfg.Config().Options.DisabledTools = []string{toolnames.Agent}
	coord.cfg.SetupAgents()

	names := builtToolNames(t, coord, coder(), 0)
	require.NotContains(t, names, toolnames.Agent)
	require.NotContains(t, names, toolnames.AgentStatus)
	require.NotContains(t, names, toolnames.AgentStop)
}

func TestBackgroundAgentToolsComeWithTheAgentToolWhereBackgroundAgentsWork(t *testing.T) {
	interactive := newBackgroundTestCoordinator(t)
	nonInteractive := newHermeticTestCoordinator(t, false, false)
	nonInteractive.background = newBackgroundAgents()
	nonInteractive.notices = newNoticeInbox()

	coder := interactive.cfg.Config().Agents[config.AgentCoder]
	narrow := coder
	narrow.AllowedTools = &config.AllowedToolSet{Kind: config.ToolSetScope, Tools: []string{toolnames.Agent}}
	noAgentTool := coder
	noAgentTool.AllowedTools = &config.AllowedToolSet{Kind: config.ToolSetScope, Tools: []string{toolnames.Read}}

	tests := []struct {
		name  string
		coord *coordinator
		agent config.Agent
		depth int
		want  bool
	}{
		{"the coder of an interactive coordinator", interactive, coder, 0, true},
		{"an allowed_tools list that only names the agent tool cannot strand an agent", interactive, narrow, 0, true},
		{"no agent tool, so nothing to launch from", interactive, noAgentTool, 0, false},
		{"a sub-agent has no user to wait on", interactive, coder, 1, false},
		{"angela run exits when its turn does", nonInteractive, nonInteractive.cfg.Config().Agents[config.AgentCoder], 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			names := builtToolNames(t, tc.coord, tc.agent, tc.depth)
			if tc.want {
				require.Contains(t, names, toolnames.AgentStatus)
				require.Contains(t, names, toolnames.AgentStop)
				return
			}
			require.NotContains(t, names, toolnames.AgentStatus)
			require.NotContains(t, names, toolnames.AgentStop)
		})
	}
}
