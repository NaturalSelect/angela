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

func TestReplaceSymbolToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: false}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Replace Symbol")
}

func TestReplaceSymbolToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: true}
	metaJSON, err := json.Marshal(tools.ReplaceSymbolResponseMetadata{
		OldContent: "func Foo() {}\n",
		NewContent: "func Foo() int { return 1 }\n",
	})
	require.NoError(t, err)
	result := &message.ToolResult{ToolCallID: "rs-1", Content: "replaced", Metadata: string(metaJSON)}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Replace Symbol")
	require.Contains(t, out, "Foo")
}

// A canceled call with no result yet must report cancellation rather
// than falling through to the diff/plain content branches.
func TestReplaceSymbolToolMessageItem_Canceled(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: true}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, nil, true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Canceled")
}

func TestReplaceSymbolToolMessageItem_ErrorResult(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rs-1", IsError: true, Content: "symbol not found"}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "symbol not found")
}

// With no diff-shaped metadata at all, the renderer must fall back to
// plain text instead of rendering an empty diff.
func TestReplaceSymbolToolMessageItem_FallsBackToPlainWithoutMetadata(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rs-1", Content: "Replaced symbol 'Foo' in a.go"}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Replaced symbol 'Foo' in a.go")
}

// Invalid JSON metadata must not crash the renderer; it should behave
// the same as having no metadata at all.
func TestReplaceSymbolToolMessageItem_InvalidMetadataFallsBackToPlain(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "rs-1", Content: "Replaced symbol 'Foo' in a.go", Metadata: "not json"}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Replaced symbol 'Foo' in a.go")
}

// A metadata blob that unmarshals partway (e.g. a non-numeric
// "additions") must not still render a diff just because new_content
// happened to be parsed before the field that failed: err != nil has
// to gate both OldContent and NewContent, not just the first one.
func TestReplaceSymbolToolMessageItem_PartialUnmarshalErrorFallsBackToPlain(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "rs-1", Name: toolnames.LSPReplaceSymbol, Input: `{"symbol":"Foo","file_path":"a.go"}`, Finished: true}
	badMetadata := `{"new_content":"func Foo() int { return 1 }","additions":"not-a-number"}`
	result := &message.ToolResult{ToolCallID: "rs-1", Content: "Replaced symbol 'Foo' in a.go", Metadata: badMetadata}
	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Replaced symbol 'Foo' in a.go")
	require.NotContains(t, out, "func Foo() int { return 1 }")
}
