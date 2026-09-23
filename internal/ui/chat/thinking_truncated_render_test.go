package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestThinkingTruncatedRendersBanner asserts that a max-tokens finish
// reached while reasoning (before any reply text was produced) paints a
// visible TRUNCATED banner rather than an empty assistant turn. The
// agent's auto-continue resends the request in this case, but since the
// model has to restart its reasoning rather than resume it, the user
// needs to know why the turn ended without a reply.
func TestThinkingTruncatedRendersBanner(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "thinking-truncated-1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "Let me work through this step by step..."},
			message.Finish{
				Reason: message.FinishReasonMaxTokens,
				Time:   testFinishTime,
			},
		},
	}

	require.True(t, msg.IsThinkingTruncated())
	require.True(t, ShouldRenderAssistantMessage(msg),
		"empty-body truncated-thinking turns must still render so the banner is visible")

	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	out := item.Render(80)
	require.Contains(t, out, thinkingTruncatedTagLabel, "banner tag")
	require.Contains(t, out, thinkingTruncatedTitle, "title")
	require.Contains(t, strings.ToLower(out), "output token limit", "details")
}

// TestThinkingTruncatedPersistedCopyWins asserts that when a finish part
// does carry its own message/details, the persisted copy takes
// precedence over the TUI defaults, matching refusal/error banners.
func TestThinkingTruncatedPersistedCopyWins(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "thinking-truncated-2",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "Reasoning that got cut off..."},
			message.Finish{
				Reason:  message.FinishReasonMaxTokens,
				Message: "Custom truncation title",
				Details: "Custom truncation details.",
				Time:    testFinishTime,
			},
		},
	}
	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	out := item.Render(80)
	require.Contains(t, out, thinkingTruncatedTagLabel)
	require.Contains(t, out, "Custom truncation title")
	require.Contains(t, out, "Custom truncation details.")
}

// TestThinkingTruncatedNotConfusedWithPlainMaxTokens asserts that a
// max-tokens finish reached after reply text was already produced (a
// plain truncated answer, not a truncated-thinking turn) does not get
// the TRUNCATED banner treatment.
func TestThinkingTruncatedNotConfusedWithPlainMaxTokens(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "plain-max-tokens-1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "Here is the answer so far"},
			message.Finish{
				Reason: message.FinishReasonMaxTokens,
				Time:   testFinishTime,
			},
		},
	}

	require.False(t, msg.IsThinkingTruncated())

	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	out := item.Render(80)
	require.NotContains(t, out, thinkingTruncatedTagLabel)
}
