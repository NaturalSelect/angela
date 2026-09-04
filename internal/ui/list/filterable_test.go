package list

import (
	"testing"

	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

// filterItem is a minimal FilterableItem test double that also implements
// MatchSettable so tests can observe the fuzzy.Match it was given.
type filterItem struct {
	*Versioned
	id     string
	filter string
	match  fuzzy.Match
	setHit int
}

func newFilterItem(id, filter string) *filterItem {
	return &filterItem{Versioned: NewVersioned(), id: id, filter: filter}
}

func (f *filterItem) Render(int) string      { return f.id }
func (f *filterItem) Finished() bool         { return true }
func (f *filterItem) Filter() string         { return f.filter }
func (f *filterItem) SetMatch(m fuzzy.Match) { f.match = m; f.setHit++ }

func filterItemIDs(items []Item) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.(*filterItem).id
	}
	return ids
}

func TestFilterableItemsSource(t *testing.T) {
	t.Parallel()

	src := FilterableItemsSource{
		newFilterItem("a", "alpha"),
		newFilterItem("b", "bravo"),
	}
	require.Equal(t, 2, src.Len())
	require.Equal(t, "alpha", src.String(0))
	require.Equal(t, "bravo", src.String(1))
}

func TestFilterableList_FilteredItems_EmptyQueryResetsMatch(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "alpha")
	b := newFilterItem("b", "bravo")
	f := NewFilterableList(a, b)

	items := f.FilteredItems()
	require.Len(t, items, 2)
	require.Equal(t, 1, a.setHit, "empty query still resets the match via SetMatch")
	require.Equal(t, fuzzy.Match{}, a.match)
	require.Equal(t, []string{"a", "b"}, filterItemIDs(items))
}

func TestFilterableList_SetFilter_MatchesSubsequence(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "apple")
	b := newFilterItem("b", "banana")
	c := newFilterItem("c", "grape")
	f := NewFilterableList(a, b, c)
	f.SetSize(40, 10)

	f.SetFilter("ap")
	got := filterItemIDs(f.FilteredItems())
	require.ElementsMatch(t, []string{"a", "c"}, got, "\"ap\" is a subsequence of apple and grape but not banana")
	require.Greater(t, a.setHit, 0, "matched items must receive their fuzzy.Match")
}

func TestFilterableList_SetFilter_NoMatchesYieldsEmptySlice(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "apple")
	f := NewFilterableList(a)
	f.SetSize(40, 10)

	f.SetFilter("zzz-no-match")
	require.Empty(t, f.FilteredItems())
}

func TestFilterableList_SetFilter_ScrollsToTop(t *testing.T) {
	t.Parallel()

	items := make([]FilterableItem, 20)
	for i := range items {
		items[i] = newFilterItem(string(rune('a'+i)), "shared-term-"+string(rune('a'+i)))
	}
	f := NewFilterableList(items...)
	f.SetSize(40, 3)
	f.ScrollToIndex(10)
	require.NotZero(t, f.offsetIdx)

	f.SetFilter("shared-term")
	require.Zero(t, f.offsetIdx, "setting a filter must scroll back to the top")
}

func TestFilterableList_SetItems_ReplacesItems(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "alpha")
	f := NewFilterableList(a)

	b := newFilterItem("b", "bravo")
	f.SetItems(b)
	require.Len(t, f.items, 1)
	require.Equal(t, "b", f.items[0].(*filterItem).id)
}

func TestFilterableList_AppendItems(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "alpha")
	f := NewFilterableList(a)

	b := newFilterItem("b", "bravo")
	f.AppendItems(b)
	require.Len(t, f.items, 2)
	require.Equal(t, "a", f.items[0].(*filterItem).id)
	require.Equal(t, "b", f.items[1].(*filterItem).id)
}

func TestFilterableList_PrependItems(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "alpha")
	f := NewFilterableList(a)

	z := newFilterItem("z", "zulu")
	f.PrependItems(z)
	require.Len(t, f.items, 2)
	require.Equal(t, "z", f.items[0].(*filterItem).id)
	require.Equal(t, "a", f.items[1].(*filterItem).id)
}

func TestFilterableList_Render(t *testing.T) {
	t.Parallel()

	a := newFilterItem("a", "alpha")
	f := NewFilterableList(a)
	f.SetSize(40, 10)

	out := f.Render()
	require.Contains(t, out, "a")
}
