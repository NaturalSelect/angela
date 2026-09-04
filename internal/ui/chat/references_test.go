package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestReferencesToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "refs-1", Name: toolnames.LSPReferences, Input: `{"symbol":"Foo"}`, Finished: false}
	item := NewReferencesToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Find References")
}

func TestReferencesToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "refs-1", Name: toolnames.LSPReferences, Input: `{"symbol":"Foo","path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "refs-1", Content: "a.go:1: Foo used here"}
	item := NewReferencesToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Find References")
	require.NotContains(t, out, "used here", "compact mode must not leak the result body")
}

func TestReferencesToolMessageItem_ErrorResult(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "refs-1", Name: toolnames.LSPReferences, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "refs-1", IsError: true, Content: "symbol not found"}
	item := NewReferencesToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "symbol not found")
}

func TestReferencesToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "refs-1", Name: toolnames.LSPReferences, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "refs-1", Content: ""}
	item := NewReferencesToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Find References")
}

// The result body is collapsed by default (investigative tools can
// always be re-queried) and only appears once expanded.
func TestReferencesToolMessageItem_ExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "refs-1", Name: toolnames.LSPReferences, Input: `{"symbol":"Foo","path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "refs-1", Content: "a.go:1: Foo used here"}
	item := NewReferencesToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.NotContains(t, collapsed, "used here")

	expandable, ok := item.(Expandable)
	require.True(t, ok)
	require.True(t, expandable.ToggleExpanded())

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "used here")
}
