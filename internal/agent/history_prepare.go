package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// assistantTurns counts the assistant messages in msgs. After a summary the
// slice starts at that summary, so this doubles as the number of turns taken
// since the conversation was compacted.
func assistantTurns(msgs []message.Message) int {
	turns := 0
	for _, m := range msgs {
		if m.Role == message.Assistant {
			turns++
		}
	}
	return turns
}

// unavailableMCPServers splits the servers whose tools are missing from this
// turn into the ones that failed and the ones still starting.
func unavailableMCPServers() (failed, pending []string) {
	return splitUnavailableMCP(slices.Collect(maps.Values(mcp.GetStates())))
}

// splitUnavailableMCP formats the servers the model should know about.
// Disabled servers are left out: the user turned those off deliberately.
// Both lists are sorted because GetStates returns a map, and the reminder's
// bytes would otherwise churn from turn to turn.
func splitUnavailableMCP(servers []mcp.ClientInfo) (failed, pending []string) {
	for _, server := range servers {
		switch server.State {
		case mcp.StateError, mcp.StateNeedsAuth:
			reason := server.State.String()
			if server.Error != nil {
				reason = server.Error.Error()
			}
			failed = append(failed, server.Name+": "+reason)
		case mcp.StateStarting:
			pending = append(pending, server.Name)
		}
	}
	sort.Strings(failed)
	sort.Strings(pending)
	return failed, pending
}

// hasAgentTool reports whether the Agent tool survived this turn's tool
// filtering. An agent it was filtered out of cannot delegate, so the
// reminder that weighs delegation has nothing to say to it.
func hasAgentTool(tools []fantasy.AgentTool) bool {
	return slices.ContainsFunc(tools, func(t fantasy.AgentTool) bool {
		return t.Info().Name == toolnames.Agent
	})
}

// hasTodosTool reports whether the Todos tool survived this turn's tool
// filtering. An agent it was filtered out of has no list to track work
// in, so the reminder that nudges toward it has nothing to say.
func hasTodosTool(tools []fantasy.AgentTool) bool {
	return slices.ContainsFunc(tools, func(t fantasy.AgentTool) bool {
		return t.Info().Name == toolnames.Todos
	})
}

// turnsSinceTodosCall counts the assistant turns that have passed since the
// model last called the todos tool, and the number of assistant turns so far
// when it never has. Assistant messages are counted rather than user ones so
// the result does not shift depending on whether the current turn's user
// message has been persisted yet.
func turnsSinceTodosCall(msgs []message.Message) int {
	turns := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != message.Assistant {
			continue
		}
		for _, tc := range msgs[i].ToolCalls() {
			if tc.Name == toolnames.Todos {
				return turns
			}
		}
		turns++
	}
	return turns
}

func (a *sessionAgent) preparePrompt(msgs []message.Message, supportsImages bool, attachments ...message.Attachment) ([]fantasy.Message, []fantasy.FilePart) {
	var history []fantasy.Message
	// NOTE: Tool results are placed by the call they answer, not by where
	// they were stored. A message written while a tool was still running,
	// such as a bang-mode command, can land between a call and its result,
	// and chat-completions backends reject a tool call that is not answered
	// right after it. Collecting the results first also lets us drop
	// orphaned ones (result without a call) and fill in orphaned calls
	// (call without a result).
	toolCallCounts := make(map[string]int)
	for _, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls() {
			toolCallCounts[tc.ID]++
		}
	}
	results := make(map[string][]fantasy.ToolResultPart)
	for _, m := range msgs {
		if m.Role != message.Tool {
			continue
		}
		for _, tr := range storedToolResults(m, toolCallCounts) {
			results[tr.ToolCallID] = append(results[tr.ToolCallID], tr)
		}
	}
	// NOTE: A call can end up with two results, usually a premature
	// "interrupted" one followed by the real output. Providers can also
	// reuse an ID across calls, so only the surplus beyond the number of
	// calls with that ID is dropped, and a real result outlives an error.
	for id, rs := range results {
		for len(rs) > toolCallCounts[id] {
			slog.Warn("Dropping duplicate tool result", "tool_call_id", id)
			rs = dropSurplusToolResult(rs)
		}
		results[id] = rs
	}

	for _, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		// Assistant message without content or tool calls (cancelled before it returned anything).
		if m.Role == message.Assistant && len(m.ToolCalls()) == 0 && m.Content().Text == "" && m.ReasoningContent().String() == "" {
			continue
		}
		// Stored tool results are emitted with the assistant message that
		// made the call.
		if m.Role == message.Tool {
			continue
		}
		aiMsgs := m.ToAIMessage()
		if !supportsImages {
			for i := range aiMsgs {
				if aiMsgs[i].Role == fantasy.MessageRoleUser {
					aiMsgs[i].Content = filterFileParts(aiMsgs[i].Content)
				}
			}
		}
		history = append(history, aiMsgs...)

		if m.Role == message.Assistant {
			if msg, ok := toolResultsForCalls(m, results); ok {
				history = append(history, msg)
			}
		}
	}

	var files []fantasy.FilePart
	for _, attachment := range attachments {
		if attachment.IsText() {
			continue
		}
		if !supportsImages {
			continue
		}
		files = append(files, fantasy.FilePart{
			Filename:  attachment.FileName,
			Data:      attachment.Content,
			MediaType: attachment.MimeType,
		})
	}

	return history, files
}

