package agent

import (
	"context"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
)

// lastEditorNotice returns the text of the newest editor notice in msgs, or
// "" when there is none. After a compaction msgs starts at the summary, so
// the notices the summary replaced no longer count.
func lastEditorNotice(msgs []message.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].IsReminder() {
			continue
		}
		if notice, ok := reminder.PersistedEditorNotice(msgs[i].Content().Text); ok {
			return notice
		}
	}
	return ""
}

// createEditorNotice persists a notice of what the user's VS Code window
// shows, ahead of the user's prompt. It does nothing when nothing is
// connected or when the notice would repeat last, the text of the one
// already in the conversation. It returns the messages to send and the text
// of the notice now in effect.
func (a *sessionAgent) createEditorNotice(ctx context.Context, sessionID, last string) ([]fantasy.Message, string, error) {
	if a.editorContext == nil {
		return nil, last, nil
	}
	editor, ok := a.editorContext()
	if !ok {
		return nil, last, nil
	}
	text := reminder.EditorNotice(editor, last)
	if text == "" {
		return nil, last, nil
	}

	slog.Debug("Injecting editor context", "session_id", sessionID)
	msg, err := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: reminder.Wrap(text)}},
	})
	if err != nil {
		return nil, last, fmt.Errorf("failed to persist editor context: %w", err)
	}
	return msg.ToAIMessage(), text, nil
}
