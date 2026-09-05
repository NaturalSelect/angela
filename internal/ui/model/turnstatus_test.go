package model

import (
	"strings"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func busyStatusUI(t *testing.T) *UI {
	t.Helper()
	ws, _ := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
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

	m := busyStatusUI(t)
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

	m := busyStatusUI(t)
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

	m := busyStatusUI(t)
	require.Contains(t, m.renderTurnStatus(200), "stop")

	m.promptQueue = 3
	require.Contains(t, m.renderTurnStatus(200), "clear queue")

	m.isCanceling = true
	require.Contains(t, m.renderTurnStatus(200), "again to cancel")
}

// Token usage and context-window usage are two views of the same number;
// showing one without the other left the reader guessing what the visible
// one meant, so both turn states must carry them together.
func TestTurnStatusShowsTokensAndContextTogether(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.agentActive = workspace.ActiveAgent{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 10_000}},
	}

	busy := ansi.Strip(m.renderTurnStatus(200))
	require.Contains(t, busy, "32%", "busy status must show the context percentage")
	require.Contains(t, busy, "3.2k", "busy status must show the raw token count")

	m.agentBusyCache.set(false)
	idle := ansi.Strip(m.renderTurnStatus(200))
	require.Contains(t, idle, "32%", "idle status must show the context percentage")
	require.Contains(t, idle, "3.2k", "idle status must show the raw token count")
}

// Without a known context window there is nothing to take a percentage of,
// so both states fall back to the raw count alone rather than a bare "%".
func TestTurnStatusTokenUsageWithoutContextWindow(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)

	busy := ansi.Strip(m.renderTurnStatus(200))
	require.Contains(t, busy, "3.2k")
	require.NotContains(t, busy, "%")

	m.agentBusyCache.set(false)
	idle := ansi.Strip(m.renderTurnStatus(200))
	require.Contains(t, idle, "3.2k")
	require.NotContains(t, idle, "%")
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

	m := busyStatusUI(t)
	m.session = nil
	require.Empty(t, m.renderTurnStatus(120))
}

// The status line renders every frame; probing the workspace there is a
// synchronous HTTP round-trip in client/server mode.
func TestTurnStatusDoesNotProbeWorkspace(t *testing.T) {
	pinTTLs(t)

	ws, counts := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.agentBusyCache.set(true)
	m.session = &session.Session{ID: "s1", PromptTokens: 10, Cost: 0.5}
	*counts = detailsSyncProbeCounts{}

	for range 20 {
		m.renderTurnStatus(120)
		m.agentBusyCache.set(false)
		m.renderTurnStatus(120)
		m.agentBusyCache.set(true)
	}

	require.Zero(t, counts.sum(), "turn status must never probe the workspace")
}

// The activity segment names the tool and what it is acting on, which is the
// whole reason the line beats a bare spinner.
func TestTurnStatusNamesTheRunningTool(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	require.Contains(t, m.renderTurnStatus(200), "Thinking",
		"with no pending tool the agent is thinking")

	out := m.renderTurnStatus(200)
	require.False(t, strings.Contains(out, "\n"), "the status line must stay on one line")
}

// A retry in progress must read as recovering, not stuck, so it takes
// over the activity segment instead of the generic "Thinking" label.
func TestTurnStatusShowsRetryInProgress(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.retryStatus = &retryStatus{
		sessionID:  m.currentSessionID(),
		attempt:    2,
		maxAttempt: 3,
		reason:     "Rate limited",
		until:      time.Now().Add(10 * time.Second),
	}

	status := m.renderTurnStatus(200)
	require.Contains(t, status, "Retrying 2/3")
	require.Contains(t, status, "Rate limited")
	require.NotContains(t, status, "Thinking")
}

// Once the announced backoff elapses, the retried request is
// indistinguishable from ordinary thinking time, so the label must fall
// back on its own without any explicit "resolved" signal.
func TestTurnStatusRetryExpiresWithoutExplicitClear(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.retryStatus = &retryStatus{
		sessionID:  m.currentSessionID(),
		attempt:    1,
		maxAttempt: 3,
		until:      time.Now().Add(-time.Millisecond),
	}

	status := m.renderTurnStatus(200)
	require.NotContains(t, status, "Retrying")
	require.Contains(t, status, "Thinking")
}

// A retry reported for a different session (a background sub-agent, or a
// turn the user has since navigated away from) must not bleed into the
// status line of whatever session is actually in view.
func TestTurnStatusRetryScopedToSession(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.retryStatus = &retryStatus{
		sessionID:  "other-session",
		attempt:    1,
		maxAttempt: 3,
		until:      time.Now().Add(time.Minute),
	}

	require.NotContains(t, m.renderTurnStatus(200), "Retrying")
}

// There is deliberately no timeout on tool calls, so a tool call that has
// been pending past toolSlowThreshold must report its own running time —
// otherwise a slow bash command or MCP server reads as a hang instead of
// as still working.
func TestTurnStatusShowsSlowToolCall(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.chat.SetMessages(&testToolMessageItem{
		testMessageItem: testMessageItem{id: "t1", text: "t1"},
		tc:              message.ToolCall{ID: "t1", Name: "Bash"},
		status:          chat.ToolStatusRunning,
	})
	m.activeTool = &toolTiming{id: "t1", since: time.Now().Add(-10 * time.Second)}

	status := m.renderTurnStatus(200)
	require.Contains(t, status, "Bash")
	require.Contains(t, status, "10s")
}

// Most tool calls finish in well under toolSlowThreshold, so one that
// hasn't crossed it yet must not carry a running-time suffix: a clock on
// every call would be noise rather than a signal.
func TestTurnStatusHidesElapsedUnderThreshold(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.chat.SetMessages(&testToolMessageItem{
		testMessageItem: testMessageItem{id: "t1", text: "t1"},
		tc:              message.ToolCall{ID: "t1", Name: "Bash"},
		status:          chat.ToolStatusRunning,
	})
	m.activeTool = &toolTiming{id: "t1", since: time.Now().Add(-2 * time.Second)}

	status := m.renderTurnStatus(200)
	require.Contains(t, status, "Bash")
	require.NotRegexp(t, `\(\d+s\)`, status)
}

// The Agent tool runs a whole nested turn, so a long duration there is
// expected rather than a sign of stalling: the status line must never
// attach a running-time suffix to it, even if activeTool's ID happens to
// line up.
func TestTurnStatusNeverFlagsAgentToolAsSlow(t *testing.T) {
	t.Parallel()

	m := busyStatusUI(t)
	m.chat.SetMessages(&testToolMessageItem{
		testMessageItem: testMessageItem{id: "t1", text: "t1"},
		tc:              message.ToolCall{ID: "t1", Name: toolnames.Agent},
		status:          chat.ToolStatusRunning,
	})
	m.activeTool = &toolTiming{id: "t1", since: time.Now().Add(-10 * time.Second)}

	status := m.renderTurnStatus(200)
	require.Contains(t, status, toolnames.Agent)
	require.NotRegexp(t, `\(\d+s\)`, status)
}
