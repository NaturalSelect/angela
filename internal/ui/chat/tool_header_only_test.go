package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Investigative tools (View, Grep, Glob, ...) can always be re-queried from
// their source, so the transcript does not need to repeat their content by
// default. Collapsed means header-only; the full result is one toggle away.
func TestInvestigativeToolDefaultsToHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "grep-1",
		Name:     toolnames.Grep,
		Input:    `{"pattern":"LoadConfig"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "grep-1",
		Content:    "internal/config/load.go:42:func LoadConfig() error {",
	}

	item := NewGrepToolMessageItem(&sty, toolCall, result, false)

	collapsed := ansi.Strip(item.Render(100))
	require.Contains(t, collapsed, toolnames.Grep, "the header must still be visible")
	require.NotContains(t, collapsed, "LoadConfig() error",
		"a collapsed investigative tool call must not leak its result body")

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded(), "first toggle should expand")

	expanded := ansi.Strip(item.Render(100))
	require.Contains(t, expanded, "LoadConfig() error",
		"expanding must reveal the full result")
}

// The new header-only default must be reached purely through the existing
// ExpandedContent toggle, never by flipping Compact. Compact carries other
// semantics (the no-padding "nested inside an Agent" prefix) that must not
// leak onto a collapsed top-level call.
func TestCollapsedInvestigativeToolKeepsTopLevelPrefix(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "grep-1",
		Name:     toolnames.Grep,
		Input:    `{"pattern":"LoadConfig"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "grep-1",
		Content:    "internal/config/load.go:42:func LoadConfig() error {",
	}

	item := NewGrepToolMessageItem(&sty, toolCall, result, false)
	defaultOut := ansi.Strip(item.Render(100))
	require.True(t, strings.HasPrefix(defaultOut, "  "),
		"a top-level collapsed call keeps the blurred two-space indent")

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	compactOut := ansi.Strip(item.Render(100))
	require.False(t, strings.HasPrefix(compactOut, "  "),
		"a nested (compact) call renders without the top-level indent")
	require.NotEqual(t, defaultOut, compactOut,
		"a collapsed top-level call must not render identically to a nested one")
}

// makeCompactHeader used to hardcode ToolStatusSuccess, so a failed Docker
// MCP call rendered as if it had succeeded once nested/compact. The header
// must always reflect the real status, whether compact or not.
func TestDockerMCPCompactHeaderShowsRealStatus(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "docker-mcp-1",
		Name:     toolnames.MCPPrefix + config.DockerMCPName + "_mcp-remove",
		Input:    `{"name":"github"}`,
		Finished: true,
	}
	result := &message.ToolResult{
		ToolCallID: "docker-mcp-1",
		IsError:    true,
		Content:    "server not found",
	}

	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)
	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := item.Render(100)
	stripped := ansi.Strip(out)
	require.Contains(t, stripped, "Docker MCP")
	require.Contains(t, stripped, "Remove")
	require.Contains(t, stripped, styles.ToolError,
		"a failed compact Docker MCP call must show the error icon")
	require.NotContains(t, stripped, styles.ToolSuccess,
		"a failed compact Docker MCP call must not render as successful")
}
