package model

import (
	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
)

// noticeOutcomes reads how each background agent ended according to the
// notices a reminder carries, keyed by agent ID. It returns nil when the
// reminder holds none.
func noticeOutcomes(msg *message.Message) map[string]agenttools.BackgroundAgentStatus {
	notices := agenttools.ParseBackgroundAgentNotices(msg.Content().Text)
	if len(notices) == 0 {
		return nil
	}
	outcomes := make(map[string]agenttools.BackgroundAgentStatus, len(notices))
	for _, notice := range notices {
		outcomes[notice.AgentID] = notice.Status
	}
	return outcomes
}

// applyBackgroundOutcomes hands each outcome to the agent call that
// launched the agent it belongs to.
func applyBackgroundOutcomes(items []*chat.AgentToolMessageItem, outcomes map[string]agenttools.BackgroundAgentStatus) {
	for _, item := range items {
		if status, ok := outcomes[item.AgentID()]; ok {
			item.SetBackgroundOutcome(status)
		}
	}
}

// agentToolItemsIn picks the agent calls out of a freshly built transcript.
func agentToolItemsIn(items []chat.MessageItem) []*chat.AgentToolMessageItem {
	var agents []*chat.AgentToolMessageItem
	for _, item := range items {
		if agent, ok := item.(*chat.AgentToolMessageItem); ok {
			agents = append(agents, agent)
		}
	}
	return agents
}

// backgroundAgentRunning reports whether the item is the call of a
// background agent that has not ended. Such an agent outlives the turn that
// launched it, so neither the spinner nor the turn status can follow the
// turn's busy state for it.
func backgroundAgentRunning(item chat.MessageItem) bool {
	agent, ok := item.(*chat.AgentToolMessageItem)
	return ok && agent.BackgroundRunning()
}

// settleBackgroundAgents gives every background agent in a freshly built
// transcript its outcome. The notices found in the transcript decide first.
// An agent with no notice is either still running or lost with a restart,
// and only its session can tell which: one that is not busy is marked
// failed. The mark is a guess, so a notice that lands later replaces it.
//
// It touches no UI state, so it is safe to run from a command.
func (m *UI) settleBackgroundAgents(items []chat.MessageItem, outcomes map[string]agenttools.BackgroundAgentStatus) {
	agents := agentToolItemsIn(items)
	applyBackgroundOutcomes(agents, outcomes)
	for _, agent := range agents {
		if !agent.BackgroundRunning() {
			continue
		}
		childID := m.com.Workspace.CreateAgentToolSessionID(agent.MessageID(), agent.ToolCall().ID)
		if !m.com.Workspace.AgentIsSessionBusy(childID) {
			agent.SetBackgroundOutcome(agenttools.BackgroundAgentFailed)
		}
	}
}

// stopSubSessionCmd cancels a child session off the Update goroutine. For a
// background agent that ends the run, and the notice it leaves behind
// reaches the transcript like any other.
func (m *UI) stopSubSessionCmd(sessionID string) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		ws.AgentCancel(sessionID)
		return nil
	}
}
