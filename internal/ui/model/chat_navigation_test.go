package model

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/stretchr/testify/require"
)

// testToolMessageItem is a minimal ToolMessageItem used to exercise
// LastPendingTool and nested-tool ID registration without pulling in real
// tool rendering.
type testToolMessageItem struct {
	testMessageItem
	tc     message.ToolCall
	status chat.ToolStatus
}

func (m *testToolMessageItem) ToolCall() message.ToolCall        { return m.tc }
func (m *testToolMessageItem) SetToolCall(tc message.ToolCall)   { m.tc = tc }
func (m *testToolMessageItem) SetResult(res *message.ToolResult) {}
func (m *testToolMessageItem) MessageID() string                 { return m.tc.ID }
func (m *testToolMessageItem) SetMessageID(id string)            { m.tc.ID = id }
func (m *testToolMessageItem) SetStatus(status chat.ToolStatus)  { m.status = status }
func (m *testToolMessageItem) Status() chat.ToolStatus           { return m.status }

var _ chat.ToolMessageItem = (*testToolMessageItem)(nil)

// testContainerItem is a minimal chat item implementing
// chat.NestedToolContainer, used to exercise UpdateNestedToolIDs without
// pulling in the real AgentToolMessageItem.
type testContainerItem struct {
	testMessageItem
	nested []chat.ToolMessageItem
}

func (m *testContainerItem) NestedTools() []chat.ToolMessageItem         { return m.nested }
func (m *testContainerItem) SetNestedTools(tools []chat.ToolMessageItem) { m.nested = tools }
func (m *testContainerItem) AddNestedTool(tool chat.ToolMessageItem) {
	m.nested = append(m.nested, tool)
}
func (m *testContainerItem) SetTiming(startedAt, endedAt int64) {}
func (m *testContainerItem) MarkActivity(ts int64)              {}

var _ chat.NestedToolContainer = (*testContainerItem)(nil)

// testAnimatableItem is a minimal chat item implementing chat.Animatable,
// used to exercise Animate/RestartPausedVisibleAnimations without pulling
// in real spinner machinery. Both methods return a non-nil sentinel
// command so tests can observe whether they actually fired.
type testAnimatableItem struct {
	testMessageItem
	animateCalls        int
	startAnimationCalls int
}

func (m *testAnimatableItem) Animate(msg anim.StepMsg) tea.Cmd {
	m.animateCalls++
	return func() tea.Msg { return nil }
}

func (m *testAnimatableItem) StartAnimation() tea.Cmd {
	m.startAnimationCalls++
	return func() tea.Msg { return nil }
}

var _ chat.Animatable = (*testAnimatableItem)(nil)

func TestChatSetSelected(t *testing.T) {
	t.Parallel()

	t.Run("lands directly on a selectable item", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}},
			&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
		)
		u.chat.SetSelected(1)
		require.Equal(t, 1, u.chat.list.Selected())
	})

	t.Run("walks forward past a non-selectable item", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			testMessageItem{id: "a", text: "a"},
			&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
			testMessageItem{id: "c", text: "c"},
		)
		u.chat.SetSelected(0)
		require.Equal(t, 1, u.chat.list.Selected())
	})

	t.Run("falls back to walking backward when forward search hits the end", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}},
			testMessageItem{id: "b", text: "b"},
			testMessageItem{id: "c", text: "c"},
		)
		u.chat.SetSelected(1)
		require.Equal(t, 0, u.chat.list.Selected())
	})

	t.Run("stays at the start when nothing in the list is selectable", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			testMessageItem{id: "a", text: "a"},
			testMessageItem{id: "b", text: "b"},
			testMessageItem{id: "c", text: "c"},
		)
		u.chat.SetSelected(1)
		require.Equal(t, 0, u.chat.list.Selected())
	})

	t.Run("negative index clears the selection", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}})
		u.chat.SetSelected(-1)
		require.Equal(t, -1, u.chat.list.Selected())
	})

	t.Run("empty list does not panic", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.NotPanics(t, func() { u.chat.SetSelected(0) })
	})
}

