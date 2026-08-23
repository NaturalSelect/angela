package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// A screenful of tool calls is only skimmable if the kinds look different.
// Every icon here used to be the same glyph, distinguished by color alone.
func TestToolKindIconsDistinguishKinds(t *testing.T) {
	t.Parallel()

	byKind := map[string]string{
		"shell":  toolKindIcon(tools.BashToolName),
		"write":  toolKindIcon(tools.EditToolName),
		"read":   toolKindIcon(tools.ViewToolName),
		"search": toolKindIcon(tools.GrepToolName),
		"fetch":  toolKindIcon(tools.WebFetchToolName),
		"web":    toolKindIcon(tools.WebSearchToolName),
		"agent":  toolKindIcon(agent.AgentToolName),
		"todo":   toolKindIcon(tools.TodosToolName),
		"lsp":    toolKindIcon(tools.DiagnosticsToolName),
		"mcp":    toolKindIcon("mcp_something"),
	}

	seen := map[string]string{}
	for kind, icon := range byKind {
		require.NotEmpty(t, icon, "%s has no icon", kind)
		require.Equal(t, 1, ansi.StringWidth(icon),
			"%s icon %q must occupy exactly one cell: the header reserves two "+
				"columns for icon plus space", kind, icon)
		if prev, dup := seen[icon]; dup {
			t.Errorf("%s and %s share icon %q", prev, kind, icon)
		}
		seen[icon] = kind
	}
}

// Tools that share a renderer should share an icon; an unknown tool must still
// get one rather than rendering a blank column.
func TestToolKindIconGroupsAndFallback(t *testing.T) {
	t.Parallel()

	require.Equal(t, toolKindIcon(tools.BashToolName), toolKindIcon(tools.JobKillToolName))
	require.Equal(t, toolKindIcon(tools.EditToolName), toolKindIcon(tools.MultiEditToolName))
	require.Equal(t, toolKindIcon(tools.GrepToolName), toolKindIcon(tools.GlobToolName))
	require.Equal(t, toolKindIcon(tools.DiagnosticsToolName), toolKindIcon(tools.SymbolsToolName))

	require.Equal(t, styles.ToolIconGeneric, toolKindIcon("some_unregistered_tool"))
	require.Equal(t, styles.ToolIconMCP, toolKindIcon("mcp_github_search"))
}

// The status styles carry their own glyph. If it is not cleared, lipgloss
// renders the status glyph and the kind glyph back to back and every header
// shifts a column.
func TestToolStatusStyleCarriesNoGlyph(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	for _, status := range []ToolStatus{
		ToolStatusSuccess, ToolStatusError, ToolStatusCanceled, ToolStatusRunning,
	} {
		got := ansi.Strip(toolStatusStyle(&sty, status).Render(styles.ToolIconShell))
		require.Equal(t, styles.ToolIconShell, got,
			"status %v leaked its own glyph into the rendered icon", status)
	}
}

// Status must still be visible, or a failed call reads as a successful one.
func TestToolStatusStyleColorsDiffer(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	success := toolStatusStyle(&sty, ToolStatusSuccess).Render(styles.ToolIconShell)
	failure := toolStatusStyle(&sty, ToolStatusError).Render(styles.ToolIconShell)
	pending := toolStatusStyle(&sty, ToolStatusRunning).Render(styles.ToolIconShell)

	require.NotEqual(t, success, failure)
	require.NotEqual(t, success, pending)
}
