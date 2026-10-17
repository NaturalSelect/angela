package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
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

// reportBannerOpen starts the banner that ReportBanner puts in front of a
// dispatch's output, and that Report.Body looks for to take it off again.
const reportBannerOpen = "[report id="

// reportBannerEnd closes the banner. The banner never contains a blank
// line itself, because the task is quoted and so has no raw newlines.
const reportBannerEnd = "\n\n"

// Report is one completed agent dispatch and the text it produced.
type Report struct {
	ID        string
	AgentType string
	Task      string
	Content   string
}

// Body returns the text the dispatch produced, without the banner that
// ReportBanner put in front of it. Content stored before the banner
// existed has none and is returned as is.
func (r Report) Body() string {
	if !strings.HasPrefix(r.Content, reportBannerOpen+r.ID) {
		return r.Content
	}
	_, body, found := strings.Cut(r.Content, reportBannerEnd)
	if !found {
		return r.Content
	}
	return body
}

// ReportBanner is the header that stamps a dispatch's output with the
// handle that loads it back after a compaction drops it from the
// conversation.
func ReportBanner(reportID, agentType, task string) string {
	var banner strings.Builder
	fmt.Fprintf(&banner, "%s%s agent=%s", reportBannerOpen, reportID, agentType)
	if task != "" {
		fmt.Fprintf(&banner, " task=%q", task)
	}
	banner.WriteString("]\nStored in full. If a later compaction drops this text, call ")
	banner.WriteString(toolnames.LoadReport)
	banner.WriteString(" with this id to get it back verbatim. Do not quote this banner back to the user.")
	banner.WriteString(reportBannerEnd)
	return banner.String()
}

// bannerReportID returns the id ReportBanner stamped at the front of
// content. The stamped id is the one the model was shown, so it stays
// valid in a session whose message ids were rewritten by a fork.
func bannerReportID(content string) (string, bool) {
	rest, ok := strings.CutPrefix(content, reportBannerOpen)
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, " ")
	if !strings.HasPrefix(id, reportIDPrefix) || len(id) != len(reportIDPrefix)+reportIDHashLen {
		return "", false
	}
	return id, true
}

// agentCallInput mirrors the fields of the agent tool's parameters that
// identify a dispatch. It is redeclared here because internal/agent
// imports this package, so the original type cannot be imported back.
type agentCallInput struct {
	Description  string `json:"description"`
	SubagentType string `json:"subagent_type"`
}

// AgentToolSessionNamer names the sub-session of an agent dispatch. Both
// the session service and a UI workspace satisfy it, so either can derive
// report handles.
type AgentToolSessionNamer interface {
	CreateAgentToolSessionID(messageID, toolCallID string) string
}

// ReportID derives the stable handle for the dispatch made by toolCallID
// on messageID. It hashes the sub-session id so the handle changes
// whenever the dispatch it names does, and stays the same across
// restarts.
func ReportID(sessions AgentToolSessionNamer, messageID, toolCallID string) string {
	return reportIDPrefix + dispatchHash(sessions, messageID, toolCallID)
}

// CollectReports returns every successful agent dispatch in sessionID and
// in the sessions it descends from, oldest first. It reads each session's
// own tool-result rows rather than the sub-sessions, so a caller reaches
// only reports from its own conversation and the ones it was forked from.
//
// The ancestors are needed because a fork copies a compacted parent's
// messages only from its summary on. The summary still names reports from
// before that point, and the parent's rows are the only place their text
// survives.
func CollectReports(ctx context.Context, sessions session.Service, messages message.Service, sessionID string) ([]Report, error) {
	var lineage []string
	for id := sessionID; id != ""; {
		lineage = append(lineage, id)
		sess, err := sessions.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get session %s: %w", id, err)
		}
		id = sess.ParentSessionID
	}

	var reports []Report
	seen := make(map[string]bool)
	for _, id := range slices.Backward(lineage) {
		msgs, err := messages.List(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("list messages of %s: %w", id, err)
		}
		for _, report := range ReportsFromMessages(sessions, msgs) {
			// A fork copies the messages after its summary, banner and all,
			// so the same report turns up in the branch and its parent.
			if !seen[report.ID] {
				seen[report.ID] = true
				reports = append(reports, report)
			}
		}
	}
	return reports, nil
}

// ReportsFromMessages pairs each agent dispatch in msgs with its
// successful, non-empty result, in conversation order. A result that
// carries a banner keeps the id on it; only one stored before banners
// existed falls back to the id derived from its message.
//
// A background dispatch has no result of its own: the launch result is
// only a receipt, and the output arrives later in a completion notice.
// The report is taken from that notice and sits where the dispatch was
// made.
func ReportsFromMessages(sessions AgentToolSessionNamer, msgs []message.Message) []Report {
	dispatches := make(map[string]Report)
	backgroundCalls := make(map[string]string)
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

	for _, msg := range msgs {
		for _, result := range msg.ToolResults() {
			dispatch, ok := dispatches[result.ToolCallID]
			if !ok || result.IsError || result.Content == "" {
				continue
			}
			if meta := ParseAgentResponseMetadata(result.Metadata); meta.Background {
				backgroundCalls[meta.AgentID] = result.ToolCallID
				continue
			}
			dispatch.Content = result.Content
			if id, ok := bannerReportID(result.Content); ok {
				dispatch.ID = id
			}
			dispatches[result.ToolCallID] = dispatch
		}
	}

	for _, msg := range msgs {
		if !msg.IsReminder() {
			continue
		}
		for _, notice := range ParseBackgroundAgentNotices(msg.Content().Text) {
			callID, ok := backgroundCalls[notice.AgentID]
			if !ok || notice.Status != BackgroundAgentCompleted {
				continue
			}
			id, ok := bannerReportID(notice.Result)
			if !ok {
				continue
			}
			dispatch := dispatches[callID]
			dispatch.ID = id
			dispatch.Content = notice.Result
			dispatches[callID] = dispatch
		}
	}

	reports := make([]Report, 0, len(order))
	for _, callID := range order {
		if report := dispatches[callID]; report.Content != "" {
			reports = append(reports, report)
		}
	}
	return reports
}

type LoadReportParams struct {
	ID string `json:"id" description:"The id of the report to load, as shown in its header or in the compaction summary"`
}

func NewLoadReportTool(sessions session.Service, messages message.Service) fantasy.AgentTool {
	return NewTool(
		toolnames.LoadReport,
		loadReportDescription,
		func(ctx context.Context, params LoadReportParams, call fantasy.ToolCall) Result {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for loading a report")
			}

			reports, err := CollectReports(ctx, sessions, messages, sessionID)
			if err != nil {
				return FailErr("failed to collect reports", err)
			}

			wanted := strings.TrimSpace(params.ID)
			for _, report := range reports {
				if report.ID == wanted {
					return Ok(report.Content)
				}
			}
			return Fail(unknownReportMessage(wanted, reports))
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
