package model

import (
	"cmp"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// mcpInfo renders the MCP status section showing active MCP clients and their
// tool/prompt counts.
func (m *UI) mcpInfo(width, maxItems int, isSection bool) string {
	var mcps []mcp.ClientInfo
	t := m.com.Styles

	for _, mcp := range m.com.Config().MCP.Sorted() {
		if state, ok := m.mcpStates[mcp.Name]; ok {
			mcps = append(mcps, state)
		}
	}

	title := t.Resource.Heading.Render("MCPs")
	if isSection {
		title = common.Section(t, title, width)
	}
	list := t.Resource.AdditionalText.Render("None")
	if len(mcps) > 0 {
		list = mcpList(t, mcps, width, maxItems)
	}

	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, list))
}

// mcpCounts formats tool, prompt, and resource counts for display.
func mcpCounts(t *styles.Styles, counts mcp.Counts) string {
	var parts []string
	if counts.Tools > 0 {
		parts = append(parts, t.Resource.CapabilityCount.Render(fmt.Sprintf("%d tools", counts.Tools)))
	}
	if counts.Prompts > 0 {
		parts = append(parts, t.Resource.CapabilityCount.Render(fmt.Sprintf("%d prompts", counts.Prompts)))
	}
	if counts.Resources > 0 {
		parts = append(parts, t.Resource.CapabilityCount.Render(fmt.Sprintf("%d resources", counts.Resources)))
	}
	return strings.Join(parts, " ")
}

// mcpList renders a list of MCP clients with their status and counts,
// truncating to maxItems if needed.
func mcpList(t *styles.Styles, mcps []mcp.ClientInfo, width, maxItems int) string {
	if maxItems <= 0 {
		return ""
	}
	var renderedMcps []string

	for _, m := range mcps {
		var icon string
		title := m.Name
		// Show "Docker MCP" instead of the config name for Docker MCP.
		if m.Name == config.DockerMCPName {
			title = "Docker MCP"
		}
		title = t.Resource.Name.Render(title)
		var description string
		var extraContent string

		switch m.State {
		case mcp.StateStarting:
			icon = t.Resource.BusyIcon.String()
			description = t.Resource.StatusText.Render("starting...")
		case mcp.StateConnected:
			icon = t.Resource.OnlineIcon.String()
			extraContent = mcpCounts(t, m.Counts)
		case mcp.StateError:
			icon = t.Resource.ErrorIcon.String()
			description = t.Resource.StatusText.Render("error")
			if m.Error != nil {
				description = t.Resource.StatusText.Render(fmt.Sprintf("error: %s", m.Error.Error()))
			}
		case mcp.StateNeedsAuth:
			icon = t.Resource.NeedsAuthIcon.String()
			description = t.Resource.StatusText.Render("needs authentication")
		case mcp.StateDisabled:
			icon = t.Resource.DisabledIcon.String()
			description = t.Resource.StatusText.Render("disabled")
		default:
			icon = t.Resource.OfflineIcon.String()
		}

		renderedMcps = append(renderedMcps, common.Status(t, common.StatusOpts{
			Icon:         icon,
			Title:        title,
			Description:  description,
			ExtraContent: extraContent,
		}, width))
	}

	if len(renderedMcps) > maxItems {
		visibleItems := renderedMcps[:maxItems-1]
		remaining := len(renderedMcps) - maxItems
		visibleItems = append(visibleItems, t.Resource.AdditionalText.Render(fmt.Sprintf("…and %d more", remaining)))
		return lipgloss.JoinVertical(lipgloss.Left, visibleItems...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, renderedMcps...)
}

// handleRunMCPPrompt handles dialog.ActionRunMCPPrompt.
func (m *UI) handleRunMCPPrompt(msg dialog.ActionRunMCPPrompt) tea.Cmd {
	var cmds []tea.Cmd
	if len(msg.Arguments) > 0 && msg.Args == nil {
		m.dialog.CloseFrontDialog()
		title := cmp.Or(msg.Title, "MCP Prompt Arguments")
		argsDialog := dialog.NewArguments(
			m.com,
			title,
			msg.Description,
			msg.Arguments,
			msg, // Pass the action as the result
		)
		m.dialog.OpenDialog(argsDialog)
		return tea.Batch(cmds...)
	}
	cmds = append(cmds, m.runMCPPrompt(msg.ClientID, msg.PromptID, msg.Args))
	return tea.Batch(cmds...)
}

// handleMCPEvent handles pubsub.Event[mcp.Event]. handled reports that
// Update must return cmd right away instead of running its shared tail.
func (m *UI) handleMCPEvent(msg pubsub.Event[mcp.Event]) (cmd tea.Cmd, handled bool) {
	switch msg.Payload.Type {
	case mcp.EventStateChanged:
		return tea.Batch(
			m.handleStateChanged(),
			m.loadMCPrompts,
		), true
	case mcp.EventPromptsListChanged:
		return handleMCPPromptsEvent(m.com.Workspace, msg.Payload.Name), true
	case mcp.EventToolsListChanged:
		return handleMCPToolsEvent(m.com.Workspace, msg.Payload.Name), true
	case mcp.EventResourcesListChanged:
		return handleMCPResourcesEvent(m.com.Workspace, msg.Payload.Name), true
	}
	return nil, false
}
