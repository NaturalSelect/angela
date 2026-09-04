package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestRenameToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rename-1", Name: toolnames.LSPRename, Input: `{"symbol":"Foo","new_name":"Bar"}`, Finished: false}
	item := NewRenameToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Rename Symbol")
}

func TestRenameToolMessageItem_ShowsOldAndNewName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rename-1", Name: toolnames.LSPRename, Input: `{"symbol":"Foo","new_name":"Bar","path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rename-1", Content: "renamed Foo to Bar in 2 files"}
	item := NewRenameToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Foo")
	require.Contains(t, out, "Bar")
	require.Contains(t, out, "→")
	// Unlike LSPReferences, rename always shows its result body once
	// present, without requiring an expand toggle.
	require.Contains(t, out, "renamed Foo to Bar")
}

func TestRenameToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rename-1", Name: toolnames.LSPRename, Input: `{"symbol":"Foo","new_name":"Bar"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rename-1", Content: "renamed Foo to Bar"}
	item := NewRenameToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Rename Symbol")
	require.NotContains(t, out, "renamed Foo to Bar", "compact mode shows only the header")
}

func TestRenameToolMessageItem_ErrorResult(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rename-1", Name: toolnames.LSPRename, Input: `{"symbol":"Foo","new_name":"Bar"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rename-1", IsError: true, Content: "symbol not found"}
	item := NewRenameToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "symbol not found")
}

func TestRenameToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rename-1", Name: toolnames.LSPRename, Input: `{"symbol":"Foo","new_name":"Bar"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rename-1", Content: ""}
	item := NewRenameToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Rename Symbol")
}
