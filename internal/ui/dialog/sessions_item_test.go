package dialog

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/dustin/go-humanize"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

// TestSessionItem_FilterAndID verifies the filterable value and
// identifier both come from the wrapped session.
func TestSessionItem_FilterAndID(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := &SessionItem{
		Versioned: list.NewVersioned(),
		Session:   session.Session{ID: "sess-1", Title: "My Session"},
		t:         &sty,
	}
	require.Equal(t, "My Session", item.Filter())
	require.Equal(t, "sess-1", item.ID())
	require.True(t, item.Finished())
}

// TestSessionItem_InfoText verifies the info column is the humanized
// relative time of the session's last update.
func TestSessionItem_InfoText(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	ts := time.Now().Add(-48 * time.Hour).Unix()
	item := &SessionItem{
		Versioned: list.NewVersioned(),
		Session:   session.Session{ID: "sess-1", Title: "My Session", UpdatedAt: ts},
		t:         &sty,
	}
	require.Equal(t, humanize.Time(time.Unix(ts, 0)), item.InfoText())
}

// TestSessionItem_SetHideInfo verifies the timestamp column disappears
// from the rendered row once hidden, and that reapplying the same
// value is a no-op for the version counter.
func TestSessionItem_SetHideInfo(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	ts := time.Now().Add(-1 * time.Hour).Unix()
	item := &SessionItem{
		Versioned: list.NewVersioned(),
		Session:   session.Session{ID: "sess-1", Title: "My Session", UpdatedAt: ts},
		t:         &sty,
	}
	info := item.InfoText()
	require.Contains(t, ansi.Strip(item.Render(60)), info)

	before := item.Version()
	item.SetHideInfo(true)
	require.Greater(t, item.Version(), before)
	require.NotContains(t, ansi.Strip(item.Render(60)), info)

	before = item.Version()
	item.SetHideInfo(true)
	require.Equal(t, before, item.Version(), "reapplying the same value must not bump")
}

// TestSessionItem_Render_NormalMode verifies the title shows up in
// both the blurred and focused row.
func TestSessionItem_Render_NormalMode(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := &SessionItem{
		Versioned: list.NewVersioned(),
		Session:   session.Session{ID: "sess-1", Title: "My Session"},
		t:         &sty,
	}
	require.Contains(t, ansi.Strip(item.Render(40)), "My Session")

	item.SetFocused(true)
	require.Contains(t, ansi.Strip(item.Render(40)), "My Session")
}

// TestSessionItem_Render_DeletingMode verifies the confirmation row
// still shows the session's title.
func TestSessionItem_Render_DeletingMode(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := &SessionItem{
		Versioned:    list.NewVersioned(),
		Session:      session.Session{ID: "sess-1", Title: "My Session"},
		t:            &sty,
		sessionsMode: sessionsModeDeleting,
	}
	require.Contains(t, ansi.Strip(item.Render(40)), "My Session")

	item.SetFocused(true)
	require.Contains(t, ansi.Strip(item.Render(40)), "My Session")
}

// TestSessionItem_Render_UpdatingMode verifies a blurred rename row
// still reads as the plain title, while the focused row switches to
// the live rename input.
func TestSessionItem_Render_UpdatingMode(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("blurred shows the plain title row", func(t *testing.T) {
		t.Parallel()
		items := sessionItems(&sty, sessionsModeUpdating, session.Session{ID: "sess-1", Title: "My Session"})
		item := items[0].(*SessionItem)
		require.Contains(t, ansi.Strip(item.Render(40)), "My Session")
	})

	t.Run("focused shows the rename input", func(t *testing.T) {
		t.Parallel()
		items := sessionItems(&sty, sessionsModeUpdating, session.Session{ID: "sess-1", Title: "My Session"})
		item := items[0].(*SessionItem)
		item.SetFocused(true)
		item.HandleInput(tea.KeyPressMsg{Code: 'X', Text: "X"})
		require.Contains(t, ansi.Strip(item.Render(40)), "X")
	})
}

// TestSessionItem_HandleInputAndCursor verifies keystrokes reach the
// rename input, bump the version, and the cursor position stays
// available for the dialog to draw.
func TestSessionItem_HandleInputAndCursor(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	items := sessionItems(&sty, sessionsModeUpdating, session.Session{ID: "sess-1", Title: "My Session"})
	item := items[0].(*SessionItem)

	before := item.Version()
	item.HandleInput(tea.KeyPressMsg{Code: 'A', Text: "A"})
	require.Equal(t, "A", item.InputValue())
	require.Greater(t, item.Version(), before)
	require.NotNil(t, item.Cursor())
}

// TestSessionItem_Render_HighlightsFuzzyMatches exercises the
// highlighted-range branch of renderItem with both a contiguous run
// and a disjoint index, so the underline segments must be split.
func TestSessionItem_Render_HighlightsFuzzyMatches(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := &SessionItem{
		Versioned: list.NewVersioned(),
		Session:   session.Session{ID: "sess-1", Title: "My Session"},
		t:         &sty,
	}
	item.SetMatch(fuzzy.Match{
		Str:            "My Session",
		MatchedIndexes: []int{0, 1, 3},
	})

	require.Contains(t, ansi.Strip(item.Render(40)), "My Session")
}

// TestSessionItems verifies the factory tags every item with the
// requested mode and, only in updating mode, wires up a focused rename
// input per session.
func TestSessionItems(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	sessions := []session.Session{
		{ID: "s1", Title: "One"},
		{ID: "s2", Title: "Two"},
	}

	t.Run("normal mode leaves the rename input unset", func(t *testing.T) {
		t.Parallel()
		items := sessionItems(&sty, sessionsModeNormal, sessions...)
		require.Len(t, items, 2)
		for i, it := range items {
			si, ok := it.(*SessionItem)
			require.True(t, ok)
			require.Equal(t, sessions[i].ID, si.ID())
			require.Equal(t, sessionsModeNormal, si.sessionsMode)
		}
	})

	t.Run("updating mode focuses a rename input per item", func(t *testing.T) {
		t.Parallel()
		items := sessionItems(&sty, sessionsModeUpdating, sessions...)
		require.Len(t, items, 2)
		for _, it := range items {
			si, ok := it.(*SessionItem)
			require.True(t, ok)
			require.Equal(t, sessionsModeUpdating, si.sessionsMode)
			require.Equal(t, "", si.InputValue())
		}
	})
}

// TestMatchedRanges pins the grouping contract: consecutive indexes
// merge into one range, and a gap starts a new one.
func TestMatchedRanges(t *testing.T) {
	t.Parallel()

	require.Equal(t, [][2]int{}, matchedRanges(nil))
	require.Equal(t, [][2]int{{2, 2}}, matchedRanges([]int{2}))
	require.Equal(t, [][2]int{{0, 2}}, matchedRanges([]int{0, 1, 2}))
	require.Equal(t, [][2]int{{0, 1}, {3, 3}}, matchedRanges([]int{0, 1, 3}))
}

// TestBytePosToVisibleCharPos verifies a plain ASCII string's byte
// offsets already equal its visible character positions, both from
// the start of the string and from a later offset.
func TestBytePosToVisibleCharPos(t *testing.T) {
	t.Parallel()

	start, stop := bytePosToVisibleCharPos("hello world", [2]int{0, 4})
	require.Equal(t, 0, start)
	require.Equal(t, 4, stop)

	start, stop = bytePosToVisibleCharPos("hello world", [2]int{6, 10})
	require.Equal(t, 6, start)
	require.Equal(t, 10, stop)
}
