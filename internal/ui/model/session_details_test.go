package model

import (
	"context"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// detailsSyncProbeCounts tracks calls to the workspace methods
// countingWorkspace.syncProbes used to sum, so detailsMockWorkspace's
// callers can assert none happened during a render window the same way
// the old fake's resetCounters + syncProbes did. Config() and WorkingDir()
// are deliberately excluded: they are cheap local reads, not HTTP
// round-trips, and were never part of the original sum either.
type detailsSyncProbeCounts struct {
	ready, agentBusy, queued, queueList, perm, active, lspState, lspDiag int
}

func (c *detailsSyncProbeCounts) sum() int {
	return c.ready + c.agentBusy + c.queued + c.queueList + c.perm + c.active + c.lspState + c.lspDiag
}

// detailsMockWorkspace is the gomock twin of the old detailsWorkspace: enough
// config for every detail column to render — mcpInfo, skillsInfo and
// modelInfo all reach into Config(). The returned counts track every
// syncProbes-equivalent call, for tests that assert none happen during
// rendering (e.g. TestSessionDetailsDoesNotProbeWorkspace); other
// consumers can ignore it.
func detailsMockWorkspace(t *testing.T) (*MockWorkspace, *detailsSyncProbeCounts) {
	t.Helper()

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("mock", config.ProviderConfig{ID: "mock", Name: "Mock", Type: catwalk.TypeOpenAI})
	cfg := &config.Config{Providers: providers, Options: &config.Options{}}
	active := workspace.ActiveAgent{ModelName: config.ModelMain}

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	counts := &detailsSyncProbeCounts{}
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().AgentIsReady().DoAndReturn(func() bool { counts.ready++; return true }).AnyTimes()
	ws.EXPECT().AgentIsBusy().DoAndReturn(func() bool { counts.agentBusy++; return false }).AnyTimes()
	ws.EXPECT().AgentQueuedPrompts(gomock.Any()).DoAndReturn(func(string) int { counts.queued++; return 0 }).AnyTimes()
	ws.EXPECT().AgentQueuedPromptsList(gomock.Any()).DoAndReturn(func(string) []string { counts.queueList++; return nil }).AnyTimes()
	ws.EXPECT().PermissionMode().DoAndReturn(func() permission.PermissionMode { counts.perm++; return permission.ModeManual }).AnyTimes()
	ws.EXPECT().AgentActive(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, string) (workspace.ActiveAgent, error) {
		counts.active++
		return active, nil
	}).AnyTimes()
	ws.EXPECT().LSPGetStates().DoAndReturn(func() map[string]workspace.LSPClientInfo {
		counts.lspState++
		return nil
	}).AnyTimes()
	ws.EXPECT().LSPGetDiagnosticCounts(gomock.Any()).DoAndReturn(func(string) lsp.DiagnosticCounts {
		counts.lspDiag++
		return lsp.DiagnosticCounts{}
	}).AnyTimes()
	ws.EXPECT().MCPGetStates().Return(nil).AnyTimes()

	return ws, counts
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
		ws, _ := detailsMockWorkspace(t)
		m := newBusyUIWithWorkspace(ws)
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
	ws, _ := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
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
		ws, _ := detailsMockWorkspace(t)
		m := newBusyUIWithWorkspace(ws)
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

	ws, counts := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.session = &session.Session{ID: "s1", Title: "t", Todos: manyTodos(5)}
	m.updateLayoutAndSize()
	*counts = detailsSyncProbeCounts{}

	for range 10 {
		scr := uv.NewScreenBuffer(m.width, m.height)
		m.drawSessionDetails(scr, m.layout.sessionDetails)
	}

	require.Zero(t, counts.sum(), "details rendering must never probe the workspace")
}
