package chat

import (
	"encoding/json"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Bash Tool
// -----------------------------------------------------------------------------

func TestBashToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"echo hi"}`}
	item := NewBashToolMessageItem(&sty, toolCall, nil, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Bash)
}

func TestBashToolInvalidInputShowsParseFailure(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `not-json`, Finished: true}
	item := NewBashToolMessageItem(&sty, toolCall, nil, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "failed to parse command")
}

func TestBashToolCollapsesMultilineCommandByDefault(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"echo hi\necho bye"}`, Finished: true}
	item := NewBashToolMessageItem(&sty, toolCall, nil, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "echo hi echo bye", "collapsed command must join lines with a space")
}

func TestBashToolExpandedKeepsMultilineCommand(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"echo hi\necho bye"}`, Finished: true}
	item := NewBashToolMessageItem(&sty, toolCall, nil, false, "")

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	expandable.ToggleExpanded()

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "echo hi echo bye", "expanded command must not be joined onto one line")
	require.Contains(t, out, "echo hi")
	require.Contains(t, out, "echo bye")
}

func TestBashToolShowsBackgroundFlagInHeader(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"echo hi","run_in_background":true}`, Finished: true}
	item := NewBashToolMessageItem(&sty, toolCall, nil, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "background=true")
}

func TestBashToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"echo hi"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "b1", Content: "distinctive-output-marker"}
	item := NewBashToolMessageItem(&sty, toolCall, result, false, "")

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Bash)
	require.NotContains(t, out, "distinctive-output-marker")
}

func TestBashToolBackgroundJobRendersJobHeader(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.BashResponseMetadata{
		Background:  true,
		ShellID:     "42",
		Description: "dev server",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"npm run dev","run_in_background":true}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "b1", Content: "starting up...", Metadata: string(metaJSON)}
	item := NewBashToolMessageItem(&sty, toolCall, result, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Job")
	require.Contains(t, out, "(Start)")
	require.Contains(t, out, "PID 42")
	require.Contains(t, out, "dev server")
	require.Contains(t, out, "npm run dev")
	require.Contains(t, out, "starting up...")
}

func TestBashToolBackgroundJobDescriptionFallsBackToCommand(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.BashResponseMetadata{Background: true, ShellID: "7"})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"sleep 100"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "b1", Metadata: string(metaJSON)}
	item := NewBashToolMessageItem(&sty, toolCall, result, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "sleep 100")
}

func TestBashToolNoOutputHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"true"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "b1", Content: tools.BashNoOutput}
	item := NewBashToolMessageItem(&sty, toolCall, result, false, "")

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, tools.BashNoOutput)
}

func TestBashToolPrefersMetadataOutputOverContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.BashResponseMetadata{Output: "from metadata"})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"echo hi"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "b1", Content: "from content", Metadata: string(metaJSON)}
	item := NewBashToolMessageItem(&sty, toolCall, result, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "from metadata")
	require.NotContains(t, out, "from content")
}

func TestBashToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "b1", Name: toolnames.Bash, Input: `{"command":"sleep 5"}`, Finished: true}
	item := NewBashToolMessageItem(&sty, toolCall, nil, false, "")

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

// -----------------------------------------------------------------------------
// Job Output Tool
// -----------------------------------------------------------------------------

func TestJobOutputToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "j1", Name: toolnames.JobOutput, Input: `{"shell_id":"1"}`}
	item := NewJobOutputToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Job")
}

func TestJobOutputToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "j1", Name: toolnames.JobOutput, Input: `not-json`, Finished: true}
	item := NewJobOutputToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestJobOutputToolRendersDescriptionAndContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.JobOutputResponseMetadata{Command: "npm run dev", Description: "dev server"})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "j1", Name: toolnames.JobOutput, Input: `{"shell_id":"9"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "j1", Content: "listening on :3000", Metadata: string(metaJSON)}
	item := NewJobOutputToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "(Output)")
	require.Contains(t, out, "PID 9")
	require.Contains(t, out, "dev server")
	require.Contains(t, out, "listening on :3000")
}

func TestJobOutputToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "j1", Name: toolnames.JobOutput, Input: `{"shell_id":"9"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "j1", Content: "listening on :3000"}
	item := NewJobOutputToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "listening on :3000")
}

func TestJobOutputToolAwaitsResult(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "j1", Name: toolnames.JobOutput, Input: `{"shell_id":"9"}`, Finished: true}
	item := NewJobOutputToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

// -----------------------------------------------------------------------------
// Job Kill Tool
// -----------------------------------------------------------------------------

func TestJobKillToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "k1", Name: toolnames.JobKill, Input: `{"shell_id":"1"}`}
	item := NewJobKillToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Job")
}

func TestJobKillToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "k1", Name: toolnames.JobKill, Input: `not-json`, Finished: true}
	item := NewJobKillToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestJobKillToolRendersAction(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.JobKillResponseMetadata{Command: "npm run dev"})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "k1", Name: toolnames.JobKill, Input: `{"shell_id":"9"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "k1", Content: "killed", Metadata: string(metaJSON)}
	item := NewJobKillToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "(Kill)")
	require.Contains(t, out, "PID 9")
	require.Contains(t, out, "npm run dev")
	require.Contains(t, out, "killed")
}

func TestJobHeaderOmitsDescriptionWhenTooNarrow(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.JobKillResponseMetadata{
		Description: "a very long running description that will not fit",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "k1", Name: toolnames.JobKill, Input: `{"shell_id":"9"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "k1", Content: "killed", Metadata: string(metaJSON)}
	item := NewJobKillToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(20))
	require.Contains(t, out, "Job")
	require.NotContains(t, out, "a very long running description")
}
