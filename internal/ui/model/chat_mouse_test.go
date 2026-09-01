package model

import (
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// testExpandableItem is a minimal chat item that is selectable, reports
// mouse clicks as handled or not (controlled by the test), and toggles an
// expanded flag, exercising HandleDelayedClick's expand-on-click wiring
// without pulling in real message rendering geometry.
type testExpandableItem struct {
	testMessageItem
	focused      bool
	clickHandled bool
	expanded     bool
}

func (m *testExpandableItem) SetFocused(focused bool) { m.focused = focused }

func (m *testExpandableItem) HandleMouseClick(btn ansi.MouseButton, x, y int) bool {
	return m.clickHandled
}

func (m *testExpandableItem) ToggleExpanded() bool {
	m.expanded = !m.expanded
	return m.expanded
}

var (
	_ list.Focusable      = (*testExpandableItem)(nil)
	_ list.MouseClickable = (*testExpandableItem)(nil)
	_ chat.Expandable     = (*testExpandableItem)(nil)
)

func TestChatHandleMouseDown(t *testing.T) {
	t.Parallel()

	t.Run("empty list returns false", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.updateLayoutAndSize()

		handled, cmd := u.chat.HandleMouseDown(0, 0)
		require.False(t, handled)
		require.Nil(t, cmd)
	})

	t.Run("non-selectable item returns false", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})
		u.updateLayoutAndSize()

		handled, cmd := u.chat.HandleMouseDown(0, 0)
		require.False(t, handled)
		require.Nil(t, cmd)
	})

	t.Run("single click selects the item and schedules a delayed click", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		handled, cmd := u.chat.HandleMouseDown(0, 0)
		require.True(t, handled)
		require.NotNil(t, cmd, "single click schedules a DelayedClickMsg")
		require.True(t, u.chat.mouseDown)
		require.Equal(t, 0, u.chat.mouseDownItem)
		require.Equal(t, 0, u.chat.list.Selected())
		require.Equal(t, 1, u.chat.clickCount)
	})

	t.Run("double click within tolerance selects a word", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "hello world"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		offset := chat.MessageLeftPaddingTotal
		x := 2 + offset // inside "hello"
		_, _ = u.chat.HandleMouseDown(x, 0)
		handled, cmd := u.chat.HandleMouseDown(x, 0)

		require.True(t, handled)
		require.Nil(t, cmd, "double click does not schedule a delayed single-click action")
		require.Equal(t, 2, u.chat.clickCount)
		require.Equal(t, 0+offset, u.chat.mouseDownX)
		require.Equal(t, 5+offset, u.chat.mouseDragX, "drag end should land on the word boundary")
	})

	t.Run("triple click within tolerance selects the line and resets the counter", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "hello world"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		_, _ = u.chat.HandleMouseDown(0, 0)
		handled, cmd := u.chat.HandleMouseDown(0, 0)

		require.True(t, handled)
		require.Nil(t, cmd)
		require.Equal(t, 0, u.chat.clickCount, "triple click resets the counter")
		offset := chat.MessageLeftPaddingTotal
		require.Equal(t, len("hello world")+offset, u.chat.mouseDragX)
	})

	t.Run("click far away in position starts a new single-click sequence", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "hello world"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		handled, cmd := u.chat.HandleMouseDown(50, 0)

		require.True(t, handled)
		require.NotNil(t, cmd)
		require.Equal(t, 1, u.chat.clickCount, "a click far from the last one is not a multi-click")
	})
}