func TestChatSelectNext(t *testing.T) {
	t.Parallel()

	t.Run("skips a non-selectable item in between", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}},
			testMessageItem{id: "b", text: "b"},
			&testHighlightableItem{testMessageItem: testMessageItem{id: "c", text: "c"}},
		)
		u.chat.list.SetSelected(0)

		u.chat.SelectNext()
		require.Equal(t, 2, u.chat.list.Selected())
	})

	t.Run("stops moving once the list end is reached, even mid-search", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}},
			testMessageItem{id: "b", text: "b"},
		)
		u.chat.list.SetSelected(0)

		u.chat.SelectNext()
		require.Equal(t, 1, u.chat.list.Selected(), "search cannot move past the last item")
	})
}

func TestChatSelectPrev(t *testing.T) {
	t.Parallel()

	t.Run("skips a non-selectable item in between", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}},
			testMessageItem{id: "b", text: "b"},
			&testHighlightableItem{testMessageItem: testMessageItem{id: "c", text: "c"}},
		)
		u.chat.list.SetSelected(2)

		u.chat.SelectPrev()
		require.Equal(t, 0, u.chat.list.Selected())
	})

	t.Run("stops moving once the list start is reached, even mid-search", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			testMessageItem{id: "a", text: "a"},
			&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
		)
		u.chat.list.SetSelected(1)

		u.chat.SelectPrev()
		require.Equal(t, 0, u.chat.list.Selected(), "search cannot move past the first item")
	})
}

func TestChatSelectFirst(t *testing.T) {
	t.Parallel()

	t.Run("lands directly on a selectable first item", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}})
		u.chat.SelectFirst()
		require.Equal(t, 0, u.chat.list.Selected())
	})

	t.Run("skips leading non-selectable items", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			testMessageItem{id: "a", text: "a"},
			testMessageItem{id: "b", text: "b"},
			&testHighlightableItem{testMessageItem: testMessageItem{id: "c", text: "c"}},
		)
		u.chat.SelectFirst()
		require.Equal(t, 2, u.chat.list.Selected())
	})

	t.Run("empty list does not panic", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.NotPanics(t, func() { u.chat.SelectFirst() })
	})
}

func TestChatSelectLast(t *testing.T) {
	t.Parallel()

	t.Run("lands directly on a selectable last item", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}})
		u.chat.SelectLast()
		require.Equal(t, 0, u.chat.list.Selected())
	})

	t.Run("skips trailing non-selectable items", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testHighlightableItem{testMessageItem: testMessageItem{id: "a", text: "a"}},
			testMessageItem{id: "b", text: "b"},
			testMessageItem{id: "c", text: "c"},
		)
		u.chat.SelectLast()
		require.Equal(t, 0, u.chat.list.Selected())
	})

	t.Run("empty list does not panic", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.NotPanics(t, func() { u.chat.SelectLast() })
	})
}

func TestChatSelectFirstInView(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "a", text: "a"},
		&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
		testMessageItem{id: "c", text: "c"},
		&testHighlightableItem{testMessageItem: testMessageItem{id: "d", text: "d"}},
	)
	u.updateLayoutAndSize()

	u.chat.SelectFirstInView()
	require.Equal(t, 1, u.chat.list.Selected())
}

func TestChatSelectLastInView(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "a", text: "a"},
		&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
		testMessageItem{id: "c", text: "c"},
		&testHighlightableItem{testMessageItem: testMessageItem{id: "d", text: "d"}},
	)
	u.updateLayoutAndSize()

	u.chat.SelectLastInView()
	require.Equal(t, 3, u.chat.list.Selected())
}

func TestChatScrollToSelected(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	msgs := make([]chat.MessageItem, 0, 60)
	for i := range 60 {
		msgs = append(msgs, testMessageItem{id: "m-" + strconv.Itoa(i), text: "message " + strconv.Itoa(i)})
	}
	u.chat.SetMessages(msgs...)
	u.updateLayoutAndSize()

	u.chat.list.SetSelected(0)
	cmd := u.chat.ScrollToSelected()
	require.False(t, u.chat.follow, "selecting the first item is not at the bottom")
	require.NotNil(t, cmd, "default scrollbar mode schedules a hide timer")
	require.True(t, u.chat.scrollbarVisible)

	u.chat.list.SetSelected(59)
	u.chat.ScrollToSelected()
	require.True(t, u.chat.follow, "selecting the last item lands at the bottom")
}

