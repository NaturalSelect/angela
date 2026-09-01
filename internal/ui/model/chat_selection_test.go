package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/stretchr/testify/require"
)

// testHighlightableItem is a minimal chat item that is both selectable
// (list.Focusable, so Chat's isSelectable gate accepts it) and
// highlightable (list.Highlightable), letting selection-geometry and mouse
// tests drive the real Chat code paths without pulling in full message
// rendering machinery.
type testHighlightableItem struct {
	testMessageItem
	focused                              bool
	startLine, startCol, endLine, endCol int
}

func (m *testHighlightableItem) SetFocused(focused bool) { m.focused = focused }

func (m *testHighlightableItem) SetHighlight(startLine, startCol, endLine, endCol int) {
	m.startLine, m.startCol, m.endLine, m.endCol = startLine, startCol, endLine, endCol
}

func (m *testHighlightableItem) Highlight() (startLine, startCol, endLine, endCol int) {
	return m.startLine, m.startCol, m.endLine, m.endCol
}

var (
	_ list.Focusable     = (*testHighlightableItem)(nil)
	_ list.Highlightable = (*testHighlightableItem)(nil)
	_ chat.MessageItem   = (*testHighlightableItem)(nil)
)

func TestAbs(t *testing.T) {
	t.Parallel()

	require.Equal(t, 5, abs(5))
	require.Equal(t, 5, abs(-5))
	require.Equal(t, 0, abs(0))
}

// TestFindWordBoundaries pins the column math findWordBoundaries uses to
// locate a double-clicked word, including the CJK case where uax29 breaks
// each Han ideograph into its own token (verified empirically: "你好"
// tokenizes as "你" then "好", each width 2).
func TestFindWordBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		line               string
		col                int
		wantStart, wantEnd int
	}{
		{"mid word selects the whole word", "hello world", 2, 0, 5},
		{"click on space returns an empty selection", "hello world", 5, 5, 5},
		{"click at word start", "hello world", 0, 0, 5},
		{"click past the end of the last token", "hi", 10, 10, 10},
		{"empty line", "", 3, 0, 0},
		{"negative column", "hello", -1, 0, 0},
		{"CJK: click inside the first ideograph", "你好 world", 1, 0, 2},
		{"CJK: click on the trailing space", "你好 world", 4, 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			startCol, endCol := findWordBoundaries(tt.line, tt.col)
			require.Equal(t, tt.wantStart, startCol, "startCol")
			require.Equal(t, tt.wantEnd, endCol, "endCol")
		})
	}
}

func TestChatSelectWord(t *testing.T) {
	t.Parallel()

	offset := chat.MessageLeftPaddingTotal

	t.Run("mid word selects the word boundaries", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "hello world"}}
		u.chat.SetMessages(item)

		u.chat.selectWord(0, 2+offset, 0)

		require.True(t, u.chat.mouseDown)
		require.Equal(t, 0, u.chat.mouseDownItem)
		require.Equal(t, 0+offset, u.chat.mouseDownX)
		require.Equal(t, 5+offset, u.chat.mouseDragX)
		require.Equal(t, 0, u.chat.mouseDownY)
		require.Equal(t, 0, u.chat.mouseDragY)
	})

	t.Run("click on whitespace falls back to single-click state", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "hello world"}}
		u.chat.SetMessages(item)

		x := 5 + offset // the space between "hello" and "world"
		u.chat.selectWord(0, x, 0)

		require.True(t, u.chat.mouseDown)
		require.Equal(t, x, u.chat.mouseDownX)
		require.Equal(t, x, u.chat.mouseDragX)
	})

	t.Run("ANSI styling does not skew the boundaries", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "\x1b[31mhello\x1b[0m world"}}
		u.chat.SetMessages(item)

		u.chat.selectWord(0, 2+offset, 0)

		require.Equal(t, 0+offset, u.chat.mouseDownX)
		require.Equal(t, 5+offset, u.chat.mouseDragX)
	})

	t.Run("no item at index is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.NotPanics(t, func() { u.chat.selectWord(0, 0, 0) })
		require.False(t, u.chat.mouseDown)
	})
}

func TestChatSelectLine(t *testing.T) {
	t.Parallel()

	offset := chat.MessageLeftPaddingTotal

	t.Run("selects the full line accounting for padding", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "line one\nline two"}}
		u.chat.SetMessages(item)

		u.chat.selectLine(0, 1)

		require.True(t, u.chat.mouseDown)
		require.Equal(t, 0, u.chat.mouseDownX)
		require.Equal(t, 1, u.chat.mouseDownY)
		require.Equal(t, len("line two")+offset, u.chat.mouseDragX)
		require.Equal(t, 1, u.chat.mouseDragY)
	})

	t.Run("empty line yields a zero-length selection", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "line one\n\nline three"}}
		u.chat.SetMessages(item)

		u.chat.selectLine(0, 1)

		require.Equal(t, 0, u.chat.mouseDownX)
		require.Equal(t, offset, u.chat.mouseDragX)
	})

	t.Run("out of range line is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "only one line"}}
		u.chat.SetMessages(item)

		u.chat.selectLine(0, 5)

		require.False(t, u.chat.mouseDown)
	})

	t.Run("no item at index is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.NotPanics(t, func() { u.chat.selectLine(0, 0) })
	})
}