func TestChatHandleDelayedClick(t *testing.T) {
	t.Parallel()

	t.Run("matching click ID toggles expansion when the item reports it handled the click", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testExpandableItem{
			testMessageItem: testMessageItem{id: "a", text: "alpha"},
			clickHandled:    true,
		}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		clickID := u.chat.pendingClickID

		handled := u.chat.HandleDelayedClick(DelayedClickMsg{ClickID: clickID, ItemIdx: 0, X: 0, Y: 0})

		require.True(t, handled)
		require.True(t, item.expanded)
	})

	t.Run("stale click ID (superseded by a newer click) is ignored", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testExpandableItem{
			testMessageItem: testMessageItem{id: "a", text: "alpha"},
			clickHandled:    true,
		}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		staleID := u.chat.pendingClickID
		u.chat.ClearMouse() // bumps pendingClickID, invalidating staleID

		handled := u.chat.HandleDelayedClick(DelayedClickMsg{ClickID: staleID, ItemIdx: 0, X: 0, Y: 0})

		require.False(t, handled)
		require.False(t, item.expanded)
	})

	t.Run("an active text highlight suppresses the delayed click", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testExpandableItem{
			testMessageItem: testMessageItem{id: "a", text: "alpha"},
			clickHandled:    true,
		}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		clickID := u.chat.pendingClickID
		// Simulate a drag that produced a real (non-empty) highlight.
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 0, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 0, 3, 0

		handled := u.chat.HandleDelayedClick(DelayedClickMsg{ClickID: clickID, ItemIdx: 0, X: 0, Y: 0})

		require.False(t, handled)
		require.False(t, item.expanded)
	})

	t.Run("item that reports the click as unhandled does not toggle expansion", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testExpandableItem{
			testMessageItem: testMessageItem{id: "a", text: "alpha"},
			clickHandled:    false,
		}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		clickID := u.chat.pendingClickID

		handled := u.chat.HandleDelayedClick(DelayedClickMsg{ClickID: clickID, ItemIdx: 0, X: 0, Y: 0})

		require.False(t, handled)
		require.False(t, item.expanded)
	})
}

func TestChatHandleMouseDrag(t *testing.T) {
	t.Parallel()

	t.Run("no-op when the mouse is not down", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		require.False(t, u.chat.HandleMouseDrag(0, 0))
	})

	t.Run("updates the drag position while the mouse is down", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		handled := u.chat.HandleMouseDrag(3, 0)

		require.True(t, handled)
		require.Equal(t, 0, u.chat.mouseDragItem)
		require.Equal(t, 3, u.chat.mouseDragX)
	})

	t.Run("empty list returns false", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.updateLayoutAndSize()
		u.chat.mouseDown = true // force past the mouseDown guard

		require.False(t, u.chat.HandleMouseDrag(0, 0))
	})
}

func TestChatHandleMouseUp(t *testing.T) {
	t.Parallel()

	t.Run("clears mouseDown when it was set", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()

		_, _ = u.chat.HandleMouseDown(0, 0)
		require.True(t, u.chat.HandleMouseUp(0, 0))
		require.False(t, u.chat.mouseDown)
	})

	t.Run("returns false when the mouse was not down", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.False(t, u.chat.HandleMouseUp(0, 0))
	})
}

func TestChatClearMouse(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "hello world"}}
	u.chat.SetMessages(item)
	u.updateLayoutAndSize()

	_, _ = u.chat.HandleMouseDown(0, 0)
	_ = u.chat.HandleMouseDrag(3, 0)
	prevClickID := u.chat.pendingClickID

	u.chat.ClearMouse()

	require.False(t, u.chat.mouseDown)
	require.Equal(t, -1, u.chat.mouseDownItem)
	require.Equal(t, -1, u.chat.mouseDragItem)
	require.Equal(t, time.Time{}, u.chat.lastClickTime)
	require.Equal(t, 0, u.chat.lastClickX)
	require.Equal(t, 0, u.chat.lastClickY)
	require.Equal(t, 0, u.chat.clickCount)
	require.Greater(t, u.chat.pendingClickID, prevClickID, "must invalidate any pending delayed click")
}

func TestChatHasHighlight(t *testing.T) {
	t.Parallel()

	t.Run("false before any selection", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.False(t, u.chat.HasHighlight())
	})

	t.Run("false for a zero-length selection", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 2, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 0, 2, 0

		require.False(t, u.chat.HasHighlight())
	})

	t.Run("true once the drag produces a real range", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 2, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 0, 5, 0

		require.True(t, u.chat.HasHighlight())
	})
}