func TestChatScrollToIndex(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	msgs := make([]chat.MessageItem, 0, 60)
	for i := range 60 {
		msgs = append(msgs, testMessageItem{id: "m-" + strconv.Itoa(i), text: "message " + strconv.Itoa(i)})
	}
	u.chat.SetMessages(msgs...)
	u.updateLayoutAndSize()

	u.chat.ScrollToIndex(0)
	require.False(t, u.chat.follow)

	u.chat.ScrollToIndex(59)
	require.True(t, u.chat.follow)
}

func TestChatIsSelectable(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "a", text: "a"},
		&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
	)

	require.False(t, u.chat.isSelectable(0))
	require.True(t, u.chat.isSelectable(1))
	require.False(t, u.chat.isSelectable(-1))
	require.False(t, u.chat.isSelectable(99))
}

func TestChatClearMessages(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "a", text: "a"},
		&testHighlightableItem{testMessageItem: testMessageItem{id: "b", text: "b"}},
	)
	u.updateLayoutAndSize()
	u.chat.pausedAnimations["a"] = struct{}{}
	_, _ = u.chat.HandleMouseDown(0, 0)

	u.chat.ClearMessages()

	require.Equal(t, 0, u.chat.Len())
	require.Nil(t, u.chat.MessageItem("a"))
	require.Empty(t, u.chat.pausedAnimations)
	require.False(t, u.chat.scrollbarVisible)
	require.False(t, u.chat.mouseDown)
	require.Equal(t, -1, u.chat.mouseDownItem)
}

func TestChatRemoveMessage(t *testing.T) {
	t.Parallel()

	t.Run("removes the item and reindexes trailing entries", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			testMessageItem{id: "a", text: "a"},
			testMessageItem{id: "b", text: "b"},
			testMessageItem{id: "c", text: "c"},
		)
		u.chat.pausedAnimations["b"] = struct{}{}

		u.chat.RemoveMessage("b")

		require.Equal(t, 2, u.chat.Len())
		require.Nil(t, u.chat.MessageItem("b"))
		require.Equal(t, 0, u.chat.idInxMap["a"])
		require.Equal(t, 1, u.chat.idInxMap["c"])
		require.NotContains(t, u.chat.pausedAnimations, "b")
	})

	t.Run("unknown ID is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(testMessageItem{id: "a", text: "a"})

		u.chat.RemoveMessage("does-not-exist")

		require.Equal(t, 1, u.chat.Len())
	})
}

func TestChatMessageItem(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(testMessageItem{id: "a", text: "alpha"})

	got := u.chat.MessageItem("a")
	require.NotNil(t, got)
	require.Equal(t, "a", got.ID())

	require.Nil(t, u.chat.MessageItem("missing"))
}

func TestChatLen(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	require.Equal(t, 0, u.chat.Len())
	u.chat.SetMessages(testMessageItem{id: "a", text: "a"}, testMessageItem{id: "b", text: "b"})
	require.Equal(t, 2, u.chat.Len())
}

func TestChatLastPendingTool(t *testing.T) {
	t.Parallel()

	t.Run("returns the most recent running or awaiting-permission tool from the end", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testToolMessageItem{testMessageItem: testMessageItem{id: "t1", text: "t1"}, tc: message.ToolCall{ID: "t1", Name: "bash"}, status: chat.ToolStatusSuccess},
			&testToolMessageItem{testMessageItem: testMessageItem{id: "t2", text: "t2"}, tc: message.ToolCall{ID: "t2", Name: "grep"}, status: chat.ToolStatusRunning},
			testMessageItem{id: "m1", text: "not a tool"},
			&testToolMessageItem{testMessageItem: testMessageItem{id: "t3", text: "t3"}, tc: message.ToolCall{ID: "t3", Name: "edit"}, status: chat.ToolStatusAwaitingPermission},
			&testToolMessageItem{testMessageItem: testMessageItem{id: "t4", text: "t4"}, tc: message.ToolCall{ID: "t4", Name: "view"}, status: chat.ToolStatusSuccess},
		)

		tc, ok := u.chat.LastPendingTool()
		require.True(t, ok)
		require.Equal(t, "t3", tc.ID, "must scan from the end and skip the trailing finished tool")
	})

	t.Run("no pending tool returns the zero value", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(
			&testToolMessageItem{testMessageItem: testMessageItem{id: "t1", text: "t1"}, tc: message.ToolCall{ID: "t1"}, status: chat.ToolStatusSuccess},
			&testToolMessageItem{testMessageItem: testMessageItem{id: "t2", text: "t2"}, tc: message.ToolCall{ID: "t2"}, status: chat.ToolStatusError},
		)

		tc, ok := u.chat.LastPendingTool()
		require.False(t, ok)
		require.Equal(t, message.ToolCall{}, tc)
	})
}

