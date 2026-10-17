package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

const agentIDPrefix = "agt_"

const (
	noticeOpen  = "<agent-notification>"
	noticeClose = "</agent-notification>"
)

// AgentID derives the handle of the background agent launched by toolCallID
// on messageID. It shares ReportID's hash so the two handles of one
// dispatch correlate, and it survives restarts for the same reason.
func AgentID(sessions AgentToolSessionNamer, messageID, toolCallID string) string {
	return agentIDPrefix + dispatchHash(sessions, messageID, toolCallID)
}

func dispatchHash(sessions AgentToolSessionNamer, messageID, toolCallID string) string {
	hash := session.HashID(sessions.CreateAgentToolSessionID(messageID, toolCallID))
	return hash[:reportIDHashLen]
}

// IsAgentID reports whether id has the shape AgentID produces.
func IsAgentID(id string) bool {
	hash, ok := strings.CutPrefix(id, agentIDPrefix)
	return ok && len(hash) == reportIDHashLen
}

// AgentResponseMetadata is the metadata of an Agent tool result. Only a
// background launch sets it.
type AgentResponseMetadata struct {
	Background bool   `json:"background,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

// ParseAgentResponseMetadata decodes a stored tool result's metadata. A
// result without metadata, or with another tool's, decodes to the zero
// value.
func ParseAgentResponseMetadata(raw string) AgentResponseMetadata {
	var meta AgentResponseMetadata
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &meta)
	}
	return meta
}

type BackgroundAgentStatus string

const (
	BackgroundAgentRunning   BackgroundAgentStatus = "running"
	BackgroundAgentCompleted BackgroundAgentStatus = "completed"
	BackgroundAgentFailed    BackgroundAgentStatus = "failed"
	BackgroundAgentStopped   BackgroundAgentStatus = "stopped"
)

// Terminal reports whether the status is one a notification can carry.
func (s BackgroundAgentStatus) Terminal() bool {
	switch s {
	case BackgroundAgentCompleted, BackgroundAgentFailed, BackgroundAgentStopped:
		return true
	}
	return false
}

// BackgroundAgentNotice is the terminal notification of one background
// agent.
type BackgroundAgentNotice struct {
	AgentID string
	Status  BackgroundAgentStatus
	Summary string
	// Result is the ReportBanner plus the agent's output when the agent
	// completed, and the error text otherwise. It may be empty.
	Result string
}

// noticeTagPattern matches every tag the notice format is built from, so
// text placed inside a notice cannot close or forge one.
var noticeTagPattern = regexp.MustCompile(`(?i)</?(agent-notification|agent-id|status|summary|result)>`)

func escapeNoticeText(text string) string {
	text = noticeTagPattern.ReplaceAllStringFunc(text, func(tag string) string {
		return "&lt;" + strings.Trim(tag, "<>") + "&gt;"
	})
	return reminder.Escape(text)
}

// RenderBackgroundAgentNotice renders one notice. The caller wraps the
// joined notices in a reminder.
func RenderBackgroundAgentNotice(n BackgroundAgentNotice) string {
	var b strings.Builder
	b.WriteString(noticeOpen + "\n")
	fmt.Fprintf(&b, "<agent-id>%s</agent-id>\n", n.AgentID)
	fmt.Fprintf(&b, "<status>%s</status>\n", n.Status)
	fmt.Fprintf(&b, "<summary>%s</summary>\n", escapeNoticeText(n.Summary))
	if n.Result != "" {
		fmt.Fprintf(&b, "<result>\n%s\n</result>\n", escapeNoticeText(n.Result))
	}
	b.WriteString(noticeClose)
	return b.String()
}

// BackgroundAgentLaunchReceipt is the tool result of a background launch.
// It carries no output, so it says outright that the model knows nothing
// yet: a model left to fill that silence reports results it never saw.
func BackgroundAgentLaunchReceipt(agentID string) string {
	var b strings.Builder
	b.WriteString("Async agent launched successfully. (This tool result is internal metadata — never quote or paste any part of it, including the agentId below, into a user-facing reply.)\n")
	fmt.Fprintf(&b, "agentId: %s (internal ID - do not mention to user. Pass it as agent_id to %s to stop this agent.)\n", agentID, toolnames.AgentStop)
	b.WriteString("The agent is working in the background. You will be notified automatically when it completes. You know nothing about its results until that notification arrives — do not report, assume, or predict them; continue other work or respond to the user in the meantime.\n")
	b.WriteString("Do not duplicate this agent's work — avoid working with the same files or topics it is using. If the user asks for progress, say the agent is still running; you'll get a completion notification.")
	return b.String()
}

// BackgroundAgentRecord is what a transcript still says about one background
// agent once the process that ran it is gone.
type BackgroundAgentRecord struct {
	AgentID   string
	AgentType string
	Task      string
	// Notice is the agent's terminal notice, or nil when none was delivered.
	Notice *BackgroundAgentNotice
	// ReportID names the stored report of a completed agent, or is empty.
	ReportID string
}

// FindBackgroundAgent looks agentID up in msgs: the launch result that
// carries it, the dispatch that made the launch, and the notice that ended
// it. It reports false when msgs holds no launch of that agent.
func FindBackgroundAgent(msgs []message.Message, agentID string) (BackgroundAgentRecord, bool) {
	var callID string
	for _, msg := range msgs {
		for _, result := range msg.ToolResults() {
			if result.IsError {
				continue
			}
			if meta := ParseAgentResponseMetadata(result.Metadata); meta.Background && meta.AgentID == agentID {
				callID = result.ToolCallID
			}
		}
	}
	if callID == "" {
		return BackgroundAgentRecord{}, false
	}

	record := BackgroundAgentRecord{AgentID: agentID, AgentType: config.AgentExplore}
	for _, msg := range msgs {
		for _, call := range msg.ToolCalls() {
			if call.ID != callID {
				continue
			}
			var input agentCallInput
			_ = json.Unmarshal([]byte(call.Input), &input)
			record.Task = input.Description
			if input.SubagentType != "" {
				record.AgentType = input.SubagentType
			}
		}
	}

	for _, msg := range msgs {
		if !msg.IsReminder() {
			continue
		}
		for _, notice := range ParseBackgroundAgentNotices(msg.Content().Text) {
			if notice.AgentID != agentID {
				continue
			}
			record.Notice = &notice
			record.ReportID = ""
			if notice.Status == BackgroundAgentCompleted {
				record.ReportID, _ = bannerReportID(notice.Result)
			}
		}
	}
	return record, true
}

// ParseBackgroundAgentNotices returns every well-formed notice in text, in
// order. A block with an unknown status or a malformed id is skipped.
func ParseBackgroundAgentNotices(text string) []BackgroundAgentNotice {
	var notices []BackgroundAgentNotice
	for {
		_, afterOpen, found := strings.Cut(text, noticeOpen)
		if !found {
			return notices
		}
		block, rest, found := strings.Cut(afterOpen, noticeClose)
		if !found {
			return notices
		}
		text = rest
		if notice, ok := parseNoticeBlock(block); ok {
			notices = append(notices, notice)
		}
	}
}

func parseNoticeBlock(block string) (BackgroundAgentNotice, bool) {
	id, _ := tagValue(block, "agent-id")
	status, _ := tagValue(block, "status")
	summary, _ := tagValue(block, "summary")
	result, _ := tagValue(block, "result")

	notice := BackgroundAgentNotice{
		AgentID: strings.TrimSpace(id),
		Status:  BackgroundAgentStatus(strings.TrimSpace(status)),
		Summary: strings.TrimSpace(summary),
		Result:  strings.TrimSuffix(strings.TrimPrefix(result, "\n"), "\n"),
	}
	if !IsAgentID(notice.AgentID) || !notice.Status.Terminal() {
		return BackgroundAgentNotice{}, false
	}
	return notice, true
}

// tagValue returns the text between the first <tag> and the </tag> after
// it. Escaping guarantees that a field's content never holds either tag,
// so the first pair is the real one.
func tagValue(block, tag string) (string, bool) {
	_, afterOpen, found := strings.Cut(block, "<"+tag+">")
	if !found {
		return "", false
	}
	value, _, found := strings.Cut(afterOpen, "</"+tag+">")
	return value, found
}
