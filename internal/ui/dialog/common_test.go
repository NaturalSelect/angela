package dialog

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// fakeSizer is a minimal [sizer] implementation for exercising
// sizeDialogList without a real list.
type fakeSizer struct {
	total         int
	width, height int
}

func (f *fakeSizer) TotalHeight() int { return f.total }
func (f *fakeSizer) SetSize(w, h int) { f.width, f.height = w, h }

func TestSizeDialogList(t *testing.T) {
	t.Parallel()
	sty := testStyles()

	t.Run("reserves a scrollbar column when content overflows the viewport", func(t *testing.T) {
		t.Parallel()
		s := &fakeSizer{total: 500}
		listHeight, listTotalHeight, listWidth := sizeDialogList(sty, s, 40, 20)
		require.Equal(t, 500, listTotalHeight)
		require.Less(t, listWidth, 40, "a scrollbar column must be reserved when content overflows")
		require.Equal(t, listWidth, s.width)
		require.Equal(t, listHeight, s.height)
	})

	t.Run("uses the full width when content fits without scrolling", func(t *testing.T) {
		t.Parallel()
		s := &fakeSizer{total: 1}
		_, _, listWidth := sizeDialogList(sty, s, 40, 20)
		require.Equal(t, 40, listWidth)
	})
}

func TestJoinScrollbar(t *testing.T) {
	t.Parallel()
	sty := testStyles()

	t.Run("appends a scrollbar when content overflows the viewport", func(t *testing.T) {
		t.Parallel()
		out := joinScrollbar(sty, "hello", 5, 50, 5, 0)
		require.Greater(t, lipgloss.Width(out), lipgloss.Width("hello"))
	})

	t.Run("returns the view unchanged when content fits", func(t *testing.T) {
		t.Parallel()
		out := joinScrollbar(sty, "hello", 5, 5, 5, 0)
		require.Equal(t, "hello", out)
	})
}

// fakeInfoItem is a minimal list.Item + infoColumnItem for exercising
// applyInfoColumnVisibility without a real session/command item.
type fakeInfoItem struct {
	*list.Versioned
	info     string
	hideInfo bool
}

func (f *fakeInfoItem) Render(int) string     { return "" }
func (f *fakeInfoItem) Finished() bool        { return true }
func (f *fakeInfoItem) InfoText() string      { return f.info }
func (f *fakeInfoItem) SetHideInfo(hide bool) { f.hideInfo = hide }

func TestApplyInfoColumnVisibility(t *testing.T) {
	t.Parallel()

	t.Run("hides the info column once it would take too much of the row", func(t *testing.T) {
		t.Parallel()
		items := []list.Item{&fakeInfoItem{Versioned: list.NewVersioned(), info: "a very long timestamp string"}}
		applyInfoColumnVisibility(items, 20, sessionInfoMaxPercent)
		require.True(t, items[0].(*fakeInfoItem).hideInfo)
	})

	t.Run("keeps the info column when it fits comfortably", func(t *testing.T) {
		t.Parallel()
		items := []list.Item{&fakeInfoItem{Versioned: list.NewVersioned(), info: "x"}}
		applyInfoColumnVisibility(items, 200, sessionInfoMaxPercent)
		require.False(t, items[0].(*fakeInfoItem).hideInfo)
	})

	t.Run("items without info text are ignored when measuring the widest entry", func(t *testing.T) {
		t.Parallel()
		items := []list.Item{&fakeInfoItem{Versioned: list.NewVersioned()}}
		applyInfoColumnVisibility(items, 10, sessionInfoMaxPercent)
		require.False(t, items[0].(*fakeInfoItem).hideInfo)
	})

	t.Run("a zero row width never hides the column", func(t *testing.T) {
		t.Parallel()
		items := []list.Item{&fakeInfoItem{Versioned: list.NewVersioned(), info: "anything"}}
		applyInfoColumnVisibility(items, 0, sessionInfoMaxPercent)
		require.False(t, items[0].(*fakeInfoItem).hideInfo)
	})
}

