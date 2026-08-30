package model

import (
	"strconv"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

type historyWorkspace struct {
	workspace.Workspace
}

func (historyWorkspace) Config() *config.Config {
	return &config.Config{}
}

func (historyWorkspace) PermissionSkipRequests() bool {
	return false
}

func TestHistoryBangCommandStripsPrefixWhileAlreadyInBangMode(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com.Workspace = historyWorkspace{}
	u.promptHistory.messages = []string{"!echo one", "!echo two"}
	u.promptHistory.index = -1

	require.True(t, u.historyPrev())
	require.True(t, u.bangMode)
	require.Equal(t, "echo one", u.textarea.Value())

	require.True(t, u.historyPrev())
	require.True(t, u.bangMode)
	require.Equal(t, "echo two", u.textarea.Value())
}

// scrolledUpUI returns a UI whose transcript is long enough to scroll and is
// parked at the top — the state in which down means "back to the latest".
func scrolledUpUI(t *testing.T) *UI {
	t.Helper()

	u := newTestUI()
	u.com.Workspace = historyWorkspace{}
	// Zero means "sitting on the newest history entry", which would let
	// down navigate history instead of reaching the transcript.
	u.promptHistory.index = -1

	msgs := make([]chat.MessageItem, 0, 60)
	for i := range 60 {
		msgs = append(msgs, testMessageItem{
			id:   "m-" + strconv.Itoa(i),
			text: "message " + strconv.Itoa(i),
		})
	}
	u.chat.SetMessages(msgs...)
	u.updateLayoutAndSize()
	u.chat.ScrollToTop()
	require.False(t, u.chat.AtBottom())
	return u
}

// One stray down must not yank the view away from what the user scrolled up
// to read: the first press only arms the jump, the second performs it.
func TestDownJumpsToBottomOnlyOnSecondPress(t *testing.T) {
	t.Parallel()

	u := scrolledUpUI(t)

	require.NotNil(t, u.handleHistoryDown(nil), "first down must start the expiry timer")
	require.True(t, u.isJumpingToBottom)
	require.False(t, u.chat.AtBottom(), "first down must leave the view where it was")

	u.handleHistoryDown(nil)
	require.False(t, u.isJumpingToBottom, "a completed jump must disarm")
	require.True(t, u.chat.AtBottom())
}

// The armed state is transient: once the timer expires, the next down is a
// first press again rather than the second half of a stale pair.
func TestDownJumpArmingExpires(t *testing.T) {
	t.Parallel()

	u := scrolledUpUI(t)

	u.handleHistoryDown(nil)
	require.True(t, u.isJumpingToBottom)

	u.Update(jumpToBottomTimerExpiredMsg{})
	require.False(t, u.isJumpingToBottom)
	require.False(t, u.chat.AtBottom(), "expiry must not scroll on its own")

	u.handleHistoryDown(nil)
	require.True(t, u.isJumpingToBottom)
	require.False(t, u.chat.AtBottom())
}

// While armed, the editor says what the second press will do, the way esc
// announces "press again to cancel".
func TestEditorPlaceholderPromptsForSecondDown(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.width = 120
	u.isJumpingToBottom = true

	require.Equal(t, "Press ↓ again to jump to the latest message", u.editorPlaceholder())
}
