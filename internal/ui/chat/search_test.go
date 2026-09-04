package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------
// Glob
// -----------------------------------------------------------------------

func TestGlobToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "glob-1", Name: toolnames.Glob, Input: `{"pattern":"*.go"}`, Finished: false}
	item := NewGlobToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Glob)
}

func TestGlobToolMessageItem_InvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "glob-1", Name: toolnames.Glob, Input: `not json`, Finished: true}
	item := NewGlobToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestGlobToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "glob-1", Name: toolnames.Glob, Input: `{"pattern":"*.go","path":"internal"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "glob-1", Content: "internal/a.go\ninternal/b.go"}
	item := NewGlobToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Glob)
	require.NotContains(t, out, "internal/a.go")
}

func TestGlobToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "glob-1", Name: toolnames.Glob, Input: `{"pattern":"*.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "glob-1", Content: ""}
	item := NewGlobToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Glob)
}

func TestGlobToolMessageItem_ExpandedShowsMatches(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "glob-1", Name: toolnames.Glob, Input: `{"pattern":"*.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "glob-1", Content: "internal/a.go\ninternal/b.go"}
	item := NewGlobToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.NotContains(t, collapsed, "internal/a.go")

	expandable, ok := item.(Expandable)
	require.True(t, ok)
	require.True(t, expandable.ToggleExpanded())

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "internal/a.go")
	require.Contains(t, expanded, "internal/b.go")
}

// -----------------------------------------------------------------------
// Grep
// -----------------------------------------------------------------------

func TestGrepToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "grep-1", Name: toolnames.Grep, Input: `{"pattern":"foo"}`, Finished: false}
	item := NewGrepToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Grep)
}

func TestGrepToolMessageItem_InvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "grep-1", Name: toolnames.Grep, Input: `not json`, Finished: true}
	item := NewGrepToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

// The path, include, and literal-text params only show up in the header
// when they are actually set.
func TestGrepToolMessageItem_ShowsOptionalParamsWhenSet(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID: "grep-1", Name: toolnames.Grep,
		Input:    `{"pattern":"LoadConfig","path":"internal/config","include":"*.go","literal_text":true}`,
		Finished: true,
	}
	result := &message.ToolResult{ToolCallID: "grep-1", Content: "internal/config/load.go:1:func LoadConfig() {"}
	item := NewGrepToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "LoadConfig")
	require.Contains(t, out, "internal/config")
	require.Contains(t, out, "*.go")
	require.Contains(t, out, "true")
}

func TestGrepToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "grep-1", Name: toolnames.Grep, Input: `{"pattern":"foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "grep-1", Content: "a.go:1:foo"}
	item := NewGrepToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Grep)
	require.NotContains(t, out, "a.go:1:foo")
}

func TestGrepToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "grep-1", Name: toolnames.Grep, Input: `{"pattern":"foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "grep-1", Content: ""}
	item := NewGrepToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Grep)
}

func TestGrepToolMessageItem_ExpandedShowsMatches(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "grep-1", Name: toolnames.Grep, Input: `{"pattern":"foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "grep-1", Content: "a.go:1:foo\nb.go:2:foo"}
	item := NewGrepToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.NotContains(t, collapsed, "a.go:1:foo")

	expandable, ok := item.(Expandable)
	require.True(t, ok)
	require.True(t, expandable.ToggleExpanded())

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "a.go:1:foo")
	require.Contains(t, expanded, "b.go:2:foo")
}

// -----------------------------------------------------------------------
// LS
// -----------------------------------------------------------------------

func TestLSToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ls-1", Name: toolnames.LS, Input: `{"path":"internal"}`, Finished: false}
	item := NewLSToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "List")
}

func TestLSToolMessageItem_InvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ls-1", Name: toolnames.LS, Input: `not json`, Finished: true}
	item := NewLSToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

// An empty path defaults to "." rather than showing a blank target.
func TestLSToolMessageItem_DefaultsToCurrentDirectory(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ls-1", Name: toolnames.LS, Input: `{}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ls-1", Content: "a.go\nb.go"}
	item := NewLSToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, ".")
}

func TestLSToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ls-1", Name: toolnames.LS, Input: `{"path":"internal"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ls-1", Content: "a.go\nb.go"}
	item := NewLSToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "List")
	require.NotContains(t, out, "a.go")
}

func TestLSToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ls-1", Name: toolnames.LS, Input: `{"path":"internal"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ls-1", Content: ""}
	item := NewLSToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "List")
}

func TestLSToolMessageItem_ExpandedShowsEntries(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ls-1", Name: toolnames.LS, Input: `{"path":"internal"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ls-1", Content: "a.go\nb.go"}
	item := NewLSToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.NotContains(t, collapsed, "a.go")

	expandable, ok := item.(Expandable)
	require.True(t, ok)
	require.True(t, expandable.ToggleExpanded())

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "a.go")
}

// -----------------------------------------------------------------------
// Sourcegraph
// -----------------------------------------------------------------------

func TestSourcegraphToolMessageItem_Pending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sg-1", Name: toolnames.Sourcegraph, Input: `{"query":"repo:foo"}`, Finished: false}
	item := NewSourcegraphToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Sourcegraph)
}

func TestSourcegraphToolMessageItem_InvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sg-1", Name: toolnames.Sourcegraph, Input: `not json`, Finished: true}
	item := NewSourcegraphToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

// Count and context window are only shown when non-zero.
func TestSourcegraphToolMessageItem_ShowsCountAndContextWhenSet(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID: "sg-1", Name: toolnames.Sourcegraph,
		Input:    `{"query":"repo:foo useEffect","count":5,"context_window":20}`,
		Finished: true,
	}
	result := &message.ToolResult{ToolCallID: "sg-1", Content: "match: foo.go:10"}
	item := NewSourcegraphToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "repo:foo useEffect")
	require.Contains(t, out, "5")
	require.Contains(t, out, "20")
}

func TestSourcegraphToolMessageItem_Compact(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sg-1", Name: toolnames.Sourcegraph, Input: `{"query":"repo:foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sg-1", Content: "match: foo.go:10"}
	item := NewSourcegraphToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok)
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Sourcegraph)
	require.NotContains(t, out, "foo.go:10")
}

func TestSourcegraphToolMessageItem_EmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sg-1", Name: toolnames.Sourcegraph, Input: `{"query":"repo:foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sg-1", Content: ""}
	item := NewSourcegraphToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Sourcegraph)
}

func TestSourcegraphToolMessageItem_ExpandedShowsMatches(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sg-1", Name: toolnames.Sourcegraph, Input: `{"query":"repo:foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "sg-1", Content: "match: foo.go:10"}
	item := NewSourcegraphToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.NotContains(t, collapsed, "foo.go:10")

	expandable, ok := item.(Expandable)
	require.True(t, ok)
	require.True(t, expandable.ToggleExpanded())

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "foo.go:10")
}
