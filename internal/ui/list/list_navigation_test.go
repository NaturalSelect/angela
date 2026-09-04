package list

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// newNItemList builds a list of n single-line tracked items, all
// finished, sized to the given viewport.
func newNItemList(n, width, height int) (*List, []*trackedItem) {
	tracked := make([]*trackedItem, n)
	items := make([]Item, n)
	for i := range n {
		it := newTrackedItem(strconv.Itoa(i), "x", true)
		tracked[i] = it
		items[i] = it
	}
	l := NewList(items...)
	l.SetSize(width, height)
	return l, tracked
}

func TestList_RegisterRenderCallback_RunsAllInOrder(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha", true)
	l := NewList(a)
	l.SetSize(40, 10)
	l.SetSelected(0)

	var calls []string
	l.RegisterRenderCallback(func(idx, selectedIdx int, item Item) Item {
		calls = append(calls, "first")
		require.Equal(t, 0, idx)
		require.Equal(t, 0, selectedIdx)
		return nil
	})
	l.RegisterRenderCallback(func(idx, selectedIdx int, item Item) Item {
		calls = append(calls, "second")
		return item
	})

	_ = l.Render()
	require.Equal(t, []string{"first", "second"}, calls)
}

func TestList_Gap(t *testing.T) {
	t.Parallel()

	l := NewList()
	require.Equal(t, 0, l.Gap())
	l.SetGap(3)
	require.Equal(t, 3, l.Gap())
}

func TestList_WidthHeight(t *testing.T) {
	t.Parallel()

	l := NewList()
	l.SetSize(12, 7)
	require.Equal(t, 12, l.Width())
	require.Equal(t, 7, l.Height())
}

func TestList_Len(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(2, 40, 10)
	require.Equal(t, 2, l.Len())
}

func TestList_Offset(t *testing.T) {
	t.Parallel()

	items := make([]Item, 4)
	for i := range items {
		items[i] = newMultiLineItem(strconv.Itoa(i), 2)
	}
	l := NewList(items...)
	l.SetSize(40, 3)

	require.Equal(t, 0, l.Offset())

	l.ScrollToIndex(2)
	require.Equal(t, 4, l.Offset(), "items 0 and 1 are each 2 lines tall")

	l.offsetLine = 1
	require.Equal(t, 5, l.Offset())
}

func TestList_ScrollToIndex(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(3, 10, 5)

	l.ScrollToIndex(1)
	require.Equal(t, 1, l.offsetIdx)
	require.Equal(t, 0, l.offsetLine)

	l.offsetLine = 4
	l.ScrollToIndex(-5)
	require.Equal(t, 0, l.offsetIdx, "a negative index clamps to zero")
	require.Equal(t, 0, l.offsetLine, "scrolling to an index resets the line offset")

	l.ScrollToIndex(50)
	require.Equal(t, 2, l.offsetIdx, "an index beyond the list clamps to the last item")
}

func TestList_FocusBlur(t *testing.T) {
	t.Parallel()

	l := NewList()
	require.False(t, l.Focused())
	l.Focus()
	require.True(t, l.Focused())
	l.Blur()
	require.False(t, l.Focused())
}

func TestList_AtTopAtBottom(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(5, 40, 3)

	require.True(t, l.AtTop())
	require.False(t, l.AtBottom(), "5 single-line items do not fit in a 3-line viewport")

	l.offsetIdx = 2
	require.False(t, l.AtTop())
	require.True(t, l.AtBottom(), "the last 3 items exactly fill the viewport")
}

func TestList_AtTopAtBottom_EmptyList(t *testing.T) {
	t.Parallel()

	l := NewList()
	require.True(t, l.AtTop())
	require.True(t, l.AtBottom())
}

func TestList_ScrollToBottom_EmptyListNoOp(t *testing.T) {
	t.Parallel()

	l := NewList()
	require.NotPanics(t, func() { l.ScrollToBottom() })
}

func TestList_ScrollBy(t *testing.T) {
	t.Parallel()

	t.Run("empty list is a no-op", func(t *testing.T) {
		t.Parallel()

		l := NewList()
		require.NotPanics(t, func() { l.ScrollBy(5) })
	})

	t.Run("zero lines is a no-op", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.ScrollBy(0)
		require.Equal(t, 0, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("scrolling down advances the offset by one item", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.ScrollBy(1)
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("scrolling down past the end clamps at the bottom", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.ScrollBy(1000)
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 1, l.offsetLine)
		require.True(t, l.AtBottom())
	})

	t.Run("already at the bottom is a no-op", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.ScrollToBottom()
		idx, line := l.offsetIdx, l.offsetLine
		l.ScrollBy(1)
		require.Equal(t, idx, l.offsetIdx)
		require.Equal(t, line, l.offsetLine)
	})

	t.Run("scrolling up retreats the offset", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.ScrollToBottom()
		l.ScrollBy(-1)
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("scrolling up past the start clamps at the top", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.ScrollToBottom()
		l.ScrollBy(-1000)
		require.Equal(t, 0, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("reverse mode inverts the scroll direction", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(5, 40, 3)
		l.SetReverse(true)
		l.ScrollBy(-1)
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})
}

func TestList_VisibleItemIndices(t *testing.T) {
	t.Parallel()

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		l := NewList()
		start, end := l.VisibleItemIndices()
		require.Equal(t, 0, start)
		require.Equal(t, 0, end)
	})

	t.Run("bounded by the viewport height", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(6, 40, 3)
		start, end := l.VisibleItemIndices()
		require.Equal(t, 0, start)
		require.Equal(t, 2, end)
	})
}