func TestChatUpdateNestedToolIDs(t *testing.T) {
	t.Parallel()

	t.Run("registers nested tool IDs to the container's index", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		container := &testContainerItem{testMessageItem: testMessageItem{id: "c1", text: "c1"}}
		u.chat.SetMessages(container)

		nested := &testToolMessageItem{testMessageItem: testMessageItem{id: "n1", text: "n1"}, tc: message.ToolCall{ID: "n1"}}
		container.AddNestedTool(nested)
		u.chat.UpdateNestedToolIDs("c1")

		require.Equal(t, 0, u.chat.idInxMap["n1"])
	})

	t.Run("unknown container ID is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.NotPanics(t, func() { u.chat.UpdateNestedToolIDs("missing") })
	})

	t.Run("ID present but item is not a container is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(testMessageItem{id: "plain", text: "plain"})
		require.NotPanics(t, func() { u.chat.UpdateNestedToolIDs("plain") })
	})
}

func TestChatInvalidateRenderCaches(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	msg := &message.Message{
		ID:   "m-assist",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	}
	item := chat.NewAssistantMessageItem(u.com.Styles, msg)
	u.chat.SetMessages(item, testMessageItem{id: "plain", text: "plain"})

	before := item.Version()
	require.NotPanics(t, u.chat.InvalidateRenderCaches)
	require.Greater(t, item.Version(), before, "cache invalidation must bump the item's version")
}

func TestChatAnimate(t *testing.T) {
	t.Parallel()

	t.Run("unknown ID is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		cmd := u.chat.Animate(anim.StepMsg{ID: "missing"})
		require.Nil(t, cmd)
	})

	t.Run("known ID that is not animatable is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(testMessageItem{id: "a", text: "a"})
		cmd := u.chat.Animate(anim.StepMsg{ID: "a"})
		require.Nil(t, cmd)
	})

	t.Run("visible item animates and clears its paused flag", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		item := &testAnimatableItem{testMessageItem: testMessageItem{id: "a", text: "a"}}
		u.chat.SetMessages(item)
		u.updateLayoutAndSize()
		u.chat.pausedAnimations["a"] = struct{}{}

		cmd := u.chat.Animate(anim.StepMsg{ID: "a"})

		require.NotNil(t, cmd)
		require.Equal(t, 1, item.animateCalls)
		require.NotContains(t, u.chat.pausedAnimations, "a")
	})

	t.Run("off-screen item is paused instead of animated", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		off := &testAnimatableItem{testMessageItem: testMessageItem{id: "off", text: "off"}}
		items := []chat.MessageItem{off}
		for i := range 20 {
			items = append(items, testMessageItem{id: "f" + strconv.Itoa(i), text: "filler"})
		}
		u.chat.SetMessages(items...)
		u.chat.SetSize(80, 5) // Small viewport; the earlier SetMessages left follow=true so this re-anchors to the bottom.

		cmd := u.chat.Animate(anim.StepMsg{ID: "off"})

		require.Nil(t, cmd)
		require.Equal(t, 0, off.animateCalls)
		require.Contains(t, u.chat.pausedAnimations, "off")
	})
}

