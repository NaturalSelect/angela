package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/google/uuid"
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

// IsReportID reports whether id has the shape ReportID produces: the
// prefix followed by lowercase hex digits only.
func IsReportID(id string) bool {
	hash, ok := strings.CutPrefix(id, reportIDPrefix)
	if !ok || len(hash) != reportIDHashLen {
		return false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
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
	if !IsReportID(id) {
		return "", false
	}
	return id, true
}

// parseReportBanner reads back the fields ReportBanner wrote at the front
// of content. The task is a quoted string that may itself hold a space or
// a closing bracket, so it is unquoted rather than split off.
func parseReportBanner(content string) (id, agentType, task string, ok bool) {
	id, ok = bannerReportID(content)
	if !ok {
		return "", "", "", false
	}
	_, rest, _ := strings.Cut(content[len(reportBannerOpen):], " ")
	rest, ok = strings.CutPrefix(rest, "agent=")
	if !ok {
		return "", "", "", false
	}
	agentEnd := strings.IndexAny(rest, " ]")
	if agentEnd < 0 {
		return "", "", "", false
	}
	agentType, rest = rest[:agentEnd], rest[agentEnd:]
	if quotedTask, hasTask := strings.CutPrefix(rest, " task="); hasTask {
		quoted, err := strconv.QuotedPrefix(quotedTask)
		if err != nil {
			return "", "", "", false
		}
		task, err = strconv.Unquote(quoted)
		if err != nil {
			return "", "", "", false
		}
	}
	return id, agentType, task, true
}

// importedReportKeyPrefix keeps an imported report's key apart from the
// tool call ids that key the dispatches next to it.
const importedReportKeyPrefix = "import:"

// ImportedReportFromMessage returns the report an import stored in msg. An
// import is a system message, so it shows in the transcript but never
// reaches the model; the model reads it only through LoadReport.
func ImportedReportFromMessage(msg message.Message) (Report, bool) {
	if msg.Role != message.System {
		return Report{}, false
	}
	content := msg.Content().Text
	id, agentType, task, ok := parseReportBanner(content)
	if !ok {
		return Report{}, false
	}
	return Report{ID: id, AgentType: agentType, Task: task, Content: content}, true
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
		if imported, ok := ImportedReportFromMessage(msg); ok {
			key := importedReportKeyPrefix + msg.ID
			dispatches[key] = imported
			order = append(order, key)
		}
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

// importedAgentType labels an imported report whose file did not say which
// agent produced it.
const importedAgentType = "imported"

// ErrEmptyReport is returned when an import has no text to store.
var ErrEmptyReport = errors.New("report is empty")

// ImportResult describes what ImportReport did with a report.
type ImportResult struct {
	// Report is the report as stored, or the identical one already there.
	// Its ID is the one LoadReport takes.
	Report Report
	// RequestedID is the id the imported report carried, or "" if it had
	// none or one that is not a report id. It differs from Report.ID when
	// that id was already taken by a different report.
	RequestedID string
	// AlreadyPresent is true when the session already held this exact
	// report, so nothing was written.
	AlreadyPresent bool
}

// bannerSafeAgentType makes a label read from a file fit the banner, where
// the agent ends at the first space or closing bracket.
func bannerSafeAgentType(label string) string {
	label = strings.Join(strings.Fields(label), "-")
	return strings.ReplaceAll(label, "]", "")
}

// importedReportNotice tells the model that the user brought a report into
// the session. The stored copy is a system message the model never sees, so
// without this notice nothing would tell it that LoadReport has something
// new to load.
func importedReportNotice(report Report) string {
	var notice strings.Builder
	fmt.Fprintf(&notice, "The user imported a report into this session: id=%s agent=%s", report.ID, report.AgentType)
	if report.Task != "" {
		fmt.Fprintf(&notice, " task=%q", report.Task)
	}
	notice.WriteString(". Its text is not in this conversation. Call ")
	notice.WriteString(toolnames.LoadReport)
	notice.WriteString(" with this id when you need it.")
	return reminder.Wrap(notice.String())
}

// ImportReport stores in as a report of sessionID so that LoadReport finds
// it there and in every session that descends from it. in may hold the
// bare text or text that still carries a banner; only its body is kept.
// A reminder follows the stored report so the model learns of the import on
// its next turn.
//
// The report keeps its own id when it has a valid one that no different
// report in the session's lineage uses. Otherwise it gets a fresh id, so
// an import can never shadow a report the model already cited.
func ImportReport(ctx context.Context, sessions session.Service, messages message.Service, sessionID string, in Report) (ImportResult, error) {
	body := strings.TrimRight(in.Body(), "\n")
	if strings.TrimSpace(body) == "" {
		return ImportResult{}, ErrEmptyReport
	}
	agentType := bannerSafeAgentType(in.AgentType)
	if agentType == "" {
		agentType = importedAgentType
	}

	// The scan reads rows, so a report written a moment ago must already
	// be on disk to be seen.
	if err := messages.FlushAll(ctx); err != nil {
		return ImportResult{}, fmt.Errorf("flush messages: %w", err)
	}
	existing, err := CollectReports(ctx, sessions, messages, sessionID)
	if err != nil {
		return ImportResult{}, err
	}
	taken := make(map[string]Report, len(existing))
	for _, report := range existing {
		taken[report.ID] = report
	}

	var result ImportResult
	id := ""
	if IsReportID(in.ID) {
		result.RequestedID = in.ID
		id = in.ID
		if held, conflict := taken[id]; conflict {
			// NOTE: A dispatch's output is stored untrimmed, while body is not.
			if strings.TrimRight(held.Body(), "\n") == body {
				result.Report = held
				result.AlreadyPresent = true
				return result, nil
			}
			id = ""
		}
	}
	for id == "" || taken[id].ID != "" {
		id = reportIDPrefix + session.HashID(uuid.NewString())[:reportIDHashLen]
	}

	report := Report{
		ID:        id,
		AgentType: agentType,
		Task:      in.Task,
		Content:   ReportBanner(id, agentType, in.Task) + body,
	}
	if _, err := messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.System,
		Parts: []message.ContentPart{message.TextContent{Text: report.Content}},
	}); err != nil {
		return ImportResult{}, fmt.Errorf("store report: %w", err)
	}
	if _, err := messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: importedReportNotice(report)}},
	}); err != nil {
		return ImportResult{}, fmt.Errorf("store report notice: %w", err)
	}
	result.Report = report
	return result, nil
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
		return fmt.Sprintf("No report with id %q: this session has no reports to load.", wanted)
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
