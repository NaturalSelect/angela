package acp

import (
	"encoding/json"
	"strings"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/message"
)

// NOTE: The zero value is valid: an unset ToolCall means no call is found.
type DiffContext struct {
	// Cwd resolves relative tool paths; ACP wants absolute ones.
	Cwd string
	// ToolCall finds the call a tool result answers. Edit results carry no
	// file path, so the path has to come from the call input.
	ToolCall func(toolCallID string) (message.ToolCall, bool)
}

func (dc DiffContext) lookup(toolCallID string) (message.ToolCall, bool) {
	if dc.ToolCall == nil {
		return message.ToolCall{}, false
	}
	return dc.ToolCall(toolCallID)
}

// Diff turns the change between two snapshots of one message into session
// updates. Live streaming passes the last snapshot sent as prev; replaying
// history passes the zero message.
//
// NOTE: Diff(m, m, dc) is always empty, so callers can reconcile repeatedly.
func Diff(prev, next message.Message, dc DiffContext) []protocol.SessionUpdate {
	switch next.Role {
	case message.Assistant:
		return diffAssistant(prev, next, dc)
	case message.Tool:
		return diffToolResults(prev, next, dc)
	case message.User:
		return diffUser(prev, next)
	default:
		return nil
	}
}

// indexToolCalls builds the lookup for replaying a whole history. A later
// snapshot of a call replaces an earlier one.
func indexToolCalls(msgs []message.Message) func(string) (message.ToolCall, bool) {
	calls := make(map[string]message.ToolCall)
	for _, m := range msgs {
		for _, call := range m.ToolCalls() {
			calls[call.ID] = call
		}
	}
	return func(id string) (message.ToolCall, bool) {
		call, ok := calls[id]
		return call, ok
	}
}

// NOTE: Editors cannot retract a chunk. A rewritten text is therefore sent
// whole, which duplicates what was shown, and a shortened text sends nothing.
func textDelta(sent, next string) string {
	if strings.HasPrefix(sent, next) {
		return ""
	}
	if appended, ok := strings.CutPrefix(next, sent); ok {
		return appended
	}
	return next
}

func diffAssistant(prev, next message.Message, dc DiffContext) []protocol.SessionUpdate {
	prevCalls := make(map[string]message.ToolCall)
	for _, call := range prev.ToolCalls() {
		prevCalls[call.ID] = call
	}

	var updates []protocol.SessionUpdate
	for _, part := range next.Parts {
		switch p := part.(type) {
		case message.ReasoningContent:
			if delta := textDelta(prev.ReasoningContent().Thinking, p.Thinking); delta != "" {
				updates = append(updates, protocol.AgentThoughtChunk{
					MessageID: next.ID,
					Content:   protocol.TextBlock{Text: delta},
				})
			}
		case message.TextContent:
			if delta := textDelta(prev.Content().Text, p.Text); delta != "" {
				updates = append(updates, textChunk(next, delta))
			}
		case message.ToolCall:
			old, seen := prevCalls[p.ID]
			switch {
			case !seen:
				updates = append(updates, toolCallStart(p, dc.Cwd))
			case p.Finished && !old.Finished:
				updates = append(updates, toolCallRunning(p, dc.Cwd))
			}
		}
	}
	return updates
}

// NOTE: Compaction summaries go out as thoughts so the editor folds them
// away instead of mixing them into the answer.
func textChunk(m message.Message, text string) protocol.SessionUpdate {
	block := protocol.TextBlock{Text: text}
	if m.IsSummaryMessage {
		return protocol.AgentThoughtChunk{MessageID: m.ID, Content: block}
	}
	return protocol.AgentMessageChunk{MessageID: m.ID, Content: block}
}

func toolCallStart(call message.ToolCall, cwd string) protocol.ToolCallStart {
	presentation := presentTool(call, cwd)
	start := protocol.ToolCallStart{
		ID:        call.ID,
		Name:      call.Name,
		Title:     presentation.title,
		Kind:      presentation.kind,
		Status:    protocol.ToolCallStatusPending,
		Locations: presentation.locations,
	}
	if call.Finished && json.Valid([]byte(call.Input)) {
		start.RawInput = json.RawMessage(call.Input)
	}
	return start
}

// NOTE: A call whose input has finished streaming counts as running; the
// permission request, if any, arrives while it is in this state.
func toolCallRunning(call message.ToolCall, cwd string) protocol.ToolCallUpdate {
	presentation := presentTool(call, cwd)
	return protocol.ToolCallUpdate{
		ID:        call.ID,
		Title:     &presentation.title,
		Status:    new(protocol.ToolCallStatusInProgress),
		Locations: presentation.locations,
	}
}

func diffToolResults(prev, next message.Message, dc DiffContext) []protocol.SessionUpdate {
	sent := make(map[string]message.ToolResult)
	for _, r := range prev.ToolResults() {
		sent[r.ToolCallID] = r
	}

	var updates []protocol.SessionUpdate
	for _, r := range next.ToolResults() {
		if old, ok := sent[r.ToolCallID]; ok && old == r {
			continue
		}
		call, found := dc.lookup(r.ToolCallID)
		status := protocol.ToolCallStatusCompleted
		if r.IsError {
			status = protocol.ToolCallStatusFailed
		}
		updates = append(updates, protocol.ToolCallUpdate{
			ID:      r.ToolCallID,
			Status:  &status,
			Content: toolResultContent(r, call, found, dc.Cwd),
		})
	}
	return updates
}

// NOTE: User messages never change after they are created, so they are
// translated once, whole. Reminders are Angela's own bookkeeping.
func diffUser(prev, next message.Message) []protocol.SessionUpdate {
	if len(prev.Parts) > 0 || next.IsReminder() {
		return nil
	}

	var updates []protocol.SessionUpdate
	for _, part := range next.Parts {
		var block protocol.ContentBlock
		switch p := part.(type) {
		case message.TextContent:
			if p.Text != "" {
				block = protocol.TextBlock{Text: p.Text}
			}
		case message.BinaryContent:
			if strings.HasPrefix(p.MIMEType, "image/") {
				block = protocol.ImageBlock{MIMEType: p.MIMEType, Data: p.Data}
			}
		case message.ShellCommand:
			block = protocol.TextBlock{Text: "$ " + p.Command + "\n" + p.Output}
		}
		if block != nil {
			updates = append(updates, protocol.UserMessageChunk{MessageID: next.ID, Content: block})
		}
	}
	return updates
}