func TestChatRestartPausedVisibleAnimations(t *testing.T) {
	t.Parallel()

	t.Run("no paused animations returns nil", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		require.Nil(t, u.chat.RestartPausedVisibleAnimations())
	})

	t.Run("restarts visible paused items, leaves off-screen ones paused, and drops stale IDs", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		visible := &testAnimatableItem{testMessageItem: testMessageItem{id: "visible", text: "v"}}
		offscreen := &testAnimatableItem{testMessageItem: testMessageItem{id: "offscreen", text: "o"}}
		items := []chat.MessageItem{offscreen}
		for i := range 20 {
			items = append(items, testMessageItem{id: "f" + strconv.Itoa(i), text: "filler"})
		}
		items = append(items, visible)
		u.chat.SetMessages(items...)
		u.chat.SetSize(80, 5)

		u.chat.pausedAnimations["visible"] = struct{}{}
		u.chat.pausedAnimations["offscreen"] = struct{}{}
		u.chat.pausedAnimations["gone"] = struct{}{} // Stale: no longer in idInxMap.

		cmd := u.chat.RestartPausedVisibleAnimations()

		require.NotNil(t, cmd)
		require.Equal(t, 1, visible.startAnimationCalls)
		require.Equal(t, 0, offscreen.startAnimationCalls)
		require.NotContains(t, u.chat.pausedAnimations, "visible")
		require.Contains(t, u.chat.pausedAnimations, "offscreen")
		require.NotContains(t, u.chat.pausedAnimations, "gone")
	})
}

func TestChatWarmCmd(t *testing.T) {
	t.Parallel()

	t.Run("zero delay fires immediately", func(t *testing.T) {
		t.Parallel()
		cmd := chatWarmCmd(5, 0)
		require.NotNil(t, cmd)
		require.Equal(t, chatWarmMsg{seq: 5}, cmd())
	})

	t.Run("positive delay schedules a tick", func(t *testing.T) {
		t.Parallel()
		require.NotNil(t, chatWarmCmd(5, time.Millisecond))
	})
}

func TestChatBeginResize(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(testMessageItem{id: "a", text: "a"})
	u.updateLayoutAndSize()

	cmd := u.chat.BeginResize()

	require.True(t, u.chat.resizing)
	require.Equal(t, 1, u.chat.resizeSettleSeq)
	require.Equal(t, 0, u.chat.warmNext)
	require.NotNil(t, cmd)
}

func TestChatWarmStep(t *testing.T) {
	t.Parallel()

	t.Run("stale sequence is a no-op", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		u.chat.SetMessages(testMessageItem{id: "a", text: "a"})
		u.updateLayoutAndSize()
		u.chat.BeginResize()

		cmd, done := u.chat.WarmStep(u.chat.resizeSettleSeq + 1)
		require.Nil(t, cmd)
		require.False(t, done)
		require.True(t, u.chat.resizing, "a stale step must not touch resizing state")
	})

	t.Run("small list finishes warming in a single step", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		msgs := make([]chat.MessageItem, 0, 5)
		for i := range 5 {
			msgs = append(msgs, testMessageItem{id: "m-" + strconv.Itoa(i), text: "m"})
		}
		u.chat.SetMessages(msgs...)
		u.updateLayoutAndSize()
		u.chat.BeginResize()

		cmd, done := u.chat.WarmStep(u.chat.resizeSettleSeq)

		require.Nil(t, cmd)
		require.True(t, done)
		require.False(t, u.chat.resizing)
	})

	t.Run("large list needs multiple steps and clears resizing only once fully warmed", func(t *testing.T) {
		t.Parallel()
		u := newTestUI()
		total := warmBatchSize*2 + 5
		msgs := make([]chat.MessageItem, 0, total)
		for i := range total {
			msgs = append(msgs, testMessageItem{id: "m-" + strconv.Itoa(i), text: "m"})
		}
		u.chat.SetMessages(msgs...)
		u.updateLayoutAndSize()
		u.chat.BeginResize()

		seq := u.chat.resizeSettleSeq
		steps := 0
		var done bool
		for !done {
			var cmd tea.Cmd
			cmd, done = u.chat.WarmStep(seq)
			steps++
			require.LessOrEqual(t, steps, 10, "must converge within a small number of batches")
			if !done {
				require.NotNil(t, cmd)
				require.True(t, u.chat.resizing)
			}
		}

		require.Greater(t, steps, 1, "a list bigger than one batch must take more than one step")
		require.False(t, u.chat.resizing)
		require.Equal(t, total, u.chat.warmNext)
	})
}
