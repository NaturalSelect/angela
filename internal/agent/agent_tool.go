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
//
// depth is the dispatch depth of the agent this tool instance belongs
// to; a dispatch through it runs the new subagent at depth+1.
func (c *coordinator) agentTool(depth int) (fantasy.AgentTool, error) {
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

			// A call with no subagent_type is almost always a search
			// need, and explore is read-only, so it is the safe default.
			agentType := params.SubagentType
			if agentType == "" {
				agentType = config.AgentExplore
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
			agent, resolved, err := c.dispatchSubAgent(ctx, entry, depth+1)
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

			run := c.runSubAgent
			if entry.cfg.Mode == config.AgentModeBranch {
				// Checked after the agent is built so that a
				// misconfigured branch still reports the build failure
				// rather than a refusal that hides it.
				if refusal := c.branchDispatchRefusal(ctx, sessionID); refusal != "" {
					return fantasy.NewTextErrorResponse(refusal), nil
				}
				run = c.runBranchAgent
			}

			return run(ctx, subAgentParams{
				Agent:          agent,
				Resolved:       resolved,
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

// HasBranch reports whether any agent needs the branch section rendered.
func (d agentToolDescription) HasBranch() bool {
	for _, a := range d.Agents {
		if a.Branch {
			return true
		}
	}
	return false
}

type agentToolDescriptionAgent struct {
	ID          string
	Description string
	// Branch marks an agent that hands the conversation to the user
	// instead of running on its own. The template lists these apart:
	// dispatched as an ordinary subagent, one would look to the model
	// like a call that simply never returns.
	Branch bool
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
