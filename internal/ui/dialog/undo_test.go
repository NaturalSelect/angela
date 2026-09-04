package dialog

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/undo"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestUndo(t *testing.T, preview undo.Preview) *Undo {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewUndo(com, "session-1", preview)
}

func smallUndoPreview() undo.Preview {
	return undo.Preview{
		CutMessageID: "msg-42",
		MessageCount: 3,
		Revert:       []string{"main.go"},
		Delete:       []string{"tmp.txt"},
	}
}

func TestUndo_ID(t *testing.T) {
	t.Parallel()

	u := newTestUndo(t, smallUndoPreview())
	require.Equal(t, UndoID, u.ID())
}

func TestUndo_HandleMsg(t *testing.T) {
	t.Parallel()

	t.Run("close key closes the dialog", func(t *testing.T) {
		t.Parallel()
		u := newTestUndo(t, smallUndoPreview())
		action := u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
		require.Equal(t, ActionClose{}, action)
	})

	t.Run("enter on default selection cancels", func(t *testing.T) {
		t.Parallel()
		u := newTestUndo(t, smallUndoPreview())
		require.True(t, u.selectedNo, "undo defaults to the safe cancel option")
		action := u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, ActionClose{}, action)
	})

	t.Run("toggling then confirming emits ActionUndoConfirmed pinned to the preview", func(t *testing.T) {
		t.Parallel()
		preview := smallUndoPreview()
		u := newTestUndo(t, preview)
		u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyLeft})
		require.False(t, u.selectedNo)

		action := u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		confirmed, ok := action.(ActionUndoConfirmed)
		require.True(t, ok)
		require.Equal(t, "session-1", confirmed.SessionID)
		require.Equal(t, preview.CutMessageID, confirmed.CutMessageID)
	})

	t.Run("tab also toggles the selection", func(t *testing.T) {
		t.Parallel()
		u := newTestUndo(t, smallUndoPreview())
		u.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
		require.False(t, u.selectedNo)
	})

	t.Run("unrelated message returns nil", func(t *testing.T) {
		t.Parallel()
		u := newTestUndo(t, smallUndoPreview())
		require.Nil(t, u.HandleMsg(tea.WindowSizeMsg{Width: 10, Height: 10}))
	})

	// This documents a known bug (also pinned for the quit dialog in
	// quit_test.go): the EnterSpace binding lists the literal " "
	// character, but a real space-bar keypress always stringifies to
	// "space" (see charmbracelet/ultraviolet's Key.Keystroke
	// special-casing of KeySpace), so it never matches and the key
	// silently does nothing.
	t.Run("space key is dead despite help text advertising it", func(t *testing.T) {
		t.Parallel()
		u := newTestUndo(t, smallUndoPreview())
		action := u.HandleMsg(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		require.Nil(t, action, "documents the current (buggy) behavior: space matches no binding")
	})
}

// TestUndo_Draw verifies the preview sections, buttons, and hint all
// render for a preview that touches revert, delete, and skipped files.
func TestUndo_Draw(t *testing.T) {
	t.Parallel()

	preview := undo.Preview{
		CutMessageID: "msg-1",
		MessageCount: 5,
		Revert:       []string{"a.go", "b.go"},
		Delete:       []string{"c.go"},
		Skipped:      []undo.SkippedFile{{Path: "d.go", Reason: "modified since"}},
	}
	u := newTestUndo(t, preview)

	const w, h = 100, 40
	scr := uv.NewScreenBuffer(w, h)
	cur := u.Draw(scr, image.Rect(0, 0, w, h))
	require.Nil(t, cur)

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Undo the last turn?")
	require.Contains(t, view, "Revert (2):")
	require.Contains(t, view, "Delete (1):")
	require.Contains(t, view, "Skipped (1):")
	require.Contains(t, view, "a.go")
	require.Contains(t, view, "d.go")
	require.Contains(t, view, "modified since")
	require.Contains(t, view, "Undo")
	require.Contains(t, view, "Cancel")
	require.Contains(t, view, "5 message(s)")
}

// TestUndo_DrawNarrowNeverPanics pins that a dialog too small for the
// content still renders instead of panicking.
func TestUndo_DrawNarrowNeverPanics(t *testing.T) {
	t.Parallel()

	preview := smallUndoPreview()
	u := newTestUndo(t, preview)
	scr := uv.NewScreenBuffer(15, 10)
	require.NotPanics(t, func() {
		u.Draw(scr, image.Rect(0, 0, 15, 10))
	})
}

func TestUndo_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	u := newTestUndo(t, smallUndoPreview())
	require.Len(t, u.ShortHelp(), 2)

	full := u.FullHelp()
	require.Len(t, full, 2)
	require.Len(t, full[0], 2)
	require.Len(t, full[1], 2)
}

// TestFormatUndoPaths verifies the bulleted list format, the truncation
// of an overlong path, and the "+N more" suffix once the cap is exceeded.
func TestFormatUndoPaths(t *testing.T) {
	t.Parallel()

	t.Run("short list has one bullet per path", func(t *testing.T) {
		t.Parallel()
		got := formatUndoPaths([]string{"a.go", "b.go"})
		require.Equal(t, "  a.go\n  b.go", got)
	})

	t.Run("long path is truncated with an ellipsis", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", maxUndoPathWidth+20) + ".go"
		got := formatUndoPaths([]string{long})
		require.True(t, ansi.StringWidth(strings.TrimPrefix(got, "  ")) <= maxUndoPathWidth)
		require.Contains(t, got, "…")
	})

	t.Run("exceeding the cap collapses the rest into a count", func(t *testing.T) {
		t.Parallel()
		paths := make([]string, maxUndoListItems+3)
		for i := range paths {
			paths[i] = strings.Repeat("f", i+1) + ".go"
		}
		got := formatUndoPaths(paths)
		require.Contains(t, got, "… +3 more")
		require.Equal(t, maxUndoListItems, strings.Count(got, ".go"), "only the capped number of paths should be listed")
	})
}

// TestFormatUndoSkipped verifies the reason is included alongside the
// path, and the same capping behavior as formatUndoPaths applies.
func TestFormatUndoSkipped(t *testing.T) {
	t.Parallel()

	t.Run("includes the reason next to the path", func(t *testing.T) {
		t.Parallel()
		got := formatUndoSkipped([]undo.SkippedFile{{Path: "a.go", Reason: "conflict"}})
		require.Equal(t, "  a.go (conflict)", got)
	})

	t.Run("exceeding the cap collapses the rest into a count", func(t *testing.T) {
		t.Parallel()
		files := make([]undo.SkippedFile, maxUndoListItems+2)
		for i := range files {
			files[i] = undo.SkippedFile{Path: "f.go", Reason: "r"}
		}
		got := formatUndoSkipped(files)
		require.Contains(t, got, "… +2 more")
	})
}
