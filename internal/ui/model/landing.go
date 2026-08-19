package model

import (
	"image"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/ultraviolet/layout"
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

// landingView renders the landing page view showing the current working
// directory, model information, and LSP/MCP status in a two-column layout.
func (m *UI) landingView() string {
	t := m.com.Styles
	width := m.layout.main.Dx()
	cwd := common.PrettyPath(t, m.com.Workspace.WorkingDir(), width)

	parts := []string{
		cwd,
	}

	parts = append(parts, "", m.modelInfo(width))
	infoSection := lipgloss.JoinVertical(lipgloss.Left, parts...)

	var remainingHeightArea image.Rectangle
	layout.Vertical(
		layout.Len(lipgloss.Height(infoSection)+1),
		layout.Fill(1),
	).Split(m.layout.main).Assign(new(image.Rectangle), &remainingHeightArea)

	mcpLspSectionWidth := min(30, (width-2)/3)

	lspSection := m.lspInfo(mcpLspSectionWidth, max(1, remainingHeightArea.Dy()), false)
	mcpSection := m.mcpInfo(mcpLspSectionWidth, max(1, remainingHeightArea.Dy()), false)
	skillsSection := m.skillsInfo(mcpLspSectionWidth, max(1, remainingHeightArea.Dy()), false)

	content := lipgloss.JoinHorizontal(lipgloss.Left, lspSection, " ", mcpSection, " ", skillsSection)

	return lipgloss.NewStyle().
		Width(width).
		Height(m.layout.main.Dy() - 1).
		PaddingTop(1).
		Render(
			lipgloss.JoinVertical(lipgloss.Left, infoSection, "", content),
		)
}
