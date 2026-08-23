package model

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
)

// Landing geometry.
const (
	// landingMenuWidth is the column budget one menu row is laid out in, so
	// the shortcuts line up under each other instead of tracking the
	// terminal edge.
	landingMenuWidth = 34
	// landingLogoMinHeight is the body height below which the letterform
	// wall is dropped. The wall goes before the menu does: a terminal that
	// cannot hold both should still offer the entry points.
	landingLogoMinHeight = 12
	// landingTopDivisor sits the block above center. Slack below the menu
	// reads as room leading into the prompt; slack above reads as a hole.
	landingTopDivisor = 3
)

// activeAgent returns the agent the current session is running, as
// memoized by the off-thread busy/agent probe (see workspace_cache.go).
// It returns nil when the agent isn't ready, when the last probe failed
// to resolve one, and when the memoized stamp belongs to a different
// session than the one on screen — during those windows the answer is
// "not known yet", never the previous session's agent. It must never
// probe the workspace: it is called on every frame and
// AgentIsReady/AgentActive are synchronous HTTP round-trips in
// client/server mode.
func (m *UI) activeAgent() *workspace.ActiveAgent {
	if !m.agentReady || !m.agentActiveKnown || m.agentActiveSession != m.currentSessionID() {
		return nil
	}
	active := m.agentActive
	return &active
}

// landingEntry is one row of the landing menu: an action and the key that
// runs it.
type landingEntry struct {
	label string
	key   string
}

// landingMenu returns the entry points the landing page offers. They are the
// bindings that do something useful before a session exists — everything
// else waits behind the command palette.
func (m *UI) landingMenu() []landingEntry {
	return []landingEntry{
		{"Resume session", m.keyMap.Sessions.Help().Key},
		{"Commands", m.keyMap.Commands.Help().Key},
		{"Switch model", m.keyMap.Models.Help().Key},
		{"Quit", m.keyMap.Quit.Help().Key},
	}
}

// renderLandingMenu lays the entries out as label-left, key-right rows within
// landingMenuWidth, falling back to the plain labels when the width cannot
// hold both columns.
func (m *UI) renderLandingMenu(width int) string {
	t := m.com.Styles
	entries := m.landingMenu()

	menuWidth := min(landingMenuWidth, width)
	rows := make([]string, 0, len(entries))
	for _, e := range entries {
		label := t.Landing.MenuLabel.Render(e.label)
		key := t.Landing.MenuKey.Render(e.key)
		gap := menuWidth - lipgloss.Width(label) - lipgloss.Width(key)
		if gap < 1 {
			rows = append(rows, ansi.Truncate(label, width, "…"))
			continue
		}
		rows = append(rows, label+strings.Repeat(" ", gap)+key)
	}
	return strings.Join(rows, "\n")
}

// landingView renders the landing page: the letterform wall over a menu of
// entry points, sat above center. Everything the old landing page reported —
// model, LSP, MCP, skills — now lives behind ctrl+d, so the entry screen
// stays an entry screen.
func (m *UI) landingView() string {
	width := m.layout.main.Dx()
	height := max(0, m.layout.main.Dy())

	var parts []string
	if height >= landingLogoMinHeight {
		parts = append(parts, renderLogo(m.com.Styles, width), "")
	}
	parts = append(parts, m.renderLandingMenu(width))
	block := strings.Join(parts, "\n")

	// Place the block above center, then let the style pad out the rest.
	if top := (height - lipgloss.Height(block)) / landingTopDivisor; top > 0 {
		block = strings.Repeat("\n", top) + block
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(block)
}
