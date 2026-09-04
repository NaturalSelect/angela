package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSymbolsToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-1", Name: toolnames.LSPSymbols, Input: `{"file_path":"a.go"}`, Finished: false}
	item := NewSymbolsToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "List Symbols")
}

func TestSymbolsToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-1", Name: toolnames.LSPSymbols, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sym-1", Content: "func Foo()"}
	item := NewSymbolsToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "List Symbols")
	require.NotContains(t, out, "func Foo()")
}

func TestSymbolsToolMessageItem_ErrorResult(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-1", Name: toolnames.LSPSymbols, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sym-1", IsError: true, Content: "file not found"}
	item := NewSymbolsToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "file not found")
}

func TestSymbolsToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-1", Name: toolnames.LSPSymbols, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sym-1", Content: ""}
	item := NewSymbolsToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "List Symbols")
}

// The symbol tree is collapsed by default and rendered with code-style
// line numbers once expanded, preserving indentation.
func TestSymbolsToolMessageItem_ExpandedShowsCodeBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-1", Name: toolnames.LSPSymbols, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sym-1", Content: "func Foo()\nfunc Bar()"}
	item := NewSymbolsToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.NotContains(t, collapsed, "func Foo()")

	expandable, ok := item.(Expandable)
	require.True(t, ok)
	require.True(t, expandable.ToggleExpanded())

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "func Foo()")
	require.Contains(t, expanded, "func Bar()")
}
