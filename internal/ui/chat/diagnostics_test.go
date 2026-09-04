package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticsToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{}`}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Diagnostics")
}

func TestDiagnosticsToolDefaultsToProjectWhenNoFilePath(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{}`, Finished: true}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "project")
}

func TestDiagnosticsToolShowsFilePathWhenSet(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{"file_path":"internal/config/load.go"}`, Finished: true}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "internal/config/load.go")
	require.NotContains(t, out, "Diagnostics project")
}

func TestDiagnosticsToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "diag1", Content: "No diagnostics found."}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "No diagnostics found.")
}

func TestDiagnosticsToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "diag1", Content: ""}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Diagnostics")
}

func TestDiagnosticsToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "diag1", Content: "internal/config/load.go:10: unused variable 'x'"}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "unused variable")
}

func TestDiagnosticsToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "diag1", Name: toolnames.LSPDiagnostics, Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "diag1", Content: "internal/config/load.go:10: unused variable 'x'"}
	item := NewDiagnosticsToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "unused variable")
}
