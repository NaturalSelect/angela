package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/stretchr/testify/require"
)

func tallThenShortItems() []chat.MessageItem {
	return []chat.MessageItem{
		&mutableMessageItem{id: "tall", lines: 200},
		testMessageItem{id: "s1", text: "short 1"},
		testMessageItem{id: "s2", text: "short 2"},
	}
}

func TestChatAtBottomWithTallItemAtViewportTop(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(tallThenShortItems()...)
	u.updateLayoutAndSize()

	u.chat.ScrollToBottom()

	require.True(t, u.chat.AtBottom(),
		"a viewport pinned to the end is at the bottom even when its top item is taller than the viewport")
}

func TestChatWheelDownToEndResumesFollow(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(tallThenShortItems()...)
	u.updateLayoutAndSize()

	u.chat.ScrollBy(-1000)
	require.False(t, u.chat.Follow(), "scrolling up leaves follow mode")

	u.chat.ScrollBy(1000)
	require.True(t, u.chat.Follow(), "scrolling back to the end resumes follow mode")
}

func TestChatWheelUpOnShortTranscriptKeepsFollow(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "s1", text: "short 1"},
		testMessageItem{id: "s2", text: "short 2"},
	)
	u.updateLayoutAndSize()
	require.True(t, u.chat.AtBottom())

	u.chat.ScrollBy(-3)

	require.True(t, u.chat.Follow(),
		"a transcript that fits the viewport has nothing to scroll back to, so the view keeps following")
}
