package message

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/stringext"
	"github.com/charmbracelet/x/ansi"
)

type MessageRole string

const (
	Assistant MessageRole = "assistant"
	User      MessageRole = "user"
	System    MessageRole = "system"
	Tool      MessageRole = "tool"
)

// mediaLoadFailedPlaceholder is the text substituted for image data that
// cannot be decoded during session replay.
const mediaLoadFailedPlaceholder = "[Image data could not be loaded]"

// MediaLoadedContent returns the generic caption used for a tool-result
// media part that has no meaningful caption of its own (e.g. the Read
// tool loading an image file). ToAIMessage compares a stored
// ToolResult.Content against this value so the generic placeholder does
// not start round-tripping as a visible Text caption on replay, while a
// real caption (e.g. from an image-generation tool) still does.
func MediaLoadedContent(mime string) string {
	return fmt.Sprintf("Loaded %s content", mime)
}

type FinishReason string

const (
	FinishReasonEndTurn   FinishReason = "end_turn"
	FinishReasonMaxTokens FinishReason = "max_tokens"
	FinishReasonToolUse   FinishReason = "tool_use"
	FinishReasonCanceled  FinishReason = "canceled"
	FinishReasonError     FinishReason = "error"
	// FinishReasonContentFilter is a provider safety/refusal stop
	// (Anthropic stop_reason=refusal, OpenAI content_filter, etc.).
	// The TUI renders this as a REFUSED banner rather than a silent
	// empty turn.
	FinishReasonContentFilter FinishReason = "content_filter"

	// Should never happen
	FinishReasonUnknown FinishReason = "unknown"
)

type ContentPart interface {
	isPart()
}

type ReasoningContent struct {
	Thinking         string                   `json:"thinking"`
	Signature        string                   `json:"signature"`
	ThoughtSignature string                   `json:"thought_signature"` // Used for google
	ToolID           string                   `json:"tool_id"`           // Used for openrouter google models
	ResponsesItems   []ResponsesReasoningItem `json:"responses_items,omitempty"`
	StartedAt        int64                    `json:"started_at,omitempty"`
	FinishedAt       int64                    `json:"finished_at,omitempty"`
}

// NOTE: fantasy's own metadata type marshals with a type wrapper that its
// UnmarshalJSON does not strip, so it comes back empty from a plain JSON round
// trip. This shape keeps the item ID and encrypted content intact in storage.
type ResponsesReasoningItem struct {
	ItemID           string   `json:"item_id"`
	EncryptedContent *string  `json:"encrypted_content,omitempty"`
	Summary          []string `json:"summary"`
}

func (i ResponsesReasoningItem) metadata() *openai.ResponsesReasoningMetadata {
	return &openai.ResponsesReasoningMetadata{
		ItemID:           i.ItemID,
		EncryptedContent: i.EncryptedContent,
		Summary:          i.Summary,
	}
}

func (tc ReasoningContent) String() string {
	return tc.Thinking
}
func (ReasoningContent) isPart() {}

func (c ReasoningContent) toAIParts() []fantasy.MessagePart {
	opts := fantasy.ProviderOptions{}
	if c.Signature != "" {
		opts[anthropic.Name] = &anthropic.ReasoningOptionMetadata{
			Signature: c.Signature,
		}
	}
	if c.ThoughtSignature != "" {
		opts[google.Name] = &google.ReasoningMetadata{
			Signature: c.ThoughtSignature,
			ToolID:    c.ToolID,
		}
	}

	var parts []fantasy.MessagePart
	for _, item := range c.ResponsesItems {
		if len(parts) == 0 {
			opts[openai.Name] = item.metadata()
			parts = append(parts, fantasy.ReasoningPart{Text: c.Thinking, ProviderOptions: opts})
			continue
		}
		parts = append(parts, fantasy.ReasoningPart{
			ProviderOptions: fantasy.ProviderOptions{openai.Name: item.metadata()},
		})
	}
	if len(parts) == 0 && c.Thinking != "" {
		parts = append(parts, fantasy.ReasoningPart{Text: c.Thinking, ProviderOptions: opts})
	}
	return parts
}

type TextContent struct {
	Text string `json:"text"`
}

func (tc TextContent) String() string {
	return tc.Text
}

func (TextContent) isPart() {}

type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (iuc ImageURLContent) String() string {
	return iuc.URL
}

func (ImageURLContent) isPart() {}

type BinaryContent struct {
	Path     string
	MIMEType string
	Data     []byte
}

