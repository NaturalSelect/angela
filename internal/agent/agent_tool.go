package agent

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed templates/agent_tool.md.tpl
var agentToolDescriptionTmpl string

type AgentParams struct {
	Description  string `json:"description,omitempty" description:"A short (3-5 words) description of the task"`
	Prompt       string `json:"prompt" description:"The task for the agent to perform. For a branch agent, keep it short — state the decision or question, not background it already has."`
	SubagentType string `json:"subagent_type,omitempty" description:"The type of specialized agent to use for this task"`
}

// agentTool builds the agent dispatch tool from the coordinator's
// subagent registry. The registry holds config snapshots, not built
// agents: each subagent is constructed on its first dispatch, so a
// session that never delegates pays nothing, and a subagent that fails
// to build only fails its own call.
//
// depth is the dispatch depth of the agent this tool instance belongs
// to; a dispatch through it runs the new subagent at depth+1.
//
// allowed is the dispatching agent's own config.Agent.AllowedAgents:
// nil or ToolSetAll means every dispatchable agent is available, and
// ToolSetScope (including an empty Agents list) narrows both the
// description and the dispatch itself to those IDs. Filtering the
// description here rather than only refusing in the run closure keeps
// it honest about what a call can actually reach.
func (c *coordinator) agentTool(depth int, allowed *config.AllowedAgentSet) (fantasy.AgentTool, error) {
	metadata := allowedAgentsOnly(c.subagents.Metadata(), allowed)
	if len(metadata) == 0 {
		return nil, nil
	}

	description, err := renderAgentToolDescription(metadata)
	if err != nil {
		return nil, fmt.Errorf("render agent tool description: %w", err)
	}

	availableIDs := make([]string, len(metadata))
	for i, a := range metadata {
		availableIDs[i] = a.ID
	}

	return tools.NewParallelTool(
		toolnames.Agent,
		description,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) tools.Result {
			if params.Prompt == "" {
				return tools.Fail("prompt is required")
			}

			// A call with no subagent_type is almost always a search
			// need, and explore is read-only, so it is the safe default.
			agentType := params.SubagentType
			if agentType == "" {
				agentType = config.AgentExplore
			}

			entry, ok := c.subagents.Get(agentType)
			if !ok {
				return tools.Failf("Unknown subagent_type %q. Available types: %s",
					agentType, strings.Join(availableIDs, ", "))
			}

			if !allowed.Allows(agentType) {
				return tools.Failf("Agent %q is not available from here. Available: %s",
					agentType, strings.Join(availableIDs, ", "))
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return tools.Fail("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return tools.Fail("agent message id missing from context")
			}

			// Building the agent can fail on a bad prompt template or
			// an unreachable provider. That is this dispatch's problem
			// alone, so it comes back as a tool error rather than
			// taking down the coordinator.
			agent, resolved, err := c.dispatchSubAgent(ctx, entry, depth+1)
			if err != nil {
				slog.Error("Failed to build subagent", "agent", agentType, "error", err)
				return tools.Failf("Subagent %q is unavailable: %v", agentType, err)
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
					return tools.Fail(refusal)
				}
				run = c.runBranchAgent
			}

			result := run(ctx, subAgentParams{
				Agent:          agent,
				Resolved:       resolved,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   title,
			})
			reportID := tools.ReportID(c.sessions, agentMessageID, call.ID)
			return tools.FromResponse(withReportHeader(result.Response(), reportID, agentType, params.Description))
		},
	), nil
}

// withReportHeader stamps a dispatch's output with the handle that loads
// it back after a compaction drops it from the conversation. A failed or
// empty dispatch is left alone: there is nothing worth reloading, and the
// banner would only dress up an error as a result.
func withReportHeader(resp fantasy.ToolResponse, reportID, agentType, task string) fantasy.ToolResponse {
	if resp.IsError || resp.Content == "" {
		return resp
	}

	var banner strings.Builder
	fmt.Fprintf(&banner, "[report id=%s agent=%s", reportID, agentType)
	if task != "" {
		fmt.Fprintf(&banner, " task=%q", task)
	}
	banner.WriteString("]\nStored in full. If a later compaction drops this text, call ")
	banner.WriteString(toolnames.LoadReport)
	banner.WriteString(" with this id to get it back verbatim. Do not quote this banner back to the user.\n\n")

	resp.Content = banner.String() + resp.Content
	return resp
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

// HasSubagent reports whether any agent needs the "Available agent
// types:" section rendered.
func (d agentToolDescription) HasSubagent() bool {
	for _, a := range d.Agents {
		if !a.Branch {
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

// allowedAgentsOnly filters a metadata list down to the IDs allowed
// permits, for a caller whose own AllowedAgents narrows what it may
// dispatch. A nil allowed is a no-op copy, since Allows returns true
// for every ID in that case.
func allowedAgentsOnly(agents []agentToolDescriptionAgent, allowed *config.AllowedAgentSet) []agentToolDescriptionAgent {
	filtered := make([]agentToolDescriptionAgent, 0, len(agents))
	for _, a := range agents {
		if allowed.Allows(a.ID) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}
