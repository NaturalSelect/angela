package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func childAssistantMessage(parts ...message.ContentPart) pubsub.Event[message.Message] {
	return pubsub.Event[message.Message]{
		Type: pubsub.UpdatedEvent,
		Payload: message.Message{
			ID:        "child-msg",
			Role:      message.Assistant,
			SessionID: "msg-1$$call-1",
			Parts:     parts,
			CreatedAt: 1000,
		},
	}
}

func TestIsWritingText(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		msg      message.Message
		expected bool
	}{
		{
			name:     "assistant text",
			msg:      message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "Here is"}}},
			expected: true,
		},
		{
			name:     "whitespace only is not writing yet",
			msg:      message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "\n "}}},
			expected: false,
		},
		{
			name: "text beside a tool call is the tool's turn",
			msg: message.Message{Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "Let me look"},
				message.ToolCall{ID: "t1", Name: toolnames.Grep},
			}},
			expected: false,
		},
		{
			name:     "reasoning alone is thinking, not writing",
			msg:      message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.ReasoningContent{Thinking: "hmm"}}},
			expected: false,
		},
		{
			name:     "the sub-agent's prompt is not its output",
			msg:      message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "find it"}}},
			expected: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, isWritingText(tt.msg))
		})
	}
}

// Text streams in as updates to a message with no tool call. Those used to be
// dropped on arrival, leaving the block naming the last tool for the whole
// reply.
func TestChildTextStreamShowsWritingOnTheAgentBlock(t *testing.T) {
	pinTTLs(t)
	m, ws := newMockBusyUI(t)
	ws.EXPECT().ParseAgentToolSessionID("msg-1$$call-1").Return("msg-1", "call-1", true).AnyTimes()

	item := chat.NewAgentToolMessageItem(m.com.Styles, message.ToolCall{
		ID:       "call-1",
		Name:     toolnames.Agent,
		Input:    `{"prompt":"find it"}`,
		Finished: true,
	}, nil, false)
	item.SetMessageID("msg-1")
	m.chat.SetMessages(item)

	m.handleChildSessionMessage(childAssistantMessage(
		message.ToolCall{ID: "t1", Name: toolnames.Grep, Input: `{"pattern":"LoadConfig"}`},
	))
	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "LoadConfig")
	require.NotContains(t, out, "Writing...")

	m.handleChildSessionMessage(childAssistantMessage(message.TextContent{Text: "The loader lives in"}))
	out = ansi.Strip(item.Render(100))
	require.Contains(t, out, "Writing...")
	require.NotContains(t, out, "LoadConfig")
}
