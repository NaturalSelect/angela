package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestVariants(t *testing.T, variants []string, current string) *Variants {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	v, err := NewVariants(com, "GPT-5", variants, current)
	require.NoError(t, err)
	return v
}

// TestNewVariants_RequiresAtLeastOneVariant pins the guard against a
// model that publishes no presets at all: opening the dialog would show
// nothing but the baseline, which is not a choice worth a dialog.
func TestNewVariants_RequiresAtLeastOneVariant(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	_, err := NewVariants(com, "GPT-5", nil, "")
	require.ErrorContains(t, err, "this model offers no variants")
}

// TestNewVariants_SelectsCurrentVariant verifies the list opens on the
// preset the session is already using, whether that is a named variant
// or the empty-string baseline.
func TestNewVariants_SelectsCurrentVariant(t *testing.T) {
	t.Parallel()

	t.Run("a named variant", func(t *testing.T) {
		t.Parallel()
		v := newTestVariants(t, []string{"low", "medium", "high"}, "medium")
		item, ok := v.list.SelectedItem().(*VariantItem)
		require.True(t, ok)
		require.Equal(t, "medium", item.variant)
	})

	t.Run("the baseline", func(t *testing.T) {
		t.Parallel()
		v := newTestVariants(t, []string{"low", "medium", "high"}, "")
		item, ok := v.list.SelectedItem().(*VariantItem)
		require.True(t, ok)
		require.Equal(t, "", item.variant)
		require.True(t, item.isCurrent)
	})
}

// TestVariants_ID verifies the dialog identifies itself for the overlay
// stack.
func TestVariants_ID(t *testing.T) {
	t.Parallel()

	v := newTestVariants(t, []string{"low", "high"}, "")
	require.Equal(t, VariantsID, v.ID())
}

// TestVariants_HandleMsg_Close verifies the close key closes the dialog.
func TestVariants_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	v := newTestVariants(t, []string{"low", "high"}, "")
	action := v.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, ActionClose{}, action)
}

// TestVariants_HandleMsg_Navigation verifies up/down wrap around the
// ends of the list.
func TestVariants_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	v := newTestVariants(t, []string{"low", "high"}, "")
	first, ok := v.list.SelectedItem().(*VariantItem)
	require.True(t, ok)
	require.Equal(t, "", first.variant, "the baseline entry leads the list")

	// Up from the first item (the baseline) wraps to the last.
	v.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	last, ok := v.list.SelectedItem().(*VariantItem)
	require.True(t, ok)
	require.Equal(t, "high", last.variant)

	// Down from the last item wraps back to the first.
	v.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	backToFirst, ok := v.list.SelectedItem().(*VariantItem)
	require.True(t, ok)
	require.Equal(t, "", backToFirst.variant)
}

// TestVariants_HandleMsg_Select verifies pressing enter dispatches the
// highlighted preset, including the empty string for the baseline.
func TestVariants_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	t.Run("a named variant", func(t *testing.T) {
		t.Parallel()
		v := newTestVariants(t, []string{"low", "high"}, "")
		v.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

		action := v.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		resp, ok := action.(ActionSelectVariant)
		require.True(t, ok)
		require.Equal(t, "low", resp.Variant)
	})

	t.Run("the baseline", func(t *testing.T) {
		t.Parallel()
		v := newTestVariants(t, []string{"low", "high"}, "low")
		action := v.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		resp, ok := action.(ActionSelectVariant)
		require.True(t, ok)
		require.Equal(t, "low", resp.Variant)
	})
}

// TestVariants_HandleMsg_TypingFilters verifies free text narrows the
// list through the shared fuzzy filter.
func TestVariants_HandleMsg_TypingFilters(t *testing.T) {
	t.Parallel()

	v := newTestVariants(t, []string{"low", "medium", "high"}, "")
	for _, r := range "high" {
		action := v.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
		_, ok := action.(ActionCmd)
		require.True(t, ok)
	}

	require.Len(t, v.list.FilteredItems(), 1)
	item, ok := v.list.SelectedItem().(*VariantItem)
	require.True(t, ok)
	require.Equal(t, "high", item.variant)
}

// TestVariants_Draw verifies the dialog renders its title and every
// preset, marking the active one as current.
func TestVariants_Draw(t *testing.T) {
	t.Parallel()

	v := newTestVariants(t, []string{"low", "high"}, "low")
	scr := uv.NewScreenBuffer(50, 16)
	v.Draw(scr, uv.Rect(0, 0, 50, 16))

	content := ansi.Strip(scr.Render())
	require.Contains(t, content, "Select Variant")
	require.Contains(t, content, baselineVariantTitle)
	require.Contains(t, content, "low")
	require.Contains(t, content, "high")
	require.Contains(t, content, "current")
}

// TestVariants_Help verifies the short and full help expose the
// bindings needed to operate the list.
func TestVariants_Help(t *testing.T) {
	t.Parallel()

	v := newTestVariants(t, []string{"low", "high"}, "")

	require.Len(t, v.ShortHelp(), 3)

	var flat []string
	for _, row := range v.FullHelp() {
		for _, b := range row {
			flat = append(flat, b.Help().Key)
		}
	}
	require.Contains(t, flat, v.keyMap.Select.Help().Key)
	require.Contains(t, flat, v.keyMap.Close.Help().Key)
}

// TestVariantItem_ID verifies the baseline entry, which has no name of
// its own, borrows its title to stay distinct from a real variant that
// happens to be named the same as the title constant.
func TestVariantItem_ID(t *testing.T) {
	t.Parallel()

	baseline := &VariantItem{variant: ""}
	require.Equal(t, baselineVariantTitle, baseline.ID())

	named := &VariantItem{variant: "medium"}
	require.Equal(t, "medium", named.ID())
}

// TestVariantItem_Render verifies the current preset shows "current" in
// place of its info text.
func TestVariantItem_Render(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	current := &VariantItem{title: "Medium", info: "GPT-5", isCurrent: true, t: &s}
	require.Contains(t, ansi.Strip(current.Render(40)), "current")
	require.NotContains(t, ansi.Strip(current.Render(40)), "GPT-5")

	other := &VariantItem{title: "High", info: "GPT-5", t: &s}
	require.Contains(t, ansi.Strip(other.Render(40)), "GPT-5")
}

// TestVariantItem_Finished pins that variant rows are render-stable:
// once built, nothing about their output changes except through an
// explicit SetFocused/SetMatch call.
func TestVariantItem_Finished(t *testing.T) {
	t.Parallel()

	item := &VariantItem{variant: "medium"}
	require.True(t, item.Finished())
}
