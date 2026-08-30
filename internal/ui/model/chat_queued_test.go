package model

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/stretchr/testify/require"
)

// renderedOrder returns the chat list's rendered text, so tests can assert
// where items sit relative to each other. Selection is not usable for this:
// queued entries are deliberately not focusable, so SelectLast skips them.
func renderedOrder(t *testing.T, u *UI) string {
	t.Helper()
	u.updateLayoutAndSize()
	return u.chat.list.Render()
}

// requireOrder asserts that every needle appears, in the given order.
func requireOrder(t *testing.T, rendered string, needles ...string) {
	t.Helper()
	prev := -1
	for _, needle := range needles {
		at := strings.Index(rendered, needle)
		require.NotEqual(t, -1, at, "%q missing from the transcript", needle)
		require.Greater(t, at, prev, "%q is out of order", needle)
		prev = at
	}
}

func TestQueuedPromptsParkAtTheEnd(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "a", text: "alpha"},
		testMessageItem{id: "b", text: "beta"},
	)

	u.chat.SetQueued(
		chat.NewQueuedMessageItem(u.com.Styles, "first waiting", 0),
		chat.NewQueuedMessageItem(u.com.Styles, "second waiting", 1),
	)

	require.Equal(t, 4, u.chat.list.Len())
	requireOrder(t, renderedOrder(t, u), "alpha", "beta", "first waiting", "second waiting")
}

// TestQueuedPromptsAreReplacedNotAccumulated pins the tail bookkeeping:
// the queue is refetched repeatedly while the agent works, and each
// answer must replace the previous tail rather than stack on it.
func TestQueuedPromptsAreReplacedNotAccumulated(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})

	for range 5 {
		u.chat.SetQueued(
			chat.NewQueuedMessageItem(u.com.Styles, "still waiting", 0),
		)
	}

	require.Equal(t, 2, u.chat.list.Len(), "repeated refreshes must not stack tails")
}

func TestAnEmptyQueueRemovesTheTail(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})
	u.chat.SetQueued(chat.NewQueuedMessageItem(u.com.Styles, "waiting", 0))
	require.Equal(t, 2, u.chat.list.Len())

	u.chat.SetQueued()

	require.Equal(t, 1, u.chat.list.Len())
	require.NotContains(t, renderedOrder(t, u), "waiting")
}

// TestNewMessagesLandAboveTheQueue is the ordering invariant that matters:
// a queued prompt has not been sent yet, so it must never appear above a
// message that already has.
func TestNewMessagesLandAboveTheQueue(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})
	u.chat.SetQueued(chat.NewQueuedMessageItem(u.com.Styles, "waiting", 0))

	u.chat.AppendMessages(testMessageItem{id: "b", text: "beta"})

	require.Equal(t, 3, u.chat.list.Len())
	requireOrder(t, renderedOrder(t, u), "alpha", "beta", "waiting")
	require.Equal(t, 1, u.chat.idInxMap["b"],
		"a real message must not be indexed past the queued tail")
}

func TestReloadingTheTranscriptClearsTheQueue(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})
	u.chat.SetQueued(chat.NewQueuedMessageItem(u.com.Styles, "waiting", 0))

	u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})

	require.Equal(t, 1, u.chat.list.Len())
	require.Empty(t, u.chat.queued)
}

// TestAQueuedPromptRendersItsText covers the reported symptom: the count
// was visible in the status bar but the prompt text was nowhere.
func TestAQueuedPromptRendersItsText(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	item := chat.NewQueuedMessageItem(u.com.Styles, "please refactor the parser", 0)

	require.Contains(t, item.Render(80), "please refactor the parser")
	require.Contains(t, item.Render(80), "queued")
}
