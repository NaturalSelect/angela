package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestThinkingTruncatedRendersBanner asserts that a max-tokens finish
// reached while reasoning (before any reply text was produced) paints a
// visible TRUNCATED banner rather than an empty assistant turn. The
// agent does not resume the turn, so the user needs to know why it ended
// without a reply and that raising max tokens is the way out.
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
	out := ansi.Strip(item.Render(80))
	require.Contains(t, out, truncatedTagLabel, "banner tag")
	require.Contains(t, out, thinkingTruncatedTitle, "title")
	require.Contains(t, strings.ToLower(out), "raise max tokens", "details")
	require.NotContains(t, strings.ToLower(out), "retry automatically",
		"the agent no longer resumes a truncated turn on its own")
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
	require.Contains(t, out, truncatedTagLabel)
	require.Contains(t, out, "Custom truncation title")
	require.Contains(t, out, "Custom truncation details.")
}

// TestTextTruncatedRendersBanner asserts that a max-tokens finish
// reached after reply text was already produced keeps the partial
// answer visible and adds the TRUNCATED banner under it. Nothing
// continues the answer automatically, so without the banner the user
// would just see a reply that stops mid-sentence.
func TestTextTruncatedRendersBanner(t *testing.T) {
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

	require.True(t, msg.IsTruncated())
	require.False(t, msg.IsThinkingTruncated(),
		"a cutoff after reply text is a plain truncation, not a truncated-thinking turn")

	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	out := ansi.Strip(item.Render(80))
	require.Contains(t, out, "Here is the answer so far")
	require.Contains(t, out, truncatedTagLabel)
	require.Contains(t, out, truncatedTitle)
	require.NotContains(t, out, thinkingTruncatedTitle)
	require.Contains(t, strings.ToLower(out), "raise max tokens")
}

// TestTruncatedToolOnlyTurnRendersBanner asserts that a turn cut off
// while its tool call was still streaming, with no reply text or
// reasoning of its own, still renders so the banner is not lost.
func TestTruncatedToolOnlyTurnRendersBanner(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "tool-only-max-tokens-1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "call-1", Name: "some_tool", Input: "{}", Finished: true},
			message.Finish{
				Reason: message.FinishReasonMaxTokens,
				Time:   testFinishTime,
			},
		},
	}

	require.True(t, ShouldRenderAssistantMessage(msg),
		"a truncated turn must render even when it only holds a tool call")

	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	out := item.Render(80)
	require.Contains(t, out, truncatedTagLabel)
	require.Contains(t, out, truncatedTitle)
}

// TestNormalFinishHasNoTruncatedBanner pins that a message which ended
// normally never gets the TRUNCATED banner.
func TestNormalFinishHasNoTruncatedBanner(t *testing.T) {
	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "end-turn-1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "All done."},
			message.Finish{
				Reason: message.FinishReasonEndTurn,
				Time:   testFinishTime,
			},
		},
	}

	require.False(t, msg.IsTruncated())

	item := NewAssistantMessageItem(&sty, msg).(*AssistantMessageItem)
	out := item.Render(80)
	require.NotContains(t, out, truncatedTagLabel)
}
