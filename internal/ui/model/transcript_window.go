package model

import (
	"context"
	"log/slog"
	"maps"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/ui/chat"
)

const (
	transcriptPageSize = 200

	// NOTE: Counted in chat items, not lines, so the check needs no height
	// measurement. Reading ahead this early keeps a page ready before the
	// user reaches the top.
	transcriptPreloadDivisor = 10
)

// transcriptWindow tracks what is left to read of the open session's
// transcript, of which only the newest messages are in the chat.
type transcriptWindow struct {
	sessionID string

	// NOTE: The cursor is a message ID, not a position. Positions shift
	// when messages are deleted or the database is compacted.
	oldestID string
	hasOlder bool
	loading  bool

	// stale marks a cursor no page can be read before: its message was
	// deleted, or the last read before it failed. It only stops reads; the
	// undo result reads the tail again whether or not it was set.
	stale bool

	// outcomes holds how background agents ended according to the notices
	// loaded so far. A notice only ever follows its agent's call, so these
	// are what the next older page needs.
	outcomes map[string]agenttools.BackgroundAgentStatus
}

// olderMessagesMsg carries one earlier page of the open session's
// transcript, already built into chat items.
type olderMessagesMsg struct {
	sessionID string
	beforeID  string
	items     []chat.MessageItem
	oldestID  string
	hasOlder  bool
	outcomes  map[string]agenttools.BackgroundAgentStatus
	err       error
}

// maybeLoadOlderMessages starts reading the page before the loaded ones once
// the viewport nears the top of what is loaded.
func (m *UI) maybeLoadOlderMessages() tea.Cmd {
	w := &m.transcript
	if m.state != uiChat || m.session == nil || w.sessionID != m.session.ID {
		return nil
	}
	if !w.hasOlder || w.loading || w.stale {
		return nil
	}
	if m.chat.FirstVisibleIndex() > m.chat.Len()/transcriptPreloadDivisor {
		return nil
	}
	w.loading = true
	return tea.Batch(
		m.showPending(olderMessagesInfo),
		m.loadOlderMessagesCmd(w.sessionID, w.oldestID, maps.Clone(w.outcomes)),
	)
}

func (m *UI) loadOlderMessagesCmd(sessionID, beforeID string, seen map[string]agenttools.BackgroundAgentStatus) tea.Cmd {
	return func() tea.Msg {
		page, err := m.com.Workspace.ListMessagePage(context.Background(), sessionID, beforeID, transcriptPageSize)
		if err != nil {
			return olderMessagesMsg{sessionID: sessionID, beforeID: beforeID, err: err}
		}
		items, _, outcomes := m.buildTranscriptItems(sessionID, page.Messages, false, seen)
		msg := olderMessagesMsg{
			sessionID: sessionID,
			beforeID:  beforeID,
			items:     items,
			hasOlder:  page.HasOlder,
			outcomes:  outcomes,
		}
		if len(page.Messages) > 0 {
			msg.oldestID = page.Messages[0].ID
		}
		return msg
	}
}

// handleOlderMessages handles olderMessagesMsg.
func (m *UI) handleOlderMessages(msg olderMessagesMsg) []tea.Cmd {
	w := &m.transcript
	// NOTE: Anything but the page that was asked for is dropped: the user
	// may have switched sessions, or an undo may have reloaded the tail,
	// while the read was in flight.
	if msg.sessionID != m.currentSessionID() || msg.sessionID != w.sessionID || !w.loading || msg.beforeID != w.oldestID {
		// NOTE: A read for the new window may be in flight; its notice is
		// not this message's to clear.
		if !w.loading {
			m.clearPending(olderMessagesInfo)
		}
		return nil
	}
	w.loading = false
	m.clearPending(olderMessagesInfo)
	if msg.err != nil {
		slog.Warn("Failed to load earlier messages", "session_id", msg.sessionID, "error", msg.err)
		// NOTE: Older history still exists, so hasOlder stays set for an undo
		// result to see; stale stops the read from being retried on every update.
		w.stale = true
		return nil
	}

	w.hasOlder = msg.hasOlder && msg.oldestID != ""
	if msg.oldestID != "" {
		w.oldestID = msg.oldestID
	}
	if w.outcomes == nil {
		w.outcomes = make(map[string]agenttools.BackgroundAgentStatus, len(msg.outcomes))
	}
	for id, status := range msg.outcomes {
		if _, newer := w.outcomes[id]; !newer {
			w.outcomes[id] = status
		}
	}

	var cmds []tea.Cmd
	for _, item := range msg.items {
		if !backgroundAgentRunning(item) {
			continue
		}
		if animatable, ok := item.(chat.Animatable); ok {
			if cmd := animatable.StartAnimation(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	m.chat.PrependMessages(msg.items...)
	if cmd := m.chat.ImageTransmitCmds(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// NOTE: Same order as for a freshly loaded transcript: the layout runs
	// first so the list takes the scrollbar-adjusted width before warming.
	m.updateLayoutAndSize()
	cmds = append(cmds, m.chat.BeginWarm())
	return cmds
}

// noteBackgroundOutcomes remembers how background agents ended according to a
// live notice, for the older pages still to be read.
func (m *UI) noteBackgroundOutcomes(outcomes map[string]agenttools.BackgroundAgentStatus) {
	if len(outcomes) == 0 {
		return
	}
	if m.transcript.outcomes == nil {
		m.transcript.outcomes = make(map[string]agenttools.BackgroundAgentStatus, len(outcomes))
	}
	maps.Copy(m.transcript.outcomes, outcomes)
}