// filterFileParts removes fantasy.FilePart entries from a slice of message
// parts. Used to strip image attachments from historical user messages when
// the current model does not support them.
func filterFileParts(parts []fantasy.MessagePart) []fantasy.MessagePart {
	filtered := make([]fantasy.MessagePart, 0, len(parts))
	for _, part := range parts {
		if _, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

// storedToolResults returns the tool results of a stored tool message,
// dropping any whose tool_call_id has no matching tool call in
// toolCallCounts. An orphaned result causes API validation to fail on every
// subsequent turn, permanently locking the session.
func storedToolResults(m message.Message, toolCallCounts map[string]int) []fantasy.ToolResultPart {
	aiMsgs := m.ToAIMessage()
	if len(aiMsgs) == 0 {
		return nil
	}
	var valid []fantasy.ToolResultPart
	for _, part := range aiMsgs[0].Content {
		tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
		if !ok {
			continue
		}
		if toolCallCounts[tr.ToolCallID] == 0 {
			slog.Warn(
				"Dropping orphaned tool result with no matching tool call",
				"tool_call_id", tr.ToolCallID,
			)
			continue
		}
		valid = append(valid, escapeReminderTags(tr))
	}
	return valid
}

// dropSurplusToolResult removes one result from the results stored for a
// single tool call. The first error goes, so a real result survives; with
// no error among them the last goes, keeping the first.
func dropSurplusToolResult(rs []fantasy.ToolResultPart) []fantasy.ToolResultPart {
	drop := len(rs) - 1
	if i := slices.IndexFunc(rs, func(tr fantasy.ToolResultPart) bool {
		_, isErr := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](tr.Output)
		return isErr
	}); i >= 0 {
		drop = i
	}
	return slices.Delete(rs, drop, drop+1)
}

// escapeReminderTags neutralizes reminder tags in a tool's output. File
// contents and command output are untrusted, so without this a result could
// close the reminder block and pose as a reminder itself. Only the payload
// sent to the model is rewritten; the stored message keeps its exact bytes.
func escapeReminderTags(tr fantasy.ToolResultPart) fantasy.ToolResultPart {
	switch out := tr.Output.(type) {
	case fantasy.ToolResultOutputContentText:
		out.Text = reminder.Escape(out.Text)
		tr.Output = out
	case fantasy.ToolResultOutputContentError:
		tr.Output = fantasy.ToolResultOutputContentError{
			Error: errors.New(reminder.Escape(out.Error.Error())),
		}
	}
	return tr
}

// toolResultsForCalls returns the tool message that answers the tool calls
// of an assistant message: one result per call, in call order. A call takes
// the next stored result with its ID from results, and gets a synthetic one
// when none is left. LLM APIs require every tool_use to be immediately
// followed by its tool_result; an interrupted session can leave orphaned
// tool_use blocks that permanently lock the conversation. Results are
// consumed from results, so each is emitted once even when a provider reuses
// an ID. Returns the message and true if the assistant message has any
// tool call.
//
// Interruption is derived at read time, never recorded: the absence of a
// result is itself the record. This is one of the system's two derivation
// points; the other is ExtractMessageItems, which decides the same thing
// for display.
func toolResultsForCalls(m message.Message, results map[string][]fantasy.ToolResultPart) (fantasy.Message, bool) {
	var parts []fantasy.MessagePart
	for _, tc := range m.ToolCalls() {
		if queue := results[tc.ID]; len(queue) > 0 {
			parts = append(parts, queue[0])
			results[tc.ID] = queue[1:]
			continue
		}
		slog.Warn(
			"Injecting synthetic tool result for orphaned tool call",
			"tool_call_id", tc.ID,
			"tool_name", tc.Name,
		)
		parts = append(parts, fantasy.ToolResultPart{
			ToolCallID: tc.ID,
			Output: fantasy.ToolResultOutputContentError{
				Error: errors.New("tool call was interrupted before returning a result; its outcome is unknown, so do not assume it ran or did not run, and verify the current state before re-running anything with side effects"),
			},
		})
	}
	if len(parts) == 0 {
		return fantasy.Message{}, false
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleTool,
		Content: parts,
	}, true
}

func (a *sessionAgent) getSessionMessages(ctx context.Context, session session.Session) ([]message.Message, error) {
	msgs, err := a.messages.List(ctx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}

	// NOTE: A summary message the session never adopted (a rejected,
	// truncated or still-streaming attempt) stays stored so the UI can
	// show its error, but its text must never reach the model.
	msgs = slices.DeleteFunc(msgs, func(msg message.Message) bool {
		return msg.IsSummaryMessage && msg.ID != session.SummaryMessageID
	})

	if session.SummaryMessageID != "" {
		summaryMsgIndex := -1
		for i, msg := range msgs {
			if msg.ID == session.SummaryMessageID {
				summaryMsgIndex = i
				break
			}
		}
		if summaryMsgIndex != -1 {
			msgs = msgs[summaryMsgIndex:]
			msgs[0].Role = message.User
		}
	}
	return msgs, nil
}

// hasUserTextMessage reports whether any user message in msgs offers a
// title text: typed text or a text attachment, as opposed to only shell
// commands or images. Persisted reminders are excluded: they carry text
// too, but are not the real user prompt title generation is waiting for.
func hasUserTextMessage(msgs []message.Message) bool {
	for _, msg := range msgs {
		if msg.Role != message.User || msg.IsReminder() {
			continue
		}
		if messageTitleText(msg) != "" {
			return true
		}
	}
	return false
}