func TestList_ScrollToSelected(t *testing.T) {
	t.Parallel()

	t.Run("no selection is a no-op", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(6, 40, 3)
		l.ScrollToSelected()
		require.Equal(t, 0, l.offsetIdx)
	})

	t.Run("selection above the viewport scrolls up to it", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(6, 40, 3)
		l.ScrollToIndex(4)
		l.SetSelected(1)
		l.ScrollToSelected()
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("selection below the viewport scrolls down to it", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(6, 40, 3)
		l.SetSelected(5)
		l.ScrollToSelected()
		require.True(t, l.SelectedItemInView())
		require.Equal(t, 3, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})

	t.Run("selection already in view is a no-op", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(6, 40, 3)
		l.SetSelected(1)
		l.ScrollToSelected()
		require.Equal(t, 0, l.offsetIdx)
	})
}

func TestList_SelectedItemInView(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(6, 40, 3)

	require.False(t, l.SelectedItemInView(), "no selection is never in view")

	l.SetSelected(1)
	require.True(t, l.SelectedItemInView())

	l.SetSelected(5)
	require.False(t, l.SelectedItemInView())
}

func TestList_SetSelected(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(3, 40, 10)

	l.SetSelected(1)
	require.Equal(t, 1, l.Selected())
	require.False(t, l.IsSelectedFirst())
	require.False(t, l.IsSelectedLast())

	l.SetSelected(0)
	require.True(t, l.IsSelectedFirst())

	l.SetSelected(2)
	require.True(t, l.IsSelectedLast())

	l.SetSelected(99)
	require.Equal(t, -1, l.Selected(), "an out-of-range index clears the selection")

	l.SetSelected(-1)
	require.Equal(t, -1, l.Selected())
}

func TestList_SelectPrevNext(t *testing.T) {
	t.Parallel()

	t.Run("normal SelectPrev decreases the index until zero", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetSelected(1)
		require.True(t, l.SelectPrev())
		require.Equal(t, 0, l.Selected())
		require.False(t, l.SelectPrev(), "already at the top")
		require.Equal(t, 0, l.Selected())
	})

	t.Run("normal SelectNext increases the index until the last item", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetSelected(1)
		require.True(t, l.SelectNext())
		require.Equal(t, 2, l.Selected())
		require.False(t, l.SelectNext(), "already at the bottom")
		require.Equal(t, 2, l.Selected())
	})

	t.Run("reverse SelectPrev increases the index", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetReverse(true)
		l.SetSelected(1)
		require.True(t, l.SelectPrev())
		require.Equal(t, 2, l.Selected())
	})

	t.Run("reverse SelectNext decreases the index", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetReverse(true)
		l.SetSelected(1)
		require.True(t, l.SelectNext())
		require.Equal(t, 0, l.Selected())
	})
}

func TestList_SelectFirstLast(t *testing.T) {
	t.Parallel()

	t.Run("empty list returns false", func(t *testing.T) {
		t.Parallel()

		l := NewList()
		require.False(t, l.SelectFirst())
		require.False(t, l.SelectLast())
	})

	t.Run("selects boundary indices", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(4, 40, 10)

		require.True(t, l.SelectFirst())
		require.Equal(t, 0, l.Selected())

		require.True(t, l.SelectLast())
		require.Equal(t, 3, l.Selected())
	})
}

func TestList_WrapToStartEnd(t *testing.T) {
	t.Parallel()

	t.Run("empty list returns false", func(t *testing.T) {
		t.Parallel()

		l := NewList()
		require.False(t, l.WrapToStart())
		require.False(t, l.WrapToEnd())
	})

	t.Run("normal mode", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)

		require.True(t, l.WrapToStart())
		require.Equal(t, 0, l.Selected())
		require.True(t, l.WrapToEnd())
		require.Equal(t, 2, l.Selected())
	})

	t.Run("reverse mode swaps the ends", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetReverse(true)

		require.True(t, l.WrapToStart())
		require.Equal(t, 2, l.Selected())
		require.True(t, l.WrapToEnd())
		require.Equal(t, 0, l.Selected())
	})
}