func TestChatGetHighlightRange(t *testing.T) {
	t.Parallel()

	t.Run("no active selection returns all -1", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		startItem, startLine, startCol, endItem, endLine, endCol := u.chat.getHighlightRange()
		require.Equal(t, -1, startItem)
		require.Equal(t, -1, startLine)
		require.Equal(t, -1, startCol)
		require.Equal(t, -1, endItem)
		require.Equal(t, -1, endLine)
		require.Equal(t, -1, endCol)
	})

	t.Run("forward drag across items", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 3, 1
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 2, 7, 4

		startItem, startLine, startCol, endItem, endLine, endCol := u.chat.getHighlightRange()
		require.Equal(t, 0, startItem)
		require.Equal(t, 1, startLine)
		require.Equal(t, 3, startCol)
		require.Equal(t, 2, endItem)
		require.Equal(t, 4, endLine)
		require.Equal(t, 7, endCol)
	})

	t.Run("reverse drag (start after end) swaps into a forward range", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 2, 7, 4
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 0, 3, 1

		startItem, startLine, startCol, endItem, endLine, endCol := u.chat.getHighlightRange()
		require.Equal(t, 0, startItem)
		require.Equal(t, 1, startLine)
		require.Equal(t, 3, startCol)
		require.Equal(t, 2, endItem)
		require.Equal(t, 4, endLine)
		require.Equal(t, 7, endCol)
	})

	t.Run("same item same line uses X to determine direction", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 1, 5, 2
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 1, 2, 2

		_, _, startCol, _, _, endCol := u.chat.getHighlightRange()
		require.Equal(t, 2, startCol, "dragging left of the down point is a backward selection")
		require.Equal(t, 5, endCol)
	})
}

func TestChatApplyHighlightRange(t *testing.T) {
	t.Parallel()

	newSpan := func() []*testHighlightableItem {
		return []*testHighlightableItem{
			{testMessageItem: testMessageItem{id: "i0", text: "x"}},
			{testMessageItem: testMessageItem{id: "i1", text: "x"}},
			{testMessageItem: testMessageItem{id: "i2", text: "x"}},
		}
	}

	t.Run("single item selection carries both endpoints", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		items := newSpan()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 1, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 0, 4, 0

		u.chat.applyHighlightRange(0, -1, items[0])
		sLine, sCol, eLine, eCol := items[0].Highlight()
		require.Equal(t, 0, sLine)
		require.Equal(t, 1, sCol)
		require.Equal(t, 0, eLine)
		require.Equal(t, 4, eCol)
	})

	t.Run("first item highlights from the start position to the item end", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		items := newSpan()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 1, 2
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 2, 4, 1

		u.chat.applyHighlightRange(0, -1, items[0])
		sLine, sCol, eLine, eCol := items[0].Highlight()
		require.Equal(t, 2, sLine)
		require.Equal(t, 1, sCol)
		require.Equal(t, -1, eLine)
		require.Equal(t, -1, eCol)
	})

	t.Run("middle item is fully highlighted", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		items := newSpan()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 1, 2
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 2, 4, 1

		u.chat.applyHighlightRange(1, -1, items[1])
		sLine, sCol, eLine, eCol := items[1].Highlight()
		require.Equal(t, 0, sLine)
		require.Equal(t, 0, sCol)
		require.Equal(t, -1, eLine)
		require.Equal(t, -1, eCol)
	})

	t.Run("last item highlights from the item start to the end position", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		items := newSpan()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 1, 2
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 2, 4, 1

		u.chat.applyHighlightRange(2, -1, items[2])
		sLine, sCol, eLine, eCol := items[2].Highlight()
		require.Equal(t, 0, sLine)
		require.Equal(t, 0, sCol)
		require.Equal(t, 1, eLine)
		require.Equal(t, 4, eCol)
	})

	t.Run("item outside the range receives no highlight", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		items := newSpan()
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 1, 1, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 1, 4, 0

		u.chat.applyHighlightRange(0, -1, items[0])
		sLine, sCol, eLine, eCol := items[0].Highlight()
		require.Equal(t, -1, sLine)
		require.Equal(t, -1, sCol)
		require.Equal(t, -1, eLine)
		require.Equal(t, -1, eCol)
	})

	t.Run("non-highlightable item is returned unchanged", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := testMessageItem{id: "p", text: "x"}
		got := u.chat.applyHighlightRange(0, -1, item)
		require.Equal(t, item, got)
	})
}

func TestChatHighlightContent(t *testing.T) {
	t.Parallel()

	t.Run("returns the highlighted substring across items", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		a := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		b := &testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "beta"}}
		c := &testHighlightableItem{testMessageItem: testMessageItem{id: "c", text: "gamma"}}
		u.chat.SetMessages(a, b, c)
		u.updateLayoutAndSize()

		// HighlightContent treats identical overall (Y, X) drag endpoints as
		// an empty selection regardless of item index, so the down/drag
		// columns must differ even though both land on line 0.
		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 1, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 2, 3, 0
		u.chat.list.Render() // Runs the applyHighlightRange callback on each visible item.

		got := u.chat.HighlightContent()
		require.Contains(t, got, "lpha")
		require.Contains(t, got, "beta")
		require.Contains(t, got, "gam")
	})

	t.Run("empty selection returns an empty string", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		a := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		u.chat.SetMessages(a)
		u.updateLayoutAndSize()

		u.chat.mouseDownItem, u.chat.mouseDownX, u.chat.mouseDownY = 0, 2, 0
		u.chat.mouseDragItem, u.chat.mouseDragX, u.chat.mouseDragY = 0, 2, 0

		require.Empty(t, u.chat.HighlightContent())
	})

	t.Run("no active selection returns an empty string", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		a := &testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "alpha"}}
		u.chat.SetMessages(a)
		u.updateLayoutAndSize()

		require.Empty(t, u.chat.HighlightContent())
	})
}
