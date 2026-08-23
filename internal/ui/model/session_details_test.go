package model

import (
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// detailsWorkspace is a stub with enough config for every detail column to
// render; mcpInfo, skillsInfo and modelInfo all reach into Config().
func detailsWorkspace() *countingWorkspace {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("mock", config.ProviderConfig{ID: "mock", Name: "Mock", Type: catwalk.TypeOpenAI})

	return &countingWorkspace{
		ready:  true,
		cfg:    &config.Config{Providers: providers, Options: &config.Options{}},
		active: workspace.ActiveAgent{ModelName: config.ModelMain},
	}
}

// drawDetails renders the details panel into a screen the size of the UI and
// returns its lines.
func drawDetails(m *UI) []string {
	m.updateLayoutAndSize()
	scr := uv.NewScreenBuffer(m.width, m.height)
	m.drawSessionDetails(scr, m.layout.sessionDetails)
	return strings.Split(strings.TrimRight(scr.Render(), "\n"), "\n")
}

func manyTodos(n int) []session.Todo {
	todos := make([]session.Todo, n)
	for i := range todos {
		todos[i] = session.Todo{
			Content: "a todo item that is long enough to need truncating",
			Status:  session.TodoStatusPending,
		}
	}
	return todos
}

// The panel is opened on top of the chat, so any line wider than the terminal
// corrupts the frame. This is the bug that recurs whenever a column is added.
func TestSessionDetailsNeverExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{80, 100, 140, 200} {
		ws := detailsWorkspace()
		m := newBusyUI(ws)
		m.width = width
		m.session = &session.Session{ID: "s1", Title: "a session title", Todos: manyTodos(30)}

		for i, line := range drawDetails(m) {
			require.LessOrEqual(t, ansi.StringWidth(line), width,
				"terminal width %d: line %d overflows", width, i)
		}
	}
}

// Every column must truncate rather than push the panel taller than its rect.
func TestSessionDetailsTruncatesOverflowingColumns(t *testing.T) {
	ws := detailsWorkspace()
	m := newBusyUI(ws)
	m.session = &session.Session{ID: "s1", Title: "a session title", Todos: manyTodos(50)}

	lines := drawDetails(m)
	require.LessOrEqual(t, len(lines), m.height)
	require.Contains(t, strings.Join(lines, "\n"), "more",
		"an overflowing column must show the …and N more tail")
}

// The panel is reachable in both layouts now; it used to be compact-only,
// which is what stranded the sidebar's content on wide terminals.
func TestSessionDetailsRectIsSetInBothLayouts(t *testing.T) {
	for _, compact := range []bool{false, true} {
		ws := detailsWorkspace()
		m := newBusyUI(ws)
		m.forceCompactMode = compact
		m.updateLayoutAndSize()

		rect := m.layout.sessionDetails
		require.Positive(t, rect.Dx(), "compact=%v: details rect has no width", compact)
		require.Positive(t, rect.Dy(), "compact=%v: details rect has no height", compact)
		require.LessOrEqual(t, rect.Dx(), m.width)
		require.LessOrEqual(t, rect.Max.Y, m.height)
	}
}

// Details rendering runs per frame; probing the workspace there is a
// synchronous HTTP round-trip in client/server mode.
func TestSessionDetailsDoesNotProbeWorkspace(t *testing.T) {
	pinTTLs(t)

	ws := detailsWorkspace()
	m := newBusyUI(ws)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.session = &session.Session{ID: "s1", Title: "t", Todos: manyTodos(5)}
	m.updateLayoutAndSize()
	ws.resetCounters()

	for range 10 {
		scr := uv.NewScreenBuffer(m.width, m.height)
		m.drawSessionDetails(scr, m.layout.sessionDetails)
	}

	require.Zero(t, ws.syncProbes(), "details rendering must never probe the workspace")
}