func TestList_SelectedItem(t *testing.T) {
	t.Parallel()

	l, tracked := newNItemList(1, 40, 10)

	require.Nil(t, l.SelectedItem(), "no selection returns nil")

	l.SetSelected(0)
	require.Same(t, Item(tracked[0]), l.SelectedItem())
}

func TestList_SelectFirstLastInView(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(6, 40, 3)

	l.SelectFirstInView()
	require.Equal(t, 0, l.Selected())

	l.SelectLastInView()
	require.Equal(t, 2, l.Selected())

	l.ScrollToIndex(3)
	l.SelectFirstInView()
	require.Equal(t, 3, l.Selected())

	l.SelectLastInView()
	require.Equal(t, 5, l.Selected())
}

func TestList_ItemAt(t *testing.T) {
	t.Parallel()

	l, tracked := newNItemList(2, 40, 10)

	require.Same(t, Item(tracked[0]), l.ItemAt(0))
	require.Same(t, Item(tracked[1]), l.ItemAt(1))
	require.Nil(t, l.ItemAt(-1))
	require.Nil(t, l.ItemAt(2))
}

func TestList_ItemIndexAtPosition(t *testing.T) {
	t.Parallel()

	a := newMultiLineItem("a", 3)
	b := newMultiLineItem("b", 2)
	l := NewList(a, b)
	l.SetSize(40, 10)
	l.SetGap(2)

	tests := []struct {
		name      string
		y         int
		wantIdx   int
		wantItemY int
	}{
		{"negative y is out of bounds", -1, -1, -1},
		{"y beyond the viewport height", 10, -1, -1},
		{"first line of the first item", 0, 0, 0},
		{"last line of the first item", 2, 0, 2},
		{"inside the gap after the first item", 3, -1, -1},
		{"first line of the second item", 5, 1, 0},
		{"last line of the second item", 6, 1, 1},
		{"past all items", 9, -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			idx, y := l.ItemIndexAtPosition(0, tt.y)
			require.Equal(t, tt.wantIdx, idx)
			require.Equal(t, tt.wantItemY, y)
		})
	}
}

func TestList_Invalidate(t *testing.T) {
	t.Parallel()

	l, tracked := newNItemList(2, 40, 10)
	_ = l.Render()
	require.Equal(t, 1, tracked[0].renderHits)

	l.Invalidate(tracked[0])
	_ = l.Render()
	require.Equal(t, 2, tracked[0].renderHits, "an invalidated item must re-render")
	require.Equal(t, 1, tracked[1].renderHits, "untouched items stay cached")
}

func TestList_InvalidateFrozen(t *testing.T) {
	t.Parallel()

	l, tracked := newNItemList(1, 40, 10)
	_ = l.Render()
	require.Equal(t, 1, tracked[0].renderHits)

	l.InvalidateFrozen(tracked[0])
	_ = l.Render()
	require.Equal(t, 2, tracked[0].renderHits, "a frozen entry must be dropped and re-rendered")
}

func TestList_GetItem_OutOfBoundsReturnsZeroValue(t *testing.T) {
	t.Parallel()

	l, _ := newNItemList(2, 40, 10)
	require.Equal(t, renderedItem{}, l.getItem(-1))
	require.Equal(t, renderedItem{}, l.getItem(2))
}

func TestList_RemoveItem(t *testing.T) {
	t.Parallel()

	t.Run("out-of-bounds index is a no-op", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.RemoveItem(-1)
		l.RemoveItem(3)
		require.Equal(t, 3, l.Len())
	})

	t.Run("removing the selected item clears the selection", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetSelected(1)
		l.RemoveItem(1)
		require.Equal(t, -1, l.Selected())
	})

	t.Run("removing an item before the selection shifts it down", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.SetSelected(2)
		l.RemoveItem(0)
		require.Equal(t, 1, l.Selected())
	})

	t.Run("removing an item before the offset shifts it down", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.ScrollToIndex(2)
		l.RemoveItem(0)
		require.Equal(t, 1, l.offsetIdx)
	})

	t.Run("removing the last visible item clamps the offset", func(t *testing.T) {
		t.Parallel()

		l, _ := newNItemList(3, 40, 10)
		l.ScrollToIndex(2)
		l.RemoveItem(2)
		require.Equal(t, 1, l.offsetIdx)
		require.Equal(t, 0, l.offsetLine)
	})
}

func TestList_Offset_WithGap(t *testing.T) {
	t.Parallel()

	items := make([]Item, 3)
	for i := range items {
		items[i] = newMultiLineItem(strconv.Itoa(i), 2)
	}
	l := NewList(items...)
	l.SetSize(40, 10)
	l.SetGap(1)

	l.ScrollToIndex(2)
	require.Equal(t, 6, l.Offset(), "2 items of height 2 plus a 1-line gap after each")
}
