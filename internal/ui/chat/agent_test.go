package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAgentToolDescriptionInvalidInputReturnsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, agentToolDescription(message.ToolCall{Input: `not-json`}))
}

func TestAgentToolDescriptionPrefersExplicitDescription(t *testing.T) {
	t.Parallel()

	desc := agentToolDescription(message.ToolCall{
		Input: `{"description":"scout the auth package","prompt":"find how login works\nand report back"}`,
	})
	require.Equal(t, "scout the auth package", desc)
}

func TestAgentToolDescriptionFallsBackToPromptFirstLine(t *testing.T) {
	t.Parallel()

	desc := agentToolDescription(message.ToolCall{
		Input: `{"prompt":"find how login works\nand report back"}`,
	})
	require.Equal(t, "find how login works", desc)
}

func TestAgentHeaderOmitsSeparatorWhenNoDescription(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewAgentToolMessageItem(&sty, message.ToolCall{
		ID:       "agent-1",
		Name:     toolnames.Agent,
		Input:    `{}`,
		Finished: true,
	}, nil, false)
	ctx := &AgentToolRenderContext{agent: item}

	out := ansi.Strip(ctx.agentHeader(&sty, 100, &ToolRenderOpts{ToolCall: item.ToolCall(), Status: ToolStatusRunning}))
	require.NotContains(t, out, agentTitleSeparator)
}

func TestAgentHeaderDropsDescriptionWhenWidthTooNarrow(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "agent-1",
		Name:     toolnames.Agent,
		Input:    `{"subagent_type":"explore","description":"a fairly long description that will not fit"}`,
		Finished: true,
	}
	item := NewAgentToolMessageItem(&sty, toolCall, nil, false)
	ctx := &AgentToolRenderContext{agent: item}

	out := ansi.Strip(ctx.agentHeader(&sty, 5, &ToolRenderOpts{ToolCall: toolCall, Status: ToolStatusRunning}))
	require.NotContains(t, out, "fairly long description")
	require.Contains(t, out, "Agent(explore)")
}

func TestCurrentActionFallsBackToNameWhenNoTarget(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, false, message.ToolCall{ID: "t1", Name: toolnames.Todos, Input: `{}`})

	require.Equal(t, toolnames.Todos, item.currentAction())
}

func TestSetTimingDedupesIdenticalValues(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewAgentToolMessageItem(&sty, message.ToolCall{
		ID: "agent-1", Name: toolnames.Agent, Input: `{}`, Finished: true,
	}, nil, false)

	item.SetTiming(1000, 1010)
	before := item.Version()
	item.SetTiming(1000, 1010)
	require.Equal(t, before, item.Version(), "setting identical timing values must not bump the version")
}

func TestAgentRenderToolPendingWithNoNestedToolsShowsSpinnerName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "agent-1", Name: toolnames.Agent, Input: `{"subagent_type":"explore"}`}
	item := NewAgentToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Agent")
}

func TestAgentRenderToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, false, grepCall("t1", "LoadConfig"))
	compactable, ok := ToolMessageItem(item).(Compactable)
	require.True(t, ok, "agent tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, agentSummaryArrow)
}

func TestAgentRenderToolFinishedWithNoNestedToolsShowsHeaderOnly(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, agentSummaryArrow)
	require.Contains(t, out, "Agent")
}

func TestAgentRenderToolCanceledShowsDoneStyleSummaryWithoutSpinner(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "agent-1", Name: toolnames.Agent, Input: `{"prompt":"find it"}`, Finished: true}
	item := NewAgentToolMessageItem(&sty, toolCall, nil, true)
	item.SetNestedTools([]ToolMessageItem{
		NewToolMessageItem(&sty, "msg-1", grepCall("t1", "LoadConfig"), nil, false, t.TempDir()),
	})

	require.Equal(t, ToolStatusCanceled, item.Status())
	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "1 tool")
}

func TestAgentRenderToolRunningAppendsSpinnerToSummary(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, false, grepCall("t1", "LoadConfig"))

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, agentSummaryArrow)
	require.Contains(t, out, toolnames.Grep)
}

// Once the sub-agent stops calling tools and starts writing its report, the
// last tool is stale. The summary has to say so rather than keep naming it.
func TestAgentSummaryShowsWritingInsteadOfTheLastTool(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, false, grepCall("t1", "LoadConfig"))
	item.SetWriting(true)

	line := ansi.Strip(item.summaryLine(&sty, false))
	require.Contains(t, line, agentWritingLabel)
	require.NotContains(t, line, toolnames.Grep)

	item.SetWriting(false)
	require.Contains(t, ansi.Strip(item.summaryLine(&sty, false)), toolnames.Grep)
}

// A sub-agent can write before it has called any tool at all, and the block
// would otherwise sit on the bare spinner for the whole reply.
func TestAgentSummaryShowsWritingBeforeAnyToolRuns(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, false)
	item.SetWriting(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, agentSummaryArrow+agentWritingLabel)
}

// The run is over once it has a result, whatever the last message looked
// like, so the totals take over from the writing label.
func TestAgentSummaryDropsWritingOnceDone(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, true, grepCall("t1", "LoadConfig"))
	item.SetWriting(true)

	line := ansi.Strip(item.summaryLine(&sty, true))
	require.Contains(t, line, "1 tool")
	require.NotContains(t, line, agentWritingLabel)
}

// Child text arrives token by token. Re-rendering the block for every delta
// after the first would be pure churn.
func TestAgentSetWritingDedupes(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, false, grepCall("t1", "LoadConfig"))

	requireBump(t, "SetWriting[true]", item, func() { item.SetWriting(true) })
	requireNoBump(t, "SetWriting[repeat]", item, func() { item.SetWriting(true) })
	requireBump(t, "SetWriting[false]", item, func() { item.SetWriting(false) })
}
