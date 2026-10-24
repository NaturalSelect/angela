package acp

import (
	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// sessionStateUpdates is what the editor shows beside the transcript: the
// title, context usage and the todo plan.
//
// NOTE: The usage update is skipped while the context window is unknown,
// because the protocol makes its size mandatory. Angela's todos carry no
// priority, so every plan entry is medium. Prices are in US dollars.
func sessionStateUpdates(sess session.Session, active workspace.ActiveAgent) []protocol.SessionUpdate {
	title := sess.Title
	updates := []protocol.SessionUpdate{protocol.SessionInfoUpdate{Title: &title}}

	if window := active.CatwalkCfg.ContextWindow; window > 0 {
		updates = append(updates, protocol.UsageUpdate{
			Used: sess.PromptTokens + sess.CompletionTokens,
			Size: window,
			Cost: &protocol.Cost{Amount: sess.Cost, Currency: "USD"},
		})
	}

	entries := make([]protocol.PlanEntry, 0, len(sess.Todos))
	for _, todo := range sess.Todos {
		entries = append(entries, protocol.PlanEntry{
			Content:  todo.Content,
			Priority: protocol.PlanPriorityMedium,
			Status:   protocol.PlanStatus(todo.Status),
		})
	}
	return append(updates, protocol.Plan{Entries: entries})
}
