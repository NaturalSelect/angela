package agent

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
)

//go:embed templates/agent_tool.md.tpl
var agentToolDescriptionTmpl string

type AgentParams struct {
	Description  string `json:"description,omitempty" description:"A short (3-5 words) description of the task"`
	Prompt       string `json:"prompt" description:"The task for the agent to perform"`
	SubagentType string `json:"subagent_type,omitempty" description:"The type of specialized agent to use for this task"`
}

const (
	AgentToolName = "agent"
)

// agentTool builds the agent dispatch tool from the coordinator's
// subagent registry. The registry holds config snapshots, not built
// agents: each subagent is constructed on its first dispatch, so a
// session that never delegates pays nothing, and a subagent that fails
// to build only fails its own call.
func (c *coordinator) agentTool() (fantasy.AgentTool, error) {
	description, err := renderAgentToolDescription(c.subagents.Metadata())
	if err != nil {
		return nil, fmt.Errorf("render agent tool description: %w", err)
	}

	return fantasy.NewParallelAgentTool(
		AgentToolName,
		description,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			// Default to task agent for backward compatibility.
			agentType := params.SubagentType
			if agentType == "" {
				agentType = config.AgentTask
			}

			entry, ok := c.subagents.Get(agentType)
			if !ok {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("Unknown subagent_type %q. Available types: %s",
						agentType, strings.Join(c.subagents.IDs(), ", ")),
				), nil
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			// Building the agent can fail on a bad prompt template or
			// an unreachable provider. That is this dispatch's problem
			// alone, so it comes back as a tool error rather than
			// taking down the coordinator.
			agent, err := entry.resolve(func(agentCfg config.Agent) (SessionAgent, error) {
				return c.buildSubAgentSync(ctx, agentCfg)
			})
			if err != nil {
				slog.Error("Failed to build subagent", "agent", agentType, "error", err)
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("Subagent %q is unavailable: %v", agentType, err),
				), nil
			}

			title := params.Description
			if title == "" {
				title = "New Agent Session"
			}

			return c.runSubAgent(ctx, subAgentParams{
				Agent:          agent,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   title,
			})
		},
	), nil
}

// agentToolDescription is the data structure passed to the agent tool
// description template.
type agentToolDescription struct {
	Agents []agentToolDescriptionAgent
}

type agentToolDescriptionAgent struct {
	ID          string
	Description string
}

func renderAgentToolDescription(agents []agentToolDescriptionAgent) (string, error) {
	tmpl, err := template.New("agent_tool").Parse(agentToolDescriptionTmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, agentToolDescription{Agents: agents}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
