package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/stretchr/testify/require"
)

// assistantWithCall is an assistant message carrying one unfinished tool
// call. Whether a result for it exists elsewhere in the transcript is what
// the interruption predicate reads.
func assistantWithCall(msgID, callID string) message.Message {
	return message.Message{
		ID:        msgID,
		SessionID: "s1",
		Role:      message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: callID, Name: "bash", Input: "{}", Finished: false},
		},
	}
}

func toolResultMessage(msgID, callID string) message.Message {
	return message.Message{
		ID:        msgID,
		SessionID: "s1",
		Role:      message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: callID, Content: "ok"},
		},
	}
}

func toolItems(t *testing.T, items []chat.MessageItem) []chat.ToolMessageItem {
	t.Helper()
	var tools []chat.ToolMessageItem
	for _, item := range items {
		if tool, ok := item.(chat.ToolMessageItem); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// Reloading a transcript whose session is not running is the restart case:
// every call still missing a result was orphaned when the process died, so
// none of them may come back spinning.
func TestBuildSessionItemsMarksOrphansWhenSessionIsIdle(t *testing.T) {
	t.Parallel()

	ws := &countingWorkspace{ready: true, agentBusy: false}
	m := newBusyUI(ws)

	items, _ := m.buildSessionItems("s1", []message.Message{
		assistantWithCall("m1", "c1"),
		assistantWithCall("m2", "c2"),
	})

	tools := toolItems(t, items)
	require.Len(t, tools, 2)
	for _, tool := range tools {
		require.Equal(t, chat.ToolStatusCanceled, tool.Status(),
			"nothing is running, so no call can still be waiting on a result")
	}
}

// A session that really is running gets to keep exactly one message
// pending: the last assistant one. Earlier calls were answered before the
// next assistant message was written, so an earlier call without a result
// is an orphan even while a run is in flight — which is what the window
// right after "restart, then send a new prompt" looks like.
func TestBuildSessionItemsKeepsOnlyTheLastAssistantRunning(t *testing.T) {
	t.Parallel()

	ws := &countingWorkspace{ready: true, agentBusy: true}
	m := newBusyUI(ws)

	items, _ := m.buildSessionItems("s1", []message.Message{
		assistantWithCall("m1", "c1"),
		assistantWithCall("m2", "c2"),
	})

	tools := toolItems(t, items)
	require.Len(t, tools, 2)
	require.Equal(t, chat.ToolStatusCanceled, tools[0].Status(),
		"a call left behind by an earlier turn is an orphan even mid-run")
	require.Equal(t, chat.ToolStatusRunning, tools[1].Status(),
		"the live turn's own call is genuinely pending")
}

// The probe is a round trip in client/server mode, and a transcript with
// every result in place has nothing to decide. Paying for it on every load
// would double the cost of opening a session for no answer.
func TestBuildSessionItemsSkipsTheProbeWithoutOrphans(t *testing.T) {
	t.Parallel()

	ws := &countingWorkspace{ready: true, agentBusy: false}
	m := newBusyUI(ws)

	items, _ := m.buildSessionItems("s1", []message.Message{
		assistantWithCall("m1", "c1"),
		toolResultMessage("t1", "c1"),
	})

	require.Zero(t, ws.sessionBusyCalls,
		"a transcript with no orphaned call must not probe for liveness")

	tools := toolItems(t, items)
	require.Len(t, tools, 1)
	require.NotEqual(t, chat.ToolStatusCanceled, tools[0].Status(),
		"a call that reported back is not interrupted")
}

// The liveness question is asked about this session, not about the process.
// Reading the global busy cache instead would let any other session's run
// keep these orphans spinning.
func TestBuildSessionItemsAsksPerSession(t *testing.T) {
	t.Parallel()

	ws := &countingWorkspace{ready: true, agentBusy: false}
	m := newBusyUI(ws)
	// A stale global cache claiming the process is busy must not leak
	// into the per-session answer.
	m.agentBusyCache.set(true)

	items, _ := m.buildSessionItems("s1", []message.Message{
		assistantWithCall("m1", "c1"),
	})

	require.Equal(t, 1, ws.sessionBusyCalls, "exactly one per-session probe")

	tools := toolItems(t, items)
	require.Len(t, tools, 1)
	require.Equal(t, chat.ToolStatusCanceled, tools[0].Status(),
		"the per-session answer wins over the process-wide cache")
}
