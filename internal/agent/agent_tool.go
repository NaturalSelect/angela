package agent

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"slices"
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
	// RunInBackground is offered to the model only where
	// backgroundAgentsAvailable allows it; see withoutParameter.
	RunInBackground bool `json:"run_in_background,omitempty" description:"Set to true to run this agent in the background. The call returns at once and you are notified when the agent finishes. Use it only when you can keep working without the result."`
}

const runInBackgroundParameter = "run_in_background"

// withoutParameter wraps a tool so its schema does not offer one
// parameter. The tool still accepts it if a model sends it anyway.
type withoutParameter struct {
	fantasy.AgentTool
	name string
}

func (t withoutParameter) Info() fantasy.ToolInfo {
	info := t.AgentTool.Info()
	delete(info.Parameters, t.name)
	info.Required = slices.DeleteFunc(slices.Clone(info.Required), func(name string) bool { return name == t.name })
	return info
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
// caller is the dispatching agent's own config. Its AllowedAgents (nil
// or ToolSetAll means every dispatchable agent, ToolSetScope including
// an empty Agents list narrows to those IDs) and branchForkRefusal
// together decide which agents the caller can reach. That one set
// drives the description, the "Available" list in errors and the
// dispatch itself, so the description stays honest about what a call can
// actually reach. A caller that can reach none gets no tool.
//
// parent is the dispatching agent's own instance, carried so a
// dispatch whose slot is "inherited" can inherit parent's live model
// instead of the coder's.
func (c *coordinator) agentTool(depth int, caller config.Agent, parent config.ActiveAgent) (fantasy.AgentTool, error) {
	branchRefusal := c.branchForkRefusal(caller, depth)
	metadata := allowedAgentsOnly(c.subagents.Metadata(), caller.AllowedAgents)
	if branchRefusal != "" {
		metadata = slices.DeleteFunc(metadata, func(a agentToolDescriptionAgent) bool { return a.Branch })
	}
	if len(metadata) == 0 {
		return nil, nil
	}

	background := c.backgroundAgentsAvailable(caller, depth)
	description, err := renderAgentToolDescription(metadata, background)
	if err != nil {
		return nil, fmt.Errorf("render agent tool description: %w", err)
	}

	availableIDs := make([]string, len(metadata))
	for i, a := range metadata {
		availableIDs[i] = a.ID
	}

	tool := tools.NewParallelTool(
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

			if !slices.Contains(availableIDs, agentType) {
				// NOTE: A branch refused only by the fork rule says so, since
				// the model can act on that and "not available" would not tell it why.
				if branchRefusal != "" && entry.cfg.Mode == config.AgentModeBranch && caller.AllowedAgents.Allows(agentType) {
					return tools.Fail(branchRefusal)
				}
				return tools.Failf("Agent %q is not available from here. Available: %s",
					agentType, strings.Join(availableIDs, ", "))
			}

			if entry.cfg.Mode == config.AgentModeBranch && params.RunInBackground {
				return tools.Failf("Agent %q is a branch agent: it hands the conversation to the user and suspends this call, so it cannot run in the background.", agentType)
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
			agent, resolved, err := c.dispatchSubAgent(ctx, entry, depth+1, parent)
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
				run = c.runBranchAgent
			}

			subParams := subAgentParams{
				Agent:          agent,
				Resolved:       resolved,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   title,
			}
			// NOTE: A model that sends run_in_background where the schema
			// never offered it gets the result in the call instead: nothing
			// is lost, and it is not promised a notification that cannot
			// come.
			if params.RunInBackground && background {
				return c.launchBackgroundAgent(ctx, subParams, agentType, params.Description)
			}

			result := run(ctx, subParams)
			reportID := tools.ReportID(c.sessions, agentMessageID, call.ID)
			return tools.FromResponse(withReportHeader(result.Response(), reportID, agentType, params.Description))
		},
	)
	if !background {
		return withoutParameter{AgentTool: tool, name: runInBackgroundParameter}, nil
	}
	return tool, nil
}

// withReportHeader stamps a dispatch's output with the handle that loads
// it back after a compaction drops it from the conversation. A failed or
// empty dispatch is left alone: there is nothing worth reloading, and the
// banner would only dress up an error as a result.
func withReportHeader(resp fantasy.ToolResponse, reportID, agentType, task string) fantasy.ToolResponse {
	if resp.IsError || resp.Content == "" {
		return resp
	}

	resp.Content = tools.ReportBanner(reportID, agentType, task) + resp.Content
	return resp
}

// agentToolDescription is the data structure passed to the agent tool
// description template.
type agentToolDescription struct {
	Agents []agentToolDescriptionAgent
	// Background reports whether this tool offers run_in_background.
	Background bool
}

// AgentStatusTool and AgentStopTool name the tools the background section
// points at, so the prompt cannot drift from the registered names.
func (agentToolDescription) AgentStatusTool() string { return toolnames.AgentStatus }

func (agentToolDescription) AgentStopTool() string { return toolnames.AgentStop }

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

func renderAgentToolDescription(agents []agentToolDescriptionAgent, background bool) (string, error) {
	tmpl, err := template.New("agent_tool").Parse(agentToolDescriptionTmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, agentToolDescription{Agents: agents, Background: background}); err != nil {
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
