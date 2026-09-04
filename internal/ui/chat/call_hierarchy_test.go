package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestCallHierarchyToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo"}`}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Call Hierarchy")
}

func TestCallHierarchyToolDefaultsToIncoming(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo"}`, Finished: true}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Foo")
	require.Contains(t, out, "incoming")
}

func TestCallHierarchyToolOutgoingDirection(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo","direction":"outgoing"}`, Finished: true}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "outgoing")
	require.NotContains(t, out, "incoming")
}

func TestCallHierarchyToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ch1", Content: "Call hierarchy for 'Foo':\n\n1 caller(s):\n"}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "caller(s)")
}

func TestCallHierarchyToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ch1", Content: ""}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Foo")
}

func TestCallHierarchyToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ch1", Content: "Call hierarchy for 'Foo':\n\n1 caller(s):\n\n  a.go:1 — Bar\n"}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "caller(s)", "collapsed call must not leak its result body")
}

func TestCallHierarchyToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ch1", Name: toolnames.LSPCallHierarchy, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ch1", Content: "Call hierarchy for 'Foo':\n\n1 caller(s):\n\n  a.go:1 — Bar\n"}
	item := NewCallHierarchyToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "caller(s)")
	require.Contains(t, out, "Bar")
}