func TestShortHelpLine(t *testing.T) {
	t.Parallel()
	sty := testStyles()
	h := help.New()
	h.Styles = sty.DialogHelpStyles()

	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "alpha")),
		key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "beta")),
		key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "gamma"), key.WithDisabled()),
	}

	t.Run("non-positive width returns an empty line", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, shortHelpLine(&h, bindings, 0))
	})

	t.Run("disabled bindings are skipped", func(t *testing.T) {
		t.Parallel()
		out := ansi.Strip(shortHelpLine(&h, bindings, 200))
		require.Contains(t, out, "alpha")
		require.Contains(t, out, "beta")
		require.NotContains(t, out, "gamma")
	})

	t.Run("a width too small for every hint truncates with an ellipsis", func(t *testing.T) {
		t.Parallel()
		full := ansi.Strip(shortHelpLine(&h, bindings[:2], 200))
		fullWidth := lipgloss.Width(full)

		out := ansi.Strip(shortHelpLine(&h, bindings[:2], fullWidth-1))
		require.NotEqual(t, full, out)
		require.Contains(t, out, "…")
	})
}

func TestAdjustOnboardingInputCursor(t *testing.T) {
	t.Parallel()
	sty := testStyles()

	t.Run("nil cursor stays nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, adjustOnboardingInputCursor(sty, nil))
	})

	t.Run("removes the dialog view frame offset in place", func(t *testing.T) {
		t.Parallel()
		cur := tea.NewCursor(10, 10)
		got := adjustOnboardingInputCursor(sty, cur)
		require.Same(t, cur, got)
	})
}

func TestRenderContext_Render(t *testing.T) {
	t.Parallel()
	sty := testStyles()

	t.Run("title and parts render together", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 40)
		rc.Title = "Hello"
		rc.AddPart("body")
		out := ansi.Strip(rc.Render())
		require.Contains(t, out, "Hello")
		require.Contains(t, out, "body")
	})

	t.Run("AddPart drops empty parts", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 40)
		rc.AddPart("")
		require.Empty(t, rc.Parts)
	})

	t.Run("title info renders alongside the title when it fits", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 60)
		rc.Title = "Hi"
		rc.TitleInfo = "[3/5]"
		out := ansi.Strip(rc.Render())
		require.Contains(t, out, "[3/5]")
	})

	t.Run("title info is dropped when it would not fit beside the title", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 10)
		rc.Title = "A very long title that eats the whole available width"
		rc.TitleInfo = "[extra]"
		out := ansi.Strip(rc.Render())
		require.NotContains(t, out, "[extra]")
	})

	t.Run("gap inserts blank lines between parts", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 40)
		rc.Gap = 1
		rc.AddPart("first")
		rc.AddPart("second")
		out := ansi.Strip(rc.Render())

		lines := strings.Split(out, "\n")
		require.Len(t, lines, 5, "top border, first, blank gap, second, bottom border")
		require.Equal(t, "first", strings.TrimSpace(strings.Trim(lines[1], "│")))
		require.Equal(t, "", strings.TrimSpace(strings.Trim(lines[2], "│")))
		require.Equal(t, "second", strings.TrimSpace(strings.Trim(lines[3], "│")))
	})

	t.Run("help renders after the parts", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 40)
		rc.AddPart("body")
		rc.Help = "help line"
		out := ansi.Strip(rc.Render())
		require.Greater(t, strings.Index(out, "help line"), strings.Index(out, "body"))
	})

	t.Run("onboarding mode renders content without the bordered box", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 40)
		rc.IsOnboarding = true
		rc.AddPart("body")
		out := rc.Render()
		require.NotContains(t, out, "╭")
	})

	t.Run("non-onboarding mode draws a bordered box", func(t *testing.T) {
		t.Parallel()
		rc := NewRenderContext(sty, 40)
		rc.AddPart("body")
		out := rc.Render()
		require.Contains(t, out, "╭")
	})
}
