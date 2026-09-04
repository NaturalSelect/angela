package chat

import (
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// ClearItemCaches
// -----------------------------------------------------------------------------

func TestClearItemCachesBumpsVersionAndClearsCache(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Content: "hi"}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)
	_ = item.Render(80)

	before := item.Version()
	ClearItemCaches([]MessageItem{item})
	require.Greater(t, item.Version(), before, "ClearItemCaches must bump the item version")

	// A version bump forces a fresh render rather than a stale cache hit.
	out := ansi.Strip(item.Render(80))
	require.Contains(t, out, toolnames.View)
}

// -----------------------------------------------------------------------------
// BuildToolResultMap
// -----------------------------------------------------------------------------

func TestBuildToolResultMap(t *testing.T) {
	t.Parallel()

	messages := []*message.Message{
		{
			ID:   "assistant-1",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{ID: "tc1", Name: "bash"},
			},
		},
		{
			ID:   "tool-1",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "tc1", Content: "result one"},
				message.ToolResult{ToolCallID: "", Content: "should be skipped"},
			},
		},
	}

	resultMap := BuildToolResultMap(messages)
	require.Len(t, resultMap, 1)
	require.Equal(t, "result one", resultMap["tc1"].Content)
}

func TestBuildToolResultMapNoToolMessagesReturnsEmptyMap(t *testing.T) {
	t.Parallel()

	messages := []*message.Message{
		{ID: "u1", Role: message.User},
		{ID: "a1", Role: message.Assistant},
	}

	resultMap := BuildToolResultMap(messages)
	require.Empty(t, resultMap)
}

// -----------------------------------------------------------------------------
// AssistantInfoItem
// -----------------------------------------------------------------------------

func TestAssistantInfoItemID(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	cfg := &config.Config{}
	msg := &message.Message{ID: "m1", Role: message.Assistant}

	item := NewAssistantInfoItem(&sty, msg, cfg, time.Unix(0, 0))

	require.Equal(t, AssistantInfoID("m1"), item.ID())
}

func TestAssistantInfoItemRawRenderWithoutFinishIsEmpty(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	cfg := &config.Config{}
	msg := &message.Message{ID: "m1", Role: message.Assistant}

	item := NewAssistantInfoItem(&sty, msg, cfg, time.Unix(0, 0))

	out := ansi.Strip(item.RawRender(80))
	require.Empty(t, out)
}

func TestAssistantInfoItemRenderShowsUnknownModelFallback(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	start := time.Unix(1000, 0)
	msg := &message.Message{
		ID:       "m1",
		Role:     message.Assistant,
		Provider: "unregistered-provider",
		Model:    "unregistered-model",
		Parts: []message.ContentPart{
			message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Unix(1005, 0).Unix()},
		},
	}

	item := NewAssistantInfoItem(&sty, msg, cfg, start)

	out := ansi.Strip(item.Render(80))
	require.Contains(t, out, "Unknown Model")
	require.Contains(t, out, "via unregistered-provider")
	require.Contains(t, out, "5s")
}

func TestAssistantInfoItemRenderShowsConfiguredModelAndProvider(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	cfg := &config.Config{
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"anthropic": {
				ID:   "anthropic",
				Name: "Anthropic",
				Models: []config.ProviderModel{
					{Model: catwalk.Model{ID: "claude", Name: "Claude Distinctive"}},
				},
			},
		}),
	}
	start := time.Unix(1000, 0)
	msg := &message.Message{
		ID:       "m1",
		Role:     message.Assistant,
		Provider: "anthropic",
		Model:    "claude",
		Parts: []message.ContentPart{
			message.Finish{Reason: message.FinishReasonEndTurn, Time: time.Unix(1002, 0).Unix()},
		},
	}

	item := NewAssistantInfoItem(&sty, msg, cfg, start)

	out := ansi.Strip(item.Render(80))
	require.Contains(t, out, "Claude Distinctive")
	require.Contains(t, out, "via Anthropic")
	require.Contains(t, out, "2s")
}

// -----------------------------------------------------------------------------
// ExtractMessageItems
// -----------------------------------------------------------------------------