func (bc BinaryContent) String(p catwalk.InferenceProvider) string {
	base64Encoded := base64.StdEncoding.EncodeToString(bc.Data)
	if p == catwalk.InferenceProviderOpenAI {
		return "data:" + bc.MIMEType + ";base64," + base64Encoded
	}
	return base64Encoded
}

func (BinaryContent) isPart() {}

type ToolCall struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Input            string `json:"input"`
	ProviderExecuted bool   `json:"provider_executed"`
	Finished         bool   `json:"finished"`
}

func (ToolCall) isPart() {}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	Data       string `json:"data"`
	MIMEType   string `json:"mime_type"`
	Metadata   string `json:"metadata"`
	IsError    bool   `json:"is_error"`
}

func (ToolResult) isPart() {}

type Finish struct {
	Reason  FinishReason `json:"reason"`
	Time    int64        `json:"time"`
	Message string       `json:"message,omitempty"`
	Details string       `json:"details,omitempty"`
	// OutputTokens is the step's completion token count, recorded via
	// SetFinishUsage once usage is known. Zero means the count
	// is unknown (e.g. messages persisted before this field existed),
	// not that the step produced no output.
	OutputTokens int64 `json:"output_tokens,omitempty"`
	// GenDurationMs is the number of milliseconds from when the model
	// request was sent to when the model's stream finished, excluding
	// tool execution time. 0 means unknown (older messages, cancellations,
	// errors, or summary messages that never went through OnStepFinish).
	GenDurationMs int64 `json:"gen_duration_ms,omitempty"`
	// InputTokens is the step's uncached prompt token count, recorded via
	// SetFinishCacheUsage once usage is known. Zero means the count is
	// unknown (older messages, or steps with only estimated usage).
	InputTokens int64 `json:"input_tokens,omitempty"`
	// CacheReadTokens is the step's prompt tokens served from cache,
	// recorded via SetFinishCacheUsage. Zero means unknown or none.
	CacheReadTokens int64 `json:"cache_read_tokens,omitempty"`
	// CacheCreationTokens is the step's prompt tokens written to cache,
	// recorded via SetFinishCacheUsage. Zero means unknown or none.
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}

func (Finish) isPart() {}

// ShellCommand stores a bang-mode shell command and its output as a
// distinct content part so it can be reconstructed on session restore.
type ShellCommand struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func (ShellCommand) isPart() {}

// HasShellCommand reports whether the message contains any ShellCommand parts.
func (m *Message) HasShellCommand() bool {
	for _, part := range m.Parts {
		if _, ok := part.(ShellCommand); ok {
			return true
		}
	}
	return false
}

// ShellCommands returns all ShellCommand parts from the message.
func (m *Message) ShellCommands() []ShellCommand {
	var cmds []ShellCommand
	for _, part := range m.Parts {
		if sc, ok := part.(ShellCommand); ok {
			cmds = append(cmds, sc)
		}
	}
	return cmds
}

// IsReminder reports whether m is a system reminder persisted as an
// ordinary User-role message (so it keeps a stable position in the
// transcript for prompt caching) rather than real user input. Create
// always appends a Finish part to non-assistant messages, so that part
// is ignored; any other part (an attachment, a shell command, ...)
// means this is real user content.
func (m *Message) IsReminder() bool {
	if m.Role != User {
		return false
	}
	var text string
	textParts := 0
	for _, part := range m.Parts {
		switch p := part.(type) {
		case TextContent:
			text = p.Text
			textParts++
		case Finish:
			// Always present on persisted non-assistant messages; not content.
		default:
			return false
		}
	}
	return textParts == 1 && reminder.IsWrapped(text)
}

type Message struct {
	ID               string
	Role             MessageRole
	SessionID        string
	Parts            []ContentPart
	Model            string
	Provider         string
	Agent            string
	CreatedAt        int64
	UpdatedAt        int64
	IsSummaryMessage bool
}

func (m *Message) Content() TextContent {
	for _, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			return c
		}
	}
	return TextContent{}
}

func (m *Message) ReasoningContent() ReasoningContent {
	for _, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			return c
		}
	}
	return ReasoningContent{}
}

func (m *Message) ImageURLContent() []ImageURLContent {
	imageURLContents := make([]ImageURLContent, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ImageURLContent); ok {
			imageURLContents = append(imageURLContents, c)
		}
	}
	return imageURLContents
}

func (m *Message) BinaryContent() []BinaryContent {
	binaryContents := make([]BinaryContent, 0)
	for _, part := range m.Parts {
		if c, ok := part.(BinaryContent); ok {
			binaryContents = append(binaryContents, c)
		}
	}
	return binaryContents
}

