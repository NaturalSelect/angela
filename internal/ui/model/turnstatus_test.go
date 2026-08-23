package model

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func busyStatusUI() *UI {
	m := newBusyUI(detailsWorkspace())
	m.agentBusyCache.set(true)
	m.session = &session.Session{
		ID:               "s1",
		Title:            "t",
		PromptTokens:     3000,
		CompletionTokens: 240,
		Cost:             0.0412,
		Todos: []session.Todo{
			{Content: "one", Status: session.TodoStatusCompleted},
			{Content: "two", Status: session.TodoStatusInProgress},
			{Content: "three", Status: session.TodoStatusPending},
		},
	}
	return m
}

// The line is drawn into a fixed-width rect; one column of overflow corrupts
// the frame. This must hold at every width, including absurd ones.
func TestTurnStatusNeverExceedsWidth(t *testing.T) {
	t.Parallel()

	m := busyStatusUI()
	for width := 1; width <= 200; width++ {
		require.LessOrEqual(t, ansi.StringWidth(m.renderTurnStatus(width)), width,
			"busy status overflows at width %d", width)
	}

	m.agentBusyCache.set(false)
	for width := 1; width <= 200; width++ {
		require.LessOrEqual(t, ansi.StringWidth(m.renderTurnStatus(width)), width,
			"idle status overflows at width %d", width)
	}
}

// Fields drop from the tail as the terminal narrows, so the most important
// information (what it is doing) is the last thing to go.
func TestTurnStatusDropsFieldsFromTheTail(t *testing.T) {
	t.Parallel()

	m := busyStatusUI()
	m.promptQueue = 2

	wide := m.renderTurnStatus(200)
	require.Contains(t, wide, "queued")
	require.Contains(t, wide, "$0.04")
	require.Contains(t, wide, "Thinking")

	narrow := m.renderTurnStatus(40)
	require.NotContains(t, narrow, "queued", "the queue count must drop before the activity")
	require.Contains(t, narrow, "Thinking", "the activity is the last field to drop")
}

// esc means three different things during a turn; the hint has to say which.
func TestTurnStatusHintTracksCancelState(t *testing.T) {
	t.Parallel()

	m := busyStatusUI()
	require.Contains(t, m.renderTurnStatus(200), "stop")

	m.promptQueue = 3
	require.Contains(t, m.renderTurnStatus(200), "clear queue")

	m.isCanceling = true
	require.Contains(t, m.renderTurnStatus(200), "again to cancel")
}

// A sub-cent turn must not read as free.
func TestFormatCostCollapsesSubCent(t *testing.T) {
	t.Parallel()

	require.Empty(t, formatCost(0))
	require.Equal(t, "$<0.01", formatCost(0.004))
	require.Equal(t, "$0.04", formatCost(0.0412))
	require.Equal(t, "$12.30", formatCost(12.3))
}

func TestFormatTokensCompact(t *testing.T) {
	t.Parallel()

	require.Equal(t, "999", formatTokensCompact(999))
	require.Equal(t, "3.2k", formatTokensCompact(3240))
	require.Equal(t, "1.5M", formatTokensCompact(1_500_000))
}

// Without a session there is nothing to report, and the line must collapse
// rather than render a bare spinner.
func TestTurnStatusEmptyWithoutSession(t *testing.T) {
	t.Parallel()

	m := busyStatusUI()
	m.session = nil
	require.Empty(t, m.renderTurnStatus(120))
}

// The status line renders every frame; probing the workspace there is a
// synchronous HTTP round-trip in client/server mode.
func TestTurnStatusDoesNotProbeWorkspace(t *testing.T) {
	pinTTLs(t)

	ws := detailsWorkspace()
	m := newBusyUI(ws)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.agentBusyCache.set(true)
	m.session = &session.Session{ID: "s1", PromptTokens: 10, Cost: 0.5}
	ws.resetCounters()

	for range 20 {
		m.renderTurnStatus(120)
		m.agentBusyCache.set(false)
		m.renderTurnStatus(120)
		m.agentBusyCache.set(true)
	}

	require.Zero(t, ws.syncProbes(), "turn status must never probe the workspace")
}

// The activity segment names the tool and what it is acting on, which is the
// whole reason the line beats a bare spinner.
func TestTurnStatusNamesTheRunningTool(t *testing.T) {
	t.Parallel()

	m := busyStatusUI()
	require.Contains(t, m.renderTurnStatus(200), "Thinking",
		"with no pending tool the agent is thinking")

	out := m.renderTurnStatus(200)
	require.False(t, strings.Contains(out, "\n"), "the status line must stay on one line")
}
