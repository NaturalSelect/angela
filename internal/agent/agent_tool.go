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
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed templates/agent_tool.md.tpl
var agentToolDescriptionTmpl string

type AgentParams struct {
	Description  string `json:"description,omitempty" description:"A short (3-5 words) description of the task"`
	Prompt       string `json:"prompt" description:"The task for the agent to perform"`
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
// branchOnly narrows both the description and the dispatch itself to
// branch-mode agents. buildTools sets it once a sub-agent has spent its
// regular delegation budget: a branch does not spend that budget
// (dispatchDepth skips the branch hop), so it is the one dispatch a
// sub-agent in that position may still make. Listing an ordinary
// subagent here without also refusing it in the run closure would let
// a caller reach one more hop than options.subagent_depth allows simply
// by asking for it.
func (c *coordinator) agentTool(depth int, branchOnly bool) (fantasy.AgentTool, error) {
	metadata := c.subagents.Metadata()
	if branchOnly {
		metadata = branchAgentsOnly(metadata)
	}
	if len(metadata) == 0 {
		return nil, nil
	}

	description, err := renderAgentToolDescription(metadata, branchOnly)
	if err != nil {
		return nil, fmt.Errorf("render agent tool description: %w", err)
	}

	branchIDs := make([]string, len(metadata))
	for i, a := range metadata {
		branchIDs[i] = a.ID
	}

	return fantasy.NewParallelAgentTool(
		toolnames.Agent,
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

			if branchOnly && entry.cfg.Mode != config.AgentModeBranch {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("This turn has no delegation budget left, so only a branch agent may be dispatched from here. Available: %s",
						strings.Join(branchIDs, ", ")),
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

			resp, err := run(ctx, subAgentParams{
				Agent:          agent,
				Resolved:       resolved,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   title,
			})
			if err != nil {
				return resp, err
			}
			reportID := tools.ReportID(c.sessions, agentMessageID, call.ID)
			return withReportHeader(resp, reportID, agentType, params.Description), nil
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
	// ForkOnly marks a description rendered for a caller with no
	// delegation budget left who may still fork a branch. The template
	// calls this out explicitly, because otherwise every branch agent it
	// lists reads as offered by choice rather than as the only option.
	ForkOnly bool
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
// types:" section rendered. It is always false once agentTool has
// filtered the list down to branchOnly.
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

func renderAgentToolDescription(agents []agentToolDescriptionAgent, forkOnly bool) (string, error) {
	tmpl, err := template.New("agent_tool").Parse(agentToolDescriptionTmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, agentToolDescription{Agents: agents, ForkOnly: forkOnly}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// branchAgentsOnly filters a metadata list down to branch-mode agents,
// for a caller that may only fork a branch rather than delegate.
func branchAgentsOnly(agents []agentToolDescriptionAgent) []agentToolDescriptionAgent {
	branches := make([]agentToolDescriptionAgent, 0, len(agents))
	for _, a := range agents {
		if a.Branch {
			branches = append(branches, a)
		}
	}
	return branches
}