func TestExtractMessageItemsUserWithShellCommandsReconstructsShellItems(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "u1",
		Role: message.User,
		Parts: []message.ContentPart{
			message.ShellCommand{Command: "echo hi", Output: "hi", ExitCode: 0},
			message.ShellCommand{Command: "echo bye", Output: "bye", ExitCode: 0},
		},
	}

	items := ExtractMessageItems(&sty, msg, nil, "", true)

	require.Len(t, items, 2)
	require.Contains(t, items[0].ID(), "echo hi")
	require.Contains(t, items[1].ID(), "echo bye")
}

func TestExtractMessageItemsUserWithoutShellCommandsReturnsUserItem(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "u1",
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello there"},
		},
	}

	items := ExtractMessageItems(&sty, msg, nil, "", true)

	require.Len(t, items, 1)
	require.Equal(t, "u1", items[0].ID())
	_, ok := items[0].(*UserMessageItem)
	require.True(t, ok, "expected a *UserMessageItem")
}

func TestExtractMessageItemsAssistantWithTextAndToolCallsAddsBoth(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "here you go"},
			message.ToolCall{ID: "tc1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true},
		},
	}
	toolResults := map[string]message.ToolResult{
		"tc1": {ToolCallID: "tc1", Content: "file contents"},
	}

	items := ExtractMessageItems(&sty, msg, toolResults, "", true)

	require.Len(t, items, 2)
	require.Equal(t, "a1", items[0].ID())
	_, ok := items[0].(*AssistantMessageItem)
	require.True(t, ok, "expected first item to be *AssistantMessageItem")
	toolItem, ok := items[1].(ToolMessageItem)
	require.True(t, ok, "expected second item to be a ToolMessageItem")
	require.Equal(t, ToolStatusSuccess, toolItem.Status())
}

func TestExtractMessageItemsAssistantToolOnlySuppressesEmptyText(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true},
		},
	}
	toolResults := map[string]message.ToolResult{
		"tc1": {ToolCallID: "tc1", Content: "file contents"},
	}

	items := ExtractMessageItems(&sty, msg, toolResults, "", true)

	require.Len(t, items, 1, "a tool-only assistant message must not render an empty text bubble")
	_, ok := items[0].(ToolMessageItem)
	require.True(t, ok, "expected the only item to be a ToolMessageItem")
}

// Missing-result cancellation derivation itself (orphaned vs. still-active
// runs) is covered by TestExtractMessageItemsMarksOrphanedCallsInterrupted
// and TestExtractMessageItemsLeavesActiveCallsRunning in orphaned_test.go;
// this test isolates the other path to canceled: an explicit
// FinishReasonCanceled, which must win even while the run is reported
// active and must also make ShouldRenderAssistantMessage render a bubble
// for the otherwise-empty message so the interruption is visible.
func TestExtractMessageItemsAssistantCanceledFinishReasonMarksToolsCanceledRegardlessOfRunActive(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "a1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true},
			message.Finish{Reason: message.FinishReasonCanceled},
		},
	}

	items := ExtractMessageItems(&sty, msg, nil, "", true)

	require.Len(t, items, 2, "a canceled finish must still render an assistant bubble alongside the tool item")
	_, ok := items[0].(*AssistantMessageItem)
	require.True(t, ok, "expected first item to be *AssistantMessageItem")
	toolItem, ok := items[1].(ToolMessageItem)
	require.True(t, ok)
	require.Equal(t, ToolStatusCanceled, toolItem.Status(),
		"an explicit cancel must mark tools canceled even if the run is reported active")
}

func TestExtractMessageItemsSystemWithContentReturnsNotice(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{
		ID:   "s1",
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: "distinctive system notice"},
		},
	}

	items := ExtractMessageItems(&sty, msg, nil, "", true)

	require.Len(t, items, 1)
	require.Contains(t, items[0].ID(), "system-notice")
}

func TestExtractMessageItemsSystemEmptyContentReturnsEmpty(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{ID: "s1", Role: message.System}

	items := ExtractMessageItems(&sty, msg, nil, "", true)

	require.Empty(t, items)
}

func TestExtractMessageItemsUnknownRoleReturnsEmpty(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	msg := &message.Message{ID: "t1", Role: message.Tool}

	items := ExtractMessageItems(&sty, msg, nil, "", true)

	require.Empty(t, items)
}
