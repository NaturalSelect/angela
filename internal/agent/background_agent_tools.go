package agent

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed agent_status.md
var agentStatusDescription string

//go:embed agent_stop.md
var agentStopDescription string

type agentStatusParams struct {
	AgentID string `json:"agent_id,omitempty" description:"The agentId from the launch receipt. Leave out to list every background agent launched from this conversation."`
}

type agentStopParams struct {
	AgentID string `json:"agent_id" description:"The agentId from the launch receipt of the background agent to stop."`
}

// agentStatusTool and agentStopTool live here rather than in package tools
// because they read the coordinator's registry, which tools cannot import.
func (c *coordinator) agentStatusTool() fantasy.AgentTool {
	return tools.NewParallelTool(
		toolnames.AgentStatus,
		agentStatusDescription,
		func(ctx context.Context, params agentStatusParams, _ fantasy.ToolCall) tools.Result {
			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return tools.Fail("session id missing from context")
			}

			if params.AgentID != "" {
				if info, ok := c.background.get(sessionID, params.AgentID); ok {
					return tools.Ok(formatBackgroundAgent(info))
				}
				msgs, err := c.messages.List(ctx, sessionID)
				if err != nil {
					return tools.FailErr("failed to read the conversation", err)
				}
				record, ok := tools.FindBackgroundAgent(msgs, params.AgentID)
				if !ok {
					return tools.Fail(unknownBackgroundAgentMessage(params.AgentID))
				}
				return tools.Ok(formatRecordedBackgroundAgent(record))
			}

			infos := c.background.list(sessionID)
			if len(infos) == 0 {
				return tools.Ok("No background agents have been launched from this conversation.")
			}
			blocks := make([]string, len(infos))
			for i, info := range infos {
				blocks[i] = formatBackgroundAgent(info)
			}
			return tools.Ok(strings.Join(blocks, "\n\n"))
		},
	)
}

func (c *coordinator) agentStopTool() fantasy.AgentTool {
	return tools.NewTool(
		toolnames.AgentStop,
		agentStopDescription,
		func(ctx context.Context, params agentStopParams, _ fantasy.ToolCall) tools.Result {
			if params.AgentID == "" {
				return tools.Fail("agent_id is required")
			}
			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return tools.Fail("session id missing from context")
			}

			switch c.background.stop(sessionID, params.AgentID) {
			case stopRequested:
				return tools.Ok(fmt.Sprintf("Stop requested for agent %s. You will be notified when it has ended.", params.AgentID))
			case stopNotRunning:
				info, _ := c.background.get(sessionID, params.AgentID)
				return tools.Ok(fmt.Sprintf("Agent %s is not running: it already %s.", params.AgentID, info.Status))
			default:
				return tools.Fail(unknownBackgroundAgentMessage(params.AgentID))
			}
		},
	)
}

func unknownBackgroundAgentMessage(agentID string) string {
	return fmt.Sprintf("No background agent %q was launched from this conversation. Call %s with no agent_id to list them.", agentID, toolnames.AgentStatus)
}

// formatRecordedBackgroundAgent renders an agent the registry no longer
// knows, from what the transcript kept. Without a notice the run died with
// the process that hosted it. A completed agent's output is never repeated
// here; the report id says where to load it.
func formatRecordedBackgroundAgent(record tools.BackgroundAgentRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agentId: %s\n", record.AgentID)
	if record.Notice == nil {
		b.WriteString("status: interrupted\n")
	} else {
		fmt.Fprintf(&b, "status: %s\n", record.Notice.Status)
	}
	fmt.Fprintf(&b, "type: %s\n", record.AgentType)
	if record.Task != "" {
		fmt.Fprintf(&b, "task: %s\n", record.Task)
	}
	switch {
	case record.Notice == nil:
		b.WriteString("Angela restarted before it finished; its transcript is in its sub-session.")
	case record.ReportID != "":
		fmt.Fprintf(&b, "Its report can be loaded with %s: %s", toolnames.LoadReport, record.ReportID)
	default:
		b.WriteString(record.Notice.Summary)
	}
	return b.String()
}

// formatBackgroundAgent renders one agent for the model. It deliberately
// carries no output: that arrives in the completion notice, and handing it
// out here would let a model poll its way to a result.
func formatBackgroundAgent(info backgroundAgentInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agentId: %s\nstatus: %s\ntype: %s\n", info.ID, info.Status, info.AgentType)
	if info.Task != "" {
		fmt.Fprintf(&b, "task: %s\n", info.Task)
	}
	if info.Status == tools.BackgroundAgentRunning {
		fmt.Fprintf(&b, "running for: %s", time.Since(info.StartedAt).Round(time.Second))
	} else {
		fmt.Fprintf(&b, "ran for: %s", info.FinishedAt.Sub(info.StartedAt).Round(time.Second))
	}
	return b.String()
}
