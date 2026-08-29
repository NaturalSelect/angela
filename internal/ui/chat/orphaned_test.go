package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// orphanCallMessage is an assistant message holding a single tool call. The
// caller decides whether a result exists, which is the whole input to the
// interruption predicate.
func orphanCallMessage() *message.Message {
	return &message.Message{
		ID:   "m1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1", Name: "bash", Input: `{"command":"sleep 600"}`, Finished: false},
		},
	}
}

// A tool call with no result, in a transcript nobody is running, was
// interrupted: the process that owned it is gone and it will never report
// back. Showing it as still running is the bug this pins — a restart used to
// leave those calls spinning forever.
func TestExtractMessageItemsMarksOrphanedCallsInterrupted(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	items := ExtractMessageItems(&sty, orphanCallMessage(), nil, "", false)
	require.Len(t, items, 1)

	tool, ok := items[0].(ToolMessageItem)
	require.True(t, ok)
	require.Equal(t, ToolStatusCanceled, tool.Status())
	require.True(t, tool.Finished(), "an interrupted call must not stay pending")

	rendered := ansi.Strip(tool.Render(80))
	require.Contains(t, rendered, "Canceled.")
	require.NotContains(t, rendered, "Waiting for tool response",
		"the spinner is the lie: nothing is going to answer this call")
}

// The same call, while its run is still active, is genuinely pending. The
// predicate must not swallow live work.
func TestExtractMessageItemsLeavesActiveCallsRunning(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	items := ExtractMessageItems(&sty, orphanCallMessage(), nil, "", true)
	require.Len(t, items, 1)

	tool, ok := items[0].(ToolMessageItem)
	require.True(t, ok)
	require.Equal(t, ToolStatusRunning, tool.Status())
	require.False(t, tool.Finished())
}

// A result already in the transcript settles the question on its own. The
// predicate only ever speaks for calls that have none, so an idle run must
// not repaint completed work as interrupted.
func TestExtractMessageItemsKeepsResultsWhenRunIsOver(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	results := map[string]message.ToolResult{
		"tc1": {ToolCallID: "tc1", Content: "done"},
	}
	items := ExtractMessageItems(&sty, orphanCallMessage(), results, "", false)
	require.Len(t, items, 1)

	tool, ok := items[0].(ToolMessageItem)
	require.True(t, ok)

	rendered := ansi.Strip(tool.Render(80))
	require.NotContains(t, rendered, "Canceled.",
		"a call that reported back is finished, however dead the run is now")
}

// The rule is stated for every tool, so it must not be wired to a list of
// tool names. An agent call is the one that matters most here: a branch
// suspends its parent on exactly this call, and after a restart the parent
// side has to stop claiming the branch is still working.
func TestExtractMessageItemsAppliesToEveryTool(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	for _, name := range []string{"bash", "edit", "agent", "view", "mcp_something"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := &message.Message{
				ID:   "m1",
				Role: message.Assistant,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "tc1", Name: name, Input: "{}", Finished: false},
				},
			}
			items := ExtractMessageItems(&sty, msg, nil, "", false)
			require.NotEmpty(t, items)

			tool, ok := items[0].(ToolMessageItem)
			require.True(t, ok)
			require.Equal(t, ToolStatusCanceled, tool.Status(),
				"no tool gets an exception from the interruption rule")
			require.False(t, strings.Contains(ansi.Strip(tool.Render(80)), "Waiting for tool response"))
		})
	}
}