func (m *Message) ToolCalls() []ToolCall {
	toolCalls := make([]ToolCall, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			toolCalls = append(toolCalls, c)
		}
	}
	return toolCalls
}

func (m *Message) ToolResults() []ToolResult {
	toolResults := make([]ToolResult, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolResult); ok {
			toolResults = append(toolResults, c)
		}
	}
	return toolResults
}

func (m *Message) IsFinished() bool {
	for _, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			return true
		}
	}
	return false
}

func (m *Message) FinishPart() *Finish {
	for _, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			return &c
		}
	}
	return nil
}

func (m *Message) FinishReason() FinishReason {
	for _, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			return c.Reason
		}
	}
	return ""
}

// IsErrorLike reports whether the message finished with an error-style
// banner (a real error or a provider safety refusal). The TUI renders
// both through the same banner path.
func (m *Message) IsErrorLike() bool {
	switch m.FinishReason() {
	case FinishReasonError, FinishReasonContentFilter:
		return true
	}
	return false
}

func (m *Message) IsThinking() bool {
	if m.ReasoningContent().Thinking != "" && m.Content().Text == "" && !m.IsFinished() {
		return true
	}
	return false
}

// IsTruncated reports whether the message was cut off by the output token
// limit. The agent does not resume such a turn on its own: the user has to
// raise the model's max tokens and ask again.
func (m *Message) IsTruncated() bool {
	return m.FinishReason() == FinishReasonMaxTokens
}

// IsThinkingTruncated reports whether the message was truncated while the
// model was still reasoning, before any reply text was produced. The TUI
// words its banner differently for this case.
func (m *Message) IsThinkingTruncated() bool {
	return m.IsTruncated() &&
		m.Content().Text == "" &&
		m.ReasoningContent().Thinking != ""
}

func (m *Message) AppendContent(delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			m.Parts[i] = TextContent{Text: c.Text + delta}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, TextContent{Text: delta})
	}
}

// SetContent replaces the message's text content outright, discarding
// whatever was accumulated before it. Used to swap streamed raw output
// for just the part extracted out of a required wrapper (e.g. compact's
// <summary> tags) once the stream has finished.
func (m *Message) SetContent(text string) {
	for i, part := range m.Parts {
		if _, ok := part.(TextContent); ok {
			m.Parts[i] = TextContent{Text: text}
			return
		}
	}
	m.Parts = append(m.Parts, TextContent{Text: text})
}

// NOTE: Edits a copy of the reasoning part, so fields set by earlier
// callbacks, such as provider metadata, are never dropped by a later update.
func (m *Message) updateReasoning(fn func(c *ReasoningContent)) bool {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			fn(&c)
			m.Parts[i] = c
			return true
		}
	}
	return false
}

func (m *Message) AppendReasoningContent(delta string) {
	if m.updateReasoning(func(c *ReasoningContent) { c.Thinking += delta }) {
		return
	}
	m.Parts = append(m.Parts, ReasoningContent{
		Thinking:  delta,
		StartedAt: time.Now().Unix(),
	})
}

func (m *Message) AppendThoughtSignature(signature string, toolCallID string) {
	if m.updateReasoning(func(c *ReasoningContent) {
		c.ThoughtSignature += signature
		c.ToolID = toolCallID
	}) {
		return
	}
	m.Parts = append(m.Parts, ReasoningContent{ThoughtSignature: signature})
}

func (m *Message) AppendReasoningSignature(signature string) {
	if m.updateReasoning(func(c *ReasoningContent) { c.Signature += signature }) {
		return
	}
	m.Parts = append(m.Parts, ReasoningContent{Signature: signature})
}

func (m *Message) SetReasoningResponsesData(data *openai.ResponsesReasoningMetadata) {
	if data == nil {
		return
	}
	item := ResponsesReasoningItem{
		ItemID:           data.ItemID,
		EncryptedContent: data.EncryptedContent,
		Summary:          slices.Clone(data.Summary),
	}
	m.updateReasoning(func(c *ReasoningContent) {
		items := slices.Clone(c.ResponsesItems)
		for i, existing := range items {
			if existing.ItemID != "" && existing.ItemID == item.ItemID {
				items[i] = item
				c.ResponsesItems = items
				return
			}
		}
		c.ResponsesItems = append(items, item)
	})
}

func (m *Message) FinishThinking() {
	m.updateReasoning(func(c *ReasoningContent) {
		if c.FinishedAt == 0 {
			c.FinishedAt = time.Now().Unix()
		}
	})
}

