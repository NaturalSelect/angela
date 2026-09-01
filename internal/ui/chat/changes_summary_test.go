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

// An applied edit becomes history the moment it lands, so Edit keeps
// showing its diff by default. The new "↳ +N -M" badge reuses the
// Additions/Removals the tool already computed at execution time.
func TestEditSummaryLineShowsAdditionsAndRemovals(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.EditResponseMetadata{
		Additions:  3,
		Removals:   1,
		OldContent: "a\nb\nc\n",
		NewContent: "a\nX\nY\nZ\nc\n",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{
		ID:       "edit-1",
		Name:     toolnames.Edit,
		Input:    `{"file_path":"a.go","old_string":"b","new_string":"X\nY\nZ"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "edit-1",
		Content:    "Content replaced in file: a.go",
		Metadata:   string(metaJSON),
	}

	item := NewEditToolMessageItem(&sty, toolCall, result, false)
	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, agentSummaryArrow)
	require.Contains(t, out, "+3")
	require.Contains(t, out, "-1")
}

// A lone arrow with no numbers would be a badge that says nothing, so a
// no-op edit must not render the summary line at all.
func TestEditSummaryLineHiddenWhenNoChanges(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.EditResponseMetadata{
		OldContent: "a\n",
		NewContent: "a\n",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{
		ID:       "edit-1",
		Name:     toolnames.Edit,
		Input:    `{"file_path":"a.go","old_string":"a","new_string":"a"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "edit-1",
		Content:    "Content replaced in file: a.go",
		Metadata:   string(metaJSON),
	}

	item := NewEditToolMessageItem(&sty, toolCall, result, false)
	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, agentSummaryArrow,
		"a zero-change summary must not render a lone arrow")
}

// MultiEdit shares Edit's metadata shape, so it must share the summary
// line too.
func TestMultiEditSummaryLineShowsAdditionsAndRemovals(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.MultiEditResponseMetadata{
		Additions: 2,
		Removals:  2,
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{
		ID:       "multiedit-1",
		Name:     toolnames.MultiEdit,
		Input:    `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"A"},{"old_string":"b","new_string":"B"}]}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "multiedit-1",
		Content:    "Applied 2 edits to a.go",
		Metadata:   string(metaJSON),
	}

	item := NewMultiEditToolMessageItem(&sty, toolCall, result, false)
	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, agentSummaryArrow)
	require.Contains(t, out, "+2")
	require.Contains(t, out, "-2")
}

// ReplaceSymbol has no diff-derived metadata of its own; it relies on
// the Additions/Removals the tool now precomputes with the same
// diff.GenerateDiff helper Edit/Write/MultiEdit use.
func TestReplaceSymbolSummaryLineReflectsMetadata(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.ReplaceSymbolResponseMetadata{
		FilePath:   "a.go",
		OldContent: "func Foo() {}\n",
		NewContent: "func Foo() int {\n\treturn 1\n}\n",
		Action:     "replace",
		Additions:  3,
		Removals:   1,
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{
		ID:       "rs-1",
		Name:     toolnames.LSPReplaceSymbol,
		Input:    `{"symbol":"Foo","file_path":"a.go","replacement":"func Foo() int {\n\treturn 1\n}"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "rs-1",
		Content:    "Replaced symbol 'Foo' in a.go (lines 1-1)",
		Metadata:   string(metaJSON),
	}

	item := NewReplaceSymbolToolMessageItem(&sty, toolCall, result, false)
	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, agentSummaryArrow)
	require.Contains(t, out, "+3")
	require.Contains(t, out, "-1")
}
