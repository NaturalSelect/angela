package model

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
)

// newDurationUI is a UI whose config can name a model, which the end-of-turn
// footer needs to render.
func newDurationUI(t *testing.T) *UI {
	t.Helper()

	ws := NewMockWorkspace(gomock.NewController(t))
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()
	return newBusyUIWithWorkspace(ws)
}

func endOfTurnReply(id string, createdAt, finishedAt int64) message.Message {
	return message.Message{
		ID:        id,
		SessionID: "s1",
		Role:      message.Assistant,
		CreatedAt: createdAt,
		Parts: []message.ContentPart{
			message.TextContent{Text: "done"},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: finishedAt},
		},
	}
}

func prompt(id string, createdAt int64) message.Message {
	return message.Message{
		ID:        id,
		SessionID: "s1",
		Role:      message.User,
		CreatedAt: createdAt,
		Parts:     []message.ContentPart{message.TextContent{Text: "go"}},
	}
}

func footerOf(t *testing.T, items []chat.MessageItem, messageID string) string {
	t.Helper()
	for _, item := range items {
		if item.ID() == chat.AssistantInfoID(messageID) {
			return ansi.Strip(item.Render(80))
		}
	}
	require.Failf(t, "no footer", "message %s has no end-of-turn footer", messageID)
	return ""
}

// A page can start in the middle of a turn, so its first message says
// nothing about when the user asked. The duration runs from the prompt the
// page was handed.
func TestBuildTranscriptItemsTimesAMidTurnPageFromItsPrompt(t *testing.T) {
	t.Parallel()
	m := newDurationUI(t)

	items, lastPrompt, _ := m.buildTranscriptItems("s1",
		[]message.Message{endOfTurnReply("a1", 150, 160)}, 100, false, nil)

	require.Contains(t, footerOf(t, items, "a1"), "in 1m0s")
	require.EqualValues(t, 100, lastPrompt, "the live timer starts from the same prompt")
}

func TestBuildTranscriptItemsTimesEachTurnFromItsOwnPrompt(t *testing.T) {
	t.Parallel()
	m := newDurationUI(t)

	items, lastPrompt, _ := m.buildTranscriptItems("s1", []message.Message{
		endOfTurnReply("a1", 150, 160),
		prompt("u2", 200),
		endOfTurnReply("a2", 220, 230),
	}, 100, false, nil)

	require.Contains(t, footerOf(t, items, "a1"), "in 1m0s", "the turn the page starts in is timed from the carried prompt")
	require.Contains(t, footerOf(t, items, "a2"), "in 30s", "a prompt on the page restarts the clock")
	require.EqualValues(t, 200, lastPrompt)
}

// With no prompt to carry, the first message is the best start there is.
func TestBuildTranscriptItemsFallsBackToTheFirstMessage(t *testing.T) {
	t.Parallel()
	m := newDurationUI(t)

	items, lastPrompt, _ := m.buildTranscriptItems("s1",
		[]message.Message{endOfTurnReply("a1", 150, 160)}, 0, false, nil)

	require.Contains(t, footerOf(t, items, "a1"), "in 10s")
	require.EqualValues(t, 150, lastPrompt)
}
