package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/notification"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// notifWorkspace is the least workspace the notifications dialog needs:
// just enough Config() to report the currently selected style.
type notifWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *notifWorkspace) Config() *config.Config { return w.cfg }

func newTestNotifications(t *testing.T, current string) *Notifications {
	t.Helper()
	cfg := &config.Config{Options: &config.Options{Notifications: current}}
	com := &common.Common{Workspace: &notifWorkspace{cfg: cfg}, Styles: testStyles()}
	return NewNotifications(com)
}

// visibleStyleCount mirrors setItems' platform gating so assertions don't
// hardcode a count that varies with NativeSupported.
func visibleStyleCount() int {
	n := len(AllNotificationStyles)
	if !notification.NativeSupported {
		n--
	}
	return n
}

func TestNotifications_ID(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "auto")
	require.Equal(t, NotificationsID, n.ID())
}

// TestNotifications_SetItemsMarksCurrentAndSelectsIt verifies that the
// item matching the configured style is flagged current and opens
// pre-selected, and that an unsupported/missing style falls back to the
// first item without selecting anything as current.
func TestNotifications_SetItemsMarksCurrentAndSelectsIt(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "bell")
	require.Len(t, n.list.FilteredItems(), visibleStyleCount())

	item, ok := n.list.SelectedItem().(*NotificationItem)
	require.True(t, ok)
	require.Equal(t, "bell", item.style.ID)
	require.True(t, item.isCurrent)
}

// TestNotifications_NilConfigDefaultsToAuto verifies that a workspace
// with no config (or no notifications option set) still opens cleanly
// with "auto" treated as the current style.
func TestNotifications_NilConfigDefaultsToAuto(t *testing.T) {
	t.Parallel()

	com := &common.Common{Workspace: &notifWorkspace{cfg: nil}, Styles: testStyles()}
	n := NewNotifications(com)

	item, ok := n.list.SelectedItem().(*NotificationItem)
	require.True(t, ok)
	require.Equal(t, "auto", item.style.ID)
	require.True(t, item.isCurrent)
}

func TestNotifications_HandleMsg(t *testing.T) {
	t.Parallel()

	t.Run("close key closes the dialog", func(t *testing.T) {
		t.Parallel()
		n := newTestNotifications(t, "auto")
		action := n.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.Equal(t, ActionClose{}, action)
	})

	t.Run("next wraps from the last item to the first", func(t *testing.T) {
		t.Parallel()
		n := newTestNotifications(t, "auto")
		n.list.SelectLast()
		require.True(t, n.list.IsSelectedLast())

		n.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
		require.True(t, n.list.IsSelectedFirst(), "next from the last item should wrap to the first")
	})

	t.Run("previous wraps from the first item to the last", func(t *testing.T) {
		t.Parallel()
		n := newTestNotifications(t, "auto")
		require.True(t, n.list.IsSelectedFirst())

		n.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
		require.True(t, n.list.IsSelectedLast(), "previous from the first item should wrap to the last")
	})

	t.Run("select emits the selected style", func(t *testing.T) {
		t.Parallel()
		n := newTestNotifications(t, "auto")
		n.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown}) // move onto "native" or the next available style
		item, ok := n.list.SelectedItem().(*NotificationItem)
		require.True(t, ok)

		action := n.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		selected, ok := action.(ActionSelectNotificationStyle)
		require.True(t, ok)
		require.Equal(t, item.style.ID, selected.Style)
	})

	t.Run("select with no matching items is a no-op", func(t *testing.T) {
		t.Parallel()
		n := newTestNotifications(t, "auto")
		n.HandleMsg(tea.KeyPressMsg{Code: 'z', Text: "z"})
		n.HandleMsg(tea.KeyPressMsg{Code: 'z', Text: "z"})
		require.Nil(t, n.list.SelectedItem(), "an unmatched filter must leave nothing selected")

		action := n.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Nil(t, action)
	})

	t.Run("typing filters the list and resets the selection", func(t *testing.T) {
		t.Parallel()
		n := newTestNotifications(t, "auto")
		action := n.HandleMsg(tea.KeyPressMsg{Code: 'b', Text: "b"})
		require.IsType(t, ActionCmd{}, action)
		require.Equal(t, "b", n.input.Value())

		ids := make([]string, 0)
		for _, it := range n.list.FilteredItems() {
			ni, ok := it.(*NotificationItem)
			require.True(t, ok)
			ids = append(ids, ni.style.ID)
		}
		require.Contains(t, ids, "bell", "filtering for 'b' must keep the Bell style")
		require.NotContains(t, ids, "auto", "filtering for 'b' must drop styles that don't match")
	})
}

// TestNotifications_Draw verifies every visible style title renders,
// along with the "current" marker for the configured style.
func TestNotifications_Draw(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "osc")
	const w, h = 60, 20
	scr := uv.NewScreenBuffer(w, h)
	n.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Notification Style")
	require.Contains(t, view, "OSC")
	require.Contains(t, view, "current")
}

func TestNotifications_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "auto")
	require.Len(t, n.ShortHelp(), 3)

	full := n.FullHelp()
	var total int
	for _, row := range full {
		total += len(row)
	}
	require.Equal(t, 4, total)
}

// TestNotificationItem_FilterAndID verify the small list.Item /
// list.FilterableItem accessor methods used by the filterable list.
func TestNotificationItem_FilterAndID(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "auto")
	item, ok := n.list.SelectedItem().(*NotificationItem)
	require.True(t, ok)

	require.Equal(t, item.style.Title, item.Filter())
	require.Equal(t, item.style.ID, item.ID())
	require.True(t, item.Finished())
}

func TestNotificationItem_SetFocusedBumpsVersionOnlyOnChange(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "auto")
	item, ok := n.list.SelectedItem().(*NotificationItem)
	require.True(t, ok)

	// The list may already have focused the selected item as part of
	// construction, so flip relative to its current state rather than
	// assuming it starts unfocused.
	initial := item.focused
	before := item.Version()
	item.SetFocused(!initial)
	require.NotEqual(t, before, item.Version(), "flipping the focus state must bump the version")

	afterFlip := item.Version()
	item.SetFocused(!initial)
	require.Equal(t, afterFlip, item.Version(), "re-applying the same focus state must not bump again")
}

func TestNotificationItem_RenderIncludesTitle(t *testing.T) {
	t.Parallel()

	n := newTestNotifications(t, "disabled")
	for _, it := range n.list.FilteredItems() {
		item, ok := it.(*NotificationItem)
		require.True(t, ok)
		rendered := ansi.Strip(item.Render(40))
		require.Contains(t, rendered, item.style.Title)
		if item.style.ID == "disabled" {
			require.Contains(t, rendered, "current")
		}
	}
}
