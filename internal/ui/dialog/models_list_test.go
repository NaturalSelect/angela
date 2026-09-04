package dialog

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// newTestModelsList builds a two-group, two-item-per-group list the way
// the models dialog does: constructed empty, then populated via
// SetGroups.
func newTestModelsList(t *testing.T) *ModelsList {
	t.Helper()
	s := styles.CharmtonePantera()
	prov := catwalk.Provider{ID: "acme", Name: "Acme"}
	item := func(id, name string) *ModelItem {
		return NewModelItem(&s, prov, config.ProviderModel{Model: catwalk.Model{ID: id, Name: name}}, config.SlotMain, false)
	}
	g1 := NewModelGroup(&s, "Group A", false, item("a1", "Model A1"), item("a2", "Model A2"))
	g2 := NewModelGroup(&s, "Group B", false, item("b1", "Model B1"), item("b2", "Model B2"))

	l := NewModelsList(&s)
	l.SetGroups(g1, g2)
	return l
}

// modelItemIDAt returns the ID of the *ModelItem currently selected, or
// "" if the selection is not a model item.
func modelItemIDAt(l *ModelsList) string {
	mi, ok := l.SelectedItem().(*ModelItem)
	if !ok {
		return ""
	}
	return mi.ID()
}

// TestModelsList_SetSelectedSkipsHeaders pins the only way the dialog
// actually calls SetSelected: index 0 always resolves past the leading
// group header onto the first real model.
func TestModelsList_SetSelectedSkipsHeaders(t *testing.T) {
	t.Parallel()

	l := newTestModelsList(t)
	l.SetSelected(0)
	require.Equal(t, modelKey("acme", "a1"), modelItemIDAt(l))
}

// TestModelsList_SetSelectedOutOfRangeDelegates verifies indexes outside
// the model-item count bypass the header-skipping walk entirely and
// forward straight to the underlying list, which is responsible for its
// own bounds handling.
func TestModelsList_SetSelectedOutOfRangeDelegates(t *testing.T) {
	t.Parallel()

	l := newTestModelsList(t)
	l.SetSelected(-1)
	require.Equal(t, -1, l.Selected())

	l.SetSelected(1000)
	require.Equal(t, -1, l.Selected(), "an index past the whole list clamps to no selection")
}

// TestModelsList_SelectFirstAndLastSkipNonModelItems verifies both ends
// of the list resolve onto a real model, skipping the group header and
// trailing spacer respectively.
func TestModelsList_SelectFirstAndLastSkipNonModelItems(t *testing.T) {
	t.Parallel()

	l := newTestModelsList(t)

	require.True(t, l.SelectFirst())
	require.Equal(t, modelKey("acme", "a1"), modelItemIDAt(l))

	require.True(t, l.SelectLast())
	require.Equal(t, modelKey("acme", "b2"), modelItemIDAt(l))
}

// TestModelsList_SelectPrevSkipsGroupBoundary verifies stepping backward
// from the second group's first model lands on the previous group's
// last model, skipping the spacer and header in between.
func TestModelsList_SelectPrevSkipsGroupBoundary(t *testing.T) {
	t.Parallel()

	l := newTestModelsList(t)
	l.SetSelected(0)
	require.Equal(t, modelKey("acme", "a1"), modelItemIDAt(l))

	require.True(t, l.SelectNext())
	require.Equal(t, modelKey("acme", "a2"), modelItemIDAt(l))
	require.True(t, l.SelectNext())
	require.Equal(t, modelKey("acme", "b1"), modelItemIDAt(l), "next must cross the group boundary onto the next group's first model")

	require.True(t, l.SelectPrev())
	require.Equal(t, modelKey("acme", "a2"), modelItemIDAt(l), "prev must cross back over the spacer and header")
}

// TestModelsList_IsSelectedFirstAndLastLeaveSelectionUnchanged verifies
// both predicates are read-only: the selection after the check must be
// exactly what it was before.
func TestModelsList_IsSelectedFirstAndLastLeaveSelectionUnchanged(t *testing.T) {
	t.Parallel()

	l := newTestModelsList(t)
	l.SetSelected(0)

	require.True(t, l.IsSelectedFirst())
	require.False(t, l.IsSelectedLast())
	require.Equal(t, modelKey("acme", "a1"), modelItemIDAt(l), "the check must not move the selection")

	l.SelectLast()
	require.False(t, l.IsSelectedFirst())
	require.True(t, l.IsSelectedLast())
	require.Equal(t, modelKey("acme", "b2"), modelItemIDAt(l), "the check must not move the selection")
}

// TestModelGroups_LenAndString exercises the fuzzy.Source adapter over a
// flat run of groups: Len reports the total item count across every
// group and String indexes into the right group's item by its filter
// text.
func TestModelGroups_LenAndString(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	prov := catwalk.Provider{ID: "acme", Name: "Acme"}
	item := func(id, name string) *ModelItem {
		return NewModelItem(&s, prov, config.ProviderModel{Model: catwalk.Model{ID: id, Name: name}}, config.SlotMain, false)
	}
	groups := modelGroups{
		NewModelGroup(&s, "Group A", false, item("a1", "Model A1")),
		NewModelGroup(&s, "Group B", false, item("b1", "Model B1"), item("b2", "Model B2")),
	}

	require.Equal(t, 3, groups.Len())
	require.Equal(t, "Model A1", groups.String(0))
	require.Equal(t, "Model B1", groups.String(1))
	require.Equal(t, "Model B2", groups.String(2))
	require.Equal(t, "", groups.String(99), "an index past every group must not panic")
}
