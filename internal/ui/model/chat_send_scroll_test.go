package model

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/stretchr/testify/require"
)

// TestSendMessageThenWheelStaysNearBottom pins that scrolling right after
// sending a prompt moves relative to the bottom of the transcript, not
// relative to wherever the selection sat while the user was reading history.
func TestSendMessageThenWheelStaysNearBottom(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	ws.EXPECT().AgentReadyErr().Return(nil)

	const count = 60
	msgs := make([]chat.MessageItem, 0, count)
	for i := range count {
		id := strconv.Itoa(i)
		msgs = append(msgs, &testHighlightableItem{
			testMessageItem: testMessageItem{id: "m-" + id, text: "message " + id},
		})
	}
	m.chat.SetMessages(msgs...)
	m.updateLayoutAndSize()

	m.chat.ScrollToIndex(5)
	m.chat.SetSelected(5)

	m.sendMessage("hello") // returned cmds (AgentRun etc.) deliberately not run
	m.Update(common.CoalescedWheelMsg{Mouse: tea.Mouse{X: -100, Y: -100}, DeltaY: -3})

	// Three wheel lines span at most two one-line items plus their gaps.
	_, lastVisible := m.chat.list.VisibleItemIndices()
	require.GreaterOrEqual(t, lastVisible, count-3,
		"wheel after sending must stay near the end of the transcript")
	require.True(t, m.chat.SelectedItemInView())
}
