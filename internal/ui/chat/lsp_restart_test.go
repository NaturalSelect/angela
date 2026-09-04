package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestLSPRestartToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{}`}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Restart LSP")
}

func TestLSPRestartToolWithoutNameRestartsAll(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{}`, Finished: true}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Restart LSP")
}

func TestLSPRestartToolWithNameShowsIt(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{"name":"gopls"}`, Finished: true}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "gopls")
}

func TestLSPRestartToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{"name":"gopls"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "r1", Content: "Restarted gopls."}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "Restarted gopls.")
}

func TestLSPRestartToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{"name":"gopls"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "r1", Content: ""}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Restart LSP")
}

func TestLSPRestartToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{"name":"gopls"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "r1", Content: "Restarted gopls."}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "Restarted gopls.")
}

func TestLSPRestartToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "r1", Name: toolnames.LSPRestart, Input: `{"name":"gopls"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "r1", Content: "Restarted gopls."}
	item := NewLSPRestartToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Restarted gopls.")
}
