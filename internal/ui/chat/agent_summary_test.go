package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// newAgentItem builds an agent block holding the given nested calls. done
// attaches a result, which is what flips the summary from "doing" to "did".
func newAgentItem(t *testing.T, done bool, calls ...message.ToolCall) *AgentToolMessageItem {
	t.Helper()
	sty := styles.CharmtonePantera()

	var result *message.ToolResult
	if done {
		result = &message.ToolResult{ToolCallID: "agent-1", Content: "found them"}
	}
	item := NewAgentToolMessageItem(&sty, message.ToolCall{
		ID:       "agent-1",
		Name:     agent.AgentToolName,
		Input:    `{"prompt":"find the config loader"}`,
		Finished: true,
	}, result, false)

	nested := make([]ToolMessageItem, 0, len(calls))
	for _, tc := range calls {
		nested = append(nested, NewToolMessageItem(&sty, "msg-1", tc, nil, false, t.TempDir()))
	}
	item.SetNestedTools(nested)
	return item
}

func globCall(id, pattern string) message.ToolCall {
	return message.ToolCall{ID: id, Name: tools.GlobToolName, Input: `{"pattern":"` + pattern + `"}`}
}

func grepCall(id, pattern string) message.ToolCall {
	return message.ToolCall{ID: id, Name: tools.GrepToolName, Input: `{"pattern":"` + pattern + `"}`}
}

// While a sub-agent runs, the block has to answer "what is it doing right
// now" — the whole point of the summary. Naming an earlier step instead
// leaves the user watching a spinner with no idea what it is waiting on.
func TestAgentSummaryNamesTheNewestTool(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, false,
		globCall("t1", "**/*.go"),
		grepCall("t2", "LoadConfig"),
	)

	line := ansi.Strip(item.summaryLine(&sty, false))
	require.Contains(t, line, tools.GrepToolName)
	require.Contains(t, line, "LoadConfig")
	require.NotContains(t, line, tools.GlobToolName,
		"the summary names the step in flight, not the one before it")
}

// Once the run is over the last step stops being interesting: the tree
// below already lists every one of them. What the user cannot get from
// the tree is the totals.
func TestAgentSummaryReportsTotalsWhenDone(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, true,
		globCall("t1", "**/*.go"),
		grepCall("t2", "LoadConfig"),
		grepCall("t3", "ParseConfig"),
	)
	item.SetTiming(1000, 1075)

	line := ansi.Strip(item.summaryLine(&sty, true))
	require.Contains(t, line, "3 tools")
	require.Contains(t, line, "1m 15s")
	require.NotContains(t, line, "ParseConfig",
		"a finished run reports totals, not a replay of its last step")
}

func TestAgentSummaryCountsOneToolInTheSingular(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, true, grepCall("t1", "LoadConfig"))
	item.SetTiming(1000, 1003)

	require.Contains(t, ansi.Strip(item.summaryLine(&sty, true)), "1 tool · 3s")
}

// Timestamps come from the child session's messages, which may not have
// arrived. The count still stands on its own; a bogus duration does not.
func TestAgentSummaryOmitsDurationItDoesNotKnow(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, true, globCall("t1", "**/*.go"), grepCall("t2", "LoadConfig"))

	line := ansi.Strip(item.summaryLine(&sty, true))
	require.Contains(t, line, "2 tools")
	require.NotContains(t, line, "·", "no timestamps means no duration field at all")
}

// A run that has not called anything yet has nothing to summarize, and an
// empty arrow line would just push the tree down a row.
func TestAgentSummaryStaysAwayUntilThereIsWork(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, false)
	require.Empty(t, item.summaryLine(&sty, false))
}

// The live path learns the window one message at a time. If each sighting
// moved the start as well as the end, every run would measure zero.
func TestMarkActivityKeepsTheStartPut(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, true, grepCall("t1", "LoadConfig"))
	item.MarkActivity(1000)
	item.MarkActivity(1030)
	item.MarkActivity(1090)

	require.Contains(t, ansi.Strip(item.summaryLine(&sty, true)), "1m 30s")
}

// A zero timestamp is "unknown", not "the epoch". Letting it open the
// window would date every run to 1970 and print an absurd duration.
func TestMarkActivityIgnoresAMissingTimestamp(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := newAgentItem(t, true, grepCall("t1", "LoadConfig"))
	item.MarkActivity(0)
	item.MarkActivity(1000)
	item.MarkActivity(1042)

	require.Contains(t, ansi.Strip(item.summaryLine(&sty, true)), "42s")
}

// Everything above tests the string in isolation; this proves it is
// actually reached by the block the user looks at.
func TestAgentSummaryReachesTheRenderedBlock(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, false, grepCall("t1", "LoadConfig"))
	item.MarkActivity(1000)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, agentSummaryArrow)
	require.Contains(t, out, "LoadConfig")
}