func (m *Message) ThinkingDuration() time.Duration {
	reasoning := m.ReasoningContent()
	if reasoning.StartedAt == 0 {
		return 0
	}

	endTime := reasoning.FinishedAt
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	return time.Duration(endTime-reasoning.StartedAt) * time.Second
}

func (m *Message) FinishToolCall(toolCallID string) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == toolCallID {
				m.Parts[i] = ToolCall{
					ID:       c.ID,
					Name:     c.Name,
					Input:    c.Input,
					Finished: true,
				}
				return
			}
		}
	}
}

func (m *Message) AppendToolCallInput(toolCallID string, inputDelta string) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == toolCallID {
				m.Parts[i] = ToolCall{
					ID:       c.ID,
					Name:     c.Name,
					Input:    c.Input + inputDelta,
					Finished: c.Finished,
				}
				return
			}
		}
	}
}

func (m *Message) AddToolCall(tc ToolCall) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == tc.ID {
				m.Parts[i] = tc
				return
			}
		}
	}
	m.Parts = append(m.Parts, tc)
}

func (m *Message) SetToolCalls(tc []ToolCall) {
	// remove any existing tool call part it could have multiple
	parts := make([]ContentPart, 0)
	for _, part := range m.Parts {
		if _, ok := part.(ToolCall); ok {
			continue
		}
		parts = append(parts, part)
	}
	m.Parts = parts
	for _, toolCall := range tc {
		m.Parts = append(m.Parts, toolCall)
	}
}

func (m *Message) AddToolResult(tr ToolResult) {
	m.Parts = append(m.Parts, tr)
}

func (m *Message) SetToolResults(tr []ToolResult) {
	for _, toolResult := range tr {
		m.Parts = append(m.Parts, toolResult)
	}
}

// Clone returns a deep copy of the message with an independent Parts slice.
// This prevents race conditions when the message is modified concurrently.
func (m *Message) Clone() Message {
	clone := *m
	clone.Parts = make([]ContentPart, len(m.Parts))
	copy(clone.Parts, m.Parts)
	return clone
}

// ResetStreamedContent removes all parts that were added during streaming
// (text, reasoning, tool calls, finish) so the message is ready for a
// retry. Non-streamed parts (images, binary attachments, tool results,
// shell commands) are preserved.
func (m *Message) ResetStreamedContent() {
	kept := m.Parts[:0]
	for _, part := range m.Parts {
		switch part.(type) {
		case TextContent, ReasoningContent, ToolCall, Finish:
			// Drop streamed parts.
		default:
			kept = append(kept, part)
		}
	}
	m.Parts = kept
}

func (m *Message) AddFinish(reason FinishReason, message, details string) {
	// remove any existing finish part
	for i, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			m.Parts = slices.Delete(m.Parts, i, i+1)
			break
		}
	}
	m.Parts = append(m.Parts, Finish{Reason: reason, Time: time.Now().Unix(), Message: message, Details: details})
}

// SetFinishUsage records the step's output token count and generation
// duration on its existing Finish part. It is a no-op if the message
// has no Finish part yet (AddFinish must run first).
//
// NOTE: kept separate from AddFinish instead of adding parameters to
// it: AddFinish has a dozen call sites across cancellation, error, and
// summarization paths that have no usage data to give it, and only
// OnStepFinish (which computes usage after calling AddFinish) has this
// data.
func (m *Message) SetFinishUsage(outputTokens int64, genDuration time.Duration) {
	for i, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			c.OutputTokens = outputTokens
			c.GenDurationMs = genDuration.Milliseconds()
			m.Parts[i] = c
			return
		}
	}
}

// SetFinishCacheUsage records the step's prompt token breakdown (plain,
// cache read, cache creation) on its existing Finish part. It is a no-op
// if the message has no Finish part yet (AddFinish must run first).
//
// NOTE: kept separate from SetFinishUsage because callers with only
// estimated usage (no real cache data) must skip this without also
// skipping the output token / duration recording.
func (m *Message) SetFinishCacheUsage(inputTokens, cacheReadTokens, cacheCreationTokens int64) {
	for i, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			c.InputTokens = inputTokens
			c.CacheReadTokens = cacheReadTokens
			c.CacheCreationTokens = cacheCreationTokens
			m.Parts[i] = c
			return
		}
	}
}

func (m *Message) AddImageURL(url, detail string) {
	m.Parts = append(m.Parts, ImageURLContent{URL: url, Detail: detail})
}

