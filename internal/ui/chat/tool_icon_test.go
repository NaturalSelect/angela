package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// A screenful of tool calls is only skimmable if the kinds look different.
// Every icon here used to be the same glyph, distinguished by color alone.
func TestToolKindIconsDistinguishKinds(t *testing.T) {
	t.Parallel()

	byKind := map[string]string{
		"shell":  toolKindIcon(toolnames.Bash),
		"write":  toolKindIcon(toolnames.Edit),
		"read":   toolKindIcon(toolnames.View),
		"search": toolKindIcon(toolnames.Grep),
		"fetch":  toolKindIcon(toolnames.WebFetch),
		"web":    toolKindIcon(toolnames.WebSearch),
		"agent":  toolKindIcon(toolnames.Agent),
		"todo":   toolKindIcon(toolnames.Todos),
		"lsp":    toolKindIcon(toolnames.LSPDiagnostics),
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

	require.Equal(t, toolKindIcon(toolnames.Bash), toolKindIcon(toolnames.JobKill))
	require.Equal(t, toolKindIcon(toolnames.Edit), toolKindIcon(toolnames.MultiEdit))
	require.Equal(t, toolKindIcon(toolnames.Grep), toolKindIcon(toolnames.Glob))
	require.Equal(t, toolKindIcon(toolnames.LSPDiagnostics), toolKindIcon(toolnames.LSPSymbols))

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
		ToolStatusSuccess, ToolStatusError, ToolStatusCanceled,
		ToolStatusRunning, ToolStatusAwaitingPermission,
	} {
		got := ansi.Strip(toolStatusStyle(&sty, status).Render(styles.ToolIconShell))
		require.Equal(t, styles.ToolIconShell, got,
			"status %v leaked its own glyph into the rendered icon", status)
	}
}

// The icon color is the only thing telling four outcomes apart at a glance,
// so no two of them may render the same. A call blocked on an approval is
// the one that matters most here: it looks exactly like a running call
// otherwise, and the user never learns they are the one holding it up.
func TestToolStatusStyleColorsDiffer(t *testing.T) {
	t.Parallel()

	for _, theme := range []struct {
		name string
		sty  styles.Styles
	}{
		{"CharmtonePantera", styles.CharmtonePantera()},
		{"AngelaTeal", styles.AngelaTeal()},
	} {
		t.Run(theme.name, func(t *testing.T) {
			t.Parallel()
			sty := theme.sty

			byStatus := map[string]ToolStatus{
				"running":  ToolStatusRunning,
				"awaiting": ToolStatusAwaitingPermission,
				"success":  ToolStatusSuccess,
				"error":    ToolStatusError,
			}

			seen := map[string]string{}
			for name, status := range byStatus {
				rendered := toolStatusStyle(&sty, status).Render(styles.ToolIconShell)
				require.NotEqual(t, ansi.Strip(rendered), rendered,
					"%s renders with no color at all", name)
				if prev, dup := seen[rendered]; dup {
					t.Errorf("%s and %s render identically: a reader cannot tell them apart", prev, name)
				}
				seen[rendered] = name
			}
		})
	}
}

// A cancelled call is a call that did not do its job, which is what the
// error color already says. Keeping them apart would spend the reader's
// attention on a distinction that does not change what they should do.
func TestCancelledSharesTheErrorColor(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	cancelled := toolStatusStyle(&sty, ToolStatusCanceled).Render(styles.ToolIconShell)
	failed := toolStatusStyle(&sty, ToolStatusError).Render(styles.ToolIconShell)

	require.Equal(t, failed, cancelled)
}

// toolIcon is the second status-to-color mapping, used by the bash job
// header. It has to agree with toolStatusStyle: a job blocked on an
// approval and a job still working carry the same glyph, so if they also
// share a color the header stops saying anything at all.
func TestToolIconDistinguishesAwaitingFromRunning(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	running := toolIcon(&sty, ToolStatusRunning)
	awaiting := toolIcon(&sty, ToolStatusAwaitingPermission)

	require.Equal(t, ansi.Strip(running), ansi.Strip(awaiting),
		"both states are still in flight and keep the same glyph")
	require.NotEqual(t, running, awaiting,
		"a job waiting on the user must not look like one that is working")
}
