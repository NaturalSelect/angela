package agent

import (
	"context"
	_ "embed"
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
	// Collect all tool call IDs present in assistant messages and all tool
	// result IDs present in tool messages. This lets us detect both orphaned
	// tool results (result without a call) and orphaned tool calls (call
	// without a result).
	knownToolCallIDs := make(map[string]struct{})
	knownToolResultIDs := make(map[string]struct{})
	for _, m := range msgs {
		switch m.Role {
		case message.Assistant:
			for _, tc := range m.ToolCalls() {
				knownToolCallIDs[tc.ID] = struct{}{}
			}
		case message.Tool:
			for _, tr := range m.ToolResults() {
				knownToolResultIDs[tr.ToolCallID] = struct{}{}
			}
		}
	}

	for _, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		// Assistant message without content or tool calls (cancelled before it returned anything).
		if m.Role == message.Assistant && len(m.ToolCalls()) == 0 && m.Content().Text == "" && m.ReasoningContent().String() == "" {
			continue
		}
		if m.Role == message.Tool {
			if msg, ok := filterOrphanedToolResults(m, knownToolCallIDs); ok {
				history = append(history, msg)
			}
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
			if msg, ok := syntheticToolResultsForOrphanedCalls(m, knownToolResultIDs); ok {
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

// filterOrphanedToolResults converts a tool message to a fantasy.Message,
// dropping any tool result parts whose tool_call_id has no matching tool call
// in the known set. An orphaned result causes API validation to fail on every
// subsequent turn, permanently locking the session. Returns the filtered
// message and true if at least one valid part remains.
func filterOrphanedToolResults(m message.Message, knownToolCallIDs map[string]struct{}) (fantasy.Message, bool) {
	aiMsgs := m.ToAIMessage()
	if len(aiMsgs) == 0 {
		return fantasy.Message{}, false
	}
	var validParts []fantasy.MessagePart
	for _, part := range aiMsgs[0].Content {
		tr, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
		if !ok {
			validParts = append(validParts, part)
			continue
		}
		if _, known := knownToolCallIDs[tr.ToolCallID]; known {
			validParts = append(validParts, escapeReminderTags(tr))
		} else {
			slog.Warn(
				"Dropping orphaned tool result with no matching tool call",
				"tool_call_id", tr.ToolCallID,
			)
		}
	}
	if len(validParts) == 0 {
		return fantasy.Message{}, false
	}
	msg := aiMsgs[0]
	msg.Content = validParts
	return msg, true
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

// syntheticToolResultsForOrphanedCalls returns a tool message containing
// synthetic tool results for any tool calls in the assistant message that
// have no matching result in knownToolResultIDs. LLM APIs require every
// tool_use to be immediately followed by a tool_result; an interrupted
// session can leave orphaned tool_use blocks that permanently lock the
// conversation. Returns the message and true if any synthetic results were
// produced.
//
// Interruption is derived at read time, never recorded: the absence of a
// result is itself the record. This is one of the system's two derivation
// points; the other is ExtractMessageItems, which decides the same thing
// for display.
func syntheticToolResultsForOrphanedCalls(m message.Message, knownToolResultIDs map[string]struct{}) (fantasy.Message, bool) {
	var syntheticParts []fantasy.MessagePart
	for _, tc := range m.ToolCalls() {
		if _, hasResult := knownToolResultIDs[tc.ID]; hasResult {
			continue
		}
		slog.Warn(
			"Injecting synthetic tool result for orphaned tool call",
			"tool_call_id", tc.ID,
			"tool_name", tc.Name,
		)
		syntheticParts = append(syntheticParts, fantasy.ToolResultPart{
			ToolCallID: tc.ID,
			Output: fantasy.ToolResultOutputContentError{
				Error: errors.New("tool call was interrupted before returning a result; its outcome is unknown, so do not assume it ran or did not run, and verify the current state before re-running anything with side effects"),
			},
		})
	}
	if len(syntheticParts) == 0 {
		return fantasy.Message{}, false
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleTool,
		Content: syntheticParts,
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

// hasUserTextMessage reports whether any user message in msgs contains
// text content (as opposed to only shell commands or other non-text
// parts). Persisted reminders are excluded: they carry text too, but
// are not the real user prompt title generation is waiting for.
func hasUserTextMessage(msgs []message.Message) bool {
	for _, msg := range msgs {
		if msg.Role != message.User || msg.IsReminder() {
			continue
		}
		for _, part := range msg.Parts {
			if tc, ok := part.(message.TextContent); ok && tc.Text != "" {
				return true
			}
		}
	}
	return false
}
