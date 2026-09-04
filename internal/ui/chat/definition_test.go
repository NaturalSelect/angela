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

func TestDefinitionToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"Foo"}`}
	item := NewDefinitionToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Find Definition")
}

func TestDefinitionToolHeaderShowsSymbol(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"LoadConfig"}`, Finished: true}
	item := NewDefinitionToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "LoadConfig")
}

func TestDefinitionToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: "Found 1 definition(s):\n\na.go:1\n"}
	item := NewDefinitionToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "definition(s)")
}

func TestDefinitionToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: ""}
	item := NewDefinitionToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Find Definition")
}

func TestDefinitionToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: "Found 1 definition(s):\n\na.go:1\n"}
	item := NewDefinitionToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "definition(s)")
}

func TestDefinitionToolExpandedUsesCodeMetadata(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.DefinitionResponseMetadata{
		FilePath: "internal/config/load.go",
		Line:     10,
		Content:  "func LoadConfig() error {\n\treturn nil\n}",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"LoadConfig"}`, Finished: true}
	result := &message.ToolResult{
		ToolCallID: "d1",
		Content:    "Found 1 definition(s):\n\ninternal/config/load.go:11\n",
		Metadata:   string(metaJSON),
	}
	item := NewDefinitionToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "func LoadConfig")
}

func TestDefinitionToolExpandedFallsBackToPlainTextWithoutMetadata(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.LSPDefinition, Input: `{"symbol":"LoadConfig"}`, Finished: true}
	result := &message.ToolResult{
		ToolCallID: "d1",
		Content:    "Found 1 definition(s):\n\ninternal/config/load.go:11\n",
	}
	item := NewDefinitionToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "definition(s)")
}
