package list

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// focusTrackingItem is a minimal Focusable test double.
type focusTrackingItem struct {
	*Versioned
	focused bool
}

func (f *focusTrackingItem) Render(int) string { return "x" }
func (f *focusTrackingItem) Finished() bool    { return true }
func (f *focusTrackingItem) SetFocused(v bool) { f.focused = v }

func TestFocusedRenderCallback_MarksOnlySelectedItemWhenFocused(t *testing.T) {
	t.Parallel()

	a := &focusTrackingItem{Versioned: NewVersioned()}
	b := &focusTrackingItem{Versioned: NewVersioned()}
	l := NewList(a, b)
	l.SetSelected(1)
	l.Focus()

	cb := FocusedRenderCallback(l)

	got := cb(0, l.Selected(), a)
	require.False(t, got.(*focusTrackingItem).focused, "unselected item must not be focused")

	got = cb(1, l.Selected(), b)
	require.True(t, got.(*focusTrackingItem).focused, "selected item in a focused list must be focused")
}

func TestFocusedRenderCallback_BlurredListNeverFocusesItems(t *testing.T) {
	t.Parallel()

	a := &focusTrackingItem{Versioned: NewVersioned()}
	l := NewList(a)
	l.SetSelected(0)
	l.Blur()

	cb := FocusedRenderCallback(l)
	got := cb(0, l.Selected(), a)
	require.False(t, got.(*focusTrackingItem).focused, "a blurred list must not mark the selected item focused")
}

func TestFocusedRenderCallback_NonFocusableItemPassesThrough(t *testing.T) {
	t.Parallel()

	item := newTrackedItem("a", "alpha", true) // does not implement Focusable
	l := NewList(item)
	l.SetSelected(0)
	l.Focus()

	cb := FocusedRenderCallback(l)
	got := cb(0, l.Selected(), item)
	require.Same(t, item, got, "non-focusable items must pass through unchanged")
}