func (m *Message) AddBinary(mimeType string, data []byte) {
	m.Parts = append(m.Parts, BinaryContent{MIMEType: mimeType, Data: data})
}

func PromptWithTextAttachments(prompt string, attachments []Attachment) string {
	var sb strings.Builder
	sb.WriteString(prompt)
	addedAttachments := false
	for _, content := range attachments {
		if !content.IsText() {
			continue
		}
		if !addedAttachments {
			sb.WriteString("\n<system_info>The files below have been attached by the user, consider them in your response</system_info>\n")
			addedAttachments = true
		}
		if content.FilePath != "" {
			fmt.Fprintf(&sb, "<file path='%s'>\n", content.FilePath)
		} else {
			sb.WriteString("<file>\n")
		}
		sb.WriteString("\n")
		sb.Write(content.Content)
		sb.WriteString("\n</file>\n")
	}
	return sb.String()
}

func (m *Message) ToAIMessage() []fantasy.Message {
	var messages []fantasy.Message
	switch m.Role {
	case User:
		var parts []fantasy.MessagePart
		text := strings.TrimSpace(m.Content().Text)
		var textAttachments []Attachment
		for _, content := range m.BinaryContent() {
			if !strings.HasPrefix(content.MIMEType, "text/") {
				continue
			}
			textAttachments = append(textAttachments, Attachment{
				FilePath: content.Path,
				MimeType: content.MIMEType,
				Content:  content.Data,
			})
		}
		text = PromptWithTextAttachments(text, textAttachments)
		// Include bang-mode shell commands as context for the agent.
		for _, sc := range m.ShellCommands() {
			shellText := fmt.Sprintf("$ %s\n%s\n(exit code %d)", sc.Command, ansi.Strip(sc.Output), sc.ExitCode)
			if text != "" {
				text += "\n\n" + shellText
			} else {
				text = shellText
			}
		}
		if text != "" {
			parts = append(parts, fantasy.TextPart{Text: text})
		}
		for _, content := range m.BinaryContent() {
			// skip text attachements
			if strings.HasPrefix(content.MIMEType, "text/") {
				continue
			}
			parts = append(parts, fantasy.FilePart{
				Filename:  content.Path,
				Data:      content.Data,
				MediaType: content.MIMEType,
			})
		}
		messages = append(messages, fantasy.Message{
			Role:    fantasy.MessageRoleUser,
			Content: parts,
		})
	case Assistant:
		// NOTE: Reasoning goes first, the order the providers produce it in
		// and the one OpenAI expects when it is replayed.
		parts := m.ReasoningContent().toAIParts()
		text := strings.TrimSpace(m.Content().Text)
		if text != "" {
			parts = append(parts, fantasy.TextPart{Text: text})
		}
		for _, call := range m.ToolCalls() {
			parts = append(parts, fantasy.ToolCallPart{
				ToolCallID:       call.ID,
				ToolName:         call.Name,
				Input:            call.Input,
				ProviderExecuted: call.ProviderExecuted,
			})
		}
		messages = append(messages, fantasy.Message{
			Role:    fantasy.MessageRoleAssistant,
			Content: parts,
		})
	case Tool:
		var parts []fantasy.MessagePart
		for _, result := range m.ToolResults() {
			var content fantasy.ToolResultOutputContent
			if result.IsError {
				content = fantasy.ToolResultOutputContentError{
					Error: errors.New(result.Content),
				}
			} else if result.Data != "" {
				if stringext.IsValidBase64(result.Data) {
					media := fantasy.ToolResultOutputContentMedia{
						Data:      result.Data,
						MediaType: result.MIMEType,
					}
					// Only carry the caption forward when it is real —
					// not the generic "Loaded ... content" placeholder
					// used for media with no meaningful caption (e.g.
					// the Read tool loading an image) — so replay stays
					// a no-op for that case.
					if result.Content != "" && result.Content != MediaLoadedContent(result.MIMEType) {
						media.Text = result.Content
					}
					content = media
				} else {
					content = fantasy.ToolResultOutputContentText{
						Text: mediaLoadFailedPlaceholder,
					}
				}
			} else {
				content = fantasy.ToolResultOutputContentText{
					Text: result.Content,
				}
			}
			parts = append(parts, fantasy.ToolResultPart{
				ToolCallID: result.ToolCallID,
				Output:     content,
			})
		}
		messages = append(messages, fantasy.Message{
			Role:    fantasy.MessageRoleTool,
			Content: parts,
		})
	}
	return messages
}
