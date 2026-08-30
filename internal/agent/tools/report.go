package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed report.md
var loadReportDescription string

// reportIDPrefix marks a report handle in prompt text so the model can
// tell it apart from a session or tool-call id.
const reportIDPrefix = "rpt_"

// reportIDHashLen keeps the handle short enough to survive being copied
// through a compaction summary by hand.
const reportIDHashLen = 8

// Report is one completed agent dispatch and the text it produced.
type Report struct {
	ID        string
	AgentType string
	Task      string
	Content   string
}

// agentCallInput mirrors the fields of the agent tool's parameters that
// identify a dispatch. It is redeclared here because internal/agent
// imports this package, so the original type cannot be imported back.
type agentCallInput struct {
	Description  string `json:"description"`
	SubagentType string `json:"subagent_type"`
}

// ReportID derives the stable handle for the dispatch made by toolCallID
// on messageID. It hashes the sub-session id so the handle changes
// whenever the dispatch it names does, and stays the same across
// restarts.
func ReportID(sessions session.Service, messageID, toolCallID string) string {
	hash := session.HashID(sessions.CreateAgentToolSessionID(messageID, toolCallID))
	return reportIDPrefix + hash[:reportIDHashLen]
}

// CollectReports returns every successful agent dispatch in sessionID, in
// conversation order. It reads the parent session's own tool-result rows
// rather than the sub-sessions, so a caller can only ever reach reports
// produced for the session it is running in.
func CollectReports(ctx context.Context, sessions session.Service, messages message.Service, sessionID string) ([]Report, error) {
	msgs, err := messages.List(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	dispatches := make(map[string]Report)
	var order []string
	for _, msg := range msgs {
		for _, call := range msg.ToolCalls() {
			if call.Name != toolnames.Agent {
				continue
			}
			var input agentCallInput
			// A dispatch whose arguments no longer parse is still a
			// dispatch; it just loses its label.
			_ = json.Unmarshal([]byte(call.Input), &input)
			agentType := input.SubagentType
			if agentType == "" {
				agentType = config.AgentExplore
			}
			dispatches[call.ID] = Report{
				ID:        ReportID(sessions, msg.ID, call.ID),
				AgentType: agentType,
				Task:      input.Description,
			}
			order = append(order, call.ID)
		}
	}

	reports := make([]Report, 0, len(order))
	for _, msg := range msgs {
		for _, result := range msg.ToolResults() {
			dispatch, ok := dispatches[result.ToolCallID]
			if !ok || result.IsError || result.Content == "" {
				continue
			}
			dispatch.Content = result.Content
			dispatches[result.ToolCallID] = dispatch
		}
	}
	for _, callID := range order {
		if report := dispatches[callID]; report.Content != "" {
			reports = append(reports, report)
		}
	}
	return reports, nil
}

type LoadReportParams struct {
	ID string `json:"id" description:"The id of the report to load, as shown in its header or in the compaction summary"`
}

func NewLoadReportTool(sessions session.Service, messages message.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		toolnames.LoadReport,
		loadReportDescription,
		func(ctx context.Context, params LoadReportParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for loading a report")
			}

			reports, err := CollectReports(ctx, sessions, messages, sessionID)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to collect reports: %w", err)
			}

			wanted := strings.TrimSpace(params.ID)
			for _, report := range reports {
				if report.ID == wanted {
					return fantasy.NewTextResponse(report.Content), nil
				}
			}
			return fantasy.NewTextErrorResponse(unknownReportMessage(wanted, reports)), nil
		},
	)
}

// unknownReportMessage names every report the session actually has, so a
// model that guessed an id can correct itself from the reply instead of
// guessing again.
func unknownReportMessage(wanted string, reports []Report) string {
	if len(reports) == 0 {
		return fmt.Sprintf("No report with id %q: this session has not dispatched any agent that produced a report.", wanted)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "No report with id %q. Reports available in this session:\n", wanted)
	for _, report := range reports {
		fmt.Fprintf(&b, "- %s (%s)", report.ID, report.AgentType)
		if report.Task != "" {
			fmt.Fprintf(&b, " %s", report.Task)
		}
		b.WriteString("\n")
	}
	return b.String()
}
