package dialog

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

// TestProviderItem_Name verifies the display name falls back to the
// provider ID when the catalog left the name blank.
func TestProviderItem_Name(t *testing.T) {
	t.Parallel()

	t.Run("named provider", func(t *testing.T) {
		t.Parallel()
		s := styles.CharmtonePantera()
		p := NewProviderItem(&s, catwalk.Provider{ID: "acme", Name: "Acme"}, false)
		require.Equal(t, "Acme", p.Name())
	})

	t.Run("nameless provider falls back to its id", func(t *testing.T) {
		t.Parallel()
		s := styles.CharmtonePantera()
		p := NewProviderItem(&s, catwalk.Provider{ID: "acme"}, false)
		require.Equal(t, "acme", p.Name())
	})
}

// TestProviderItem_FilterIDRender pins the basic ListItem surface: filter
// text tracks the display name, ID tracks the provider ID, and the
// "Configured" marker only appears when the provider already has
// credentials.
func TestProviderItem_FilterIDRender(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	p := NewProviderItem(&s, catwalk.Provider{ID: "acme", Name: "Acme"}, true)
	require.True(t, p.Finished())
	require.Equal(t, "Acme", p.Filter())
	require.Equal(t, "acme", p.ID())

	rendered := ansi.Strip(p.Render(40))
	require.Contains(t, rendered, "Acme")
	require.Contains(t, rendered, providerConfiguredInfo)

	unconfigured := NewProviderItem(&s, catwalk.Provider{ID: "acme", Name: "Acme"}, false)
	require.NotContains(t, ansi.Strip(unconfigured.Render(40)), providerConfiguredInfo)
}

// TestProviderItem_SetFocusedBumpsOnChange verifies the version-bump
// convention: only an actual state change invalidates the render cache.
func TestProviderItem_SetFocusedBumpsOnChange(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	p := NewProviderItem(&s, catwalk.Provider{ID: "acme"}, false)
	before := p.Version()

	p.SetFocused(false) // already false: no-op
	require.Equal(t, before, p.Version())

	p.SetFocused(true)
	require.Greater(t, p.Version(), before)

	afterFirstBump := p.Version()
	p.SetFocused(true) // already true: no-op
	require.Equal(t, afterFirstBump, p.Version())
}

// TestProviderItem_SetMatchBumpsOnChange mirrors the focus case for the
// fuzzy match assigned to the item.
func TestProviderItem_SetMatchBumpsOnChange(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	p := NewProviderItem(&s, catwalk.Provider{ID: "acme"}, false)
	before := p.Version()

	p.SetMatch(fuzzy.Match{})
	require.Equal(t, before, p.Version(), "an identical zero-value match must not bump")

	m := fuzzy.Match{Str: "acme", Index: 1, Score: 5, MatchedIndexes: []int{0, 1}}
	p.SetMatch(m)
	require.Greater(t, p.Version(), before)

	afterBump := p.Version()
	p.SetMatch(m)
	require.Equal(t, afterBump, p.Version(), "an identical match must not bump again")
}

// TestAddProviderItem covers the trailing "add custom provider" row,
// which is always present regardless of the current search query.
func TestAddProviderItem(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	p := NewAddProviderItem(&s)

	require.True(t, p.Finished())
	require.Equal(t, addProviderItemName, p.Filter())
	require.Equal(t, addProviderItemID, p.ID())

	rendered := ansi.Strip(p.Render(60))
	require.Contains(t, rendered, addProviderItemName)
	require.Contains(t, rendered, addProviderItemInfo)

	before := p.Version()
	p.SetFocused(false)
	require.Equal(t, before, p.Version(), "already-blurred must not bump")
	p.SetFocused(true)
	require.Greater(t, p.Version(), before)

	afterFocus := p.Version()
	m := fuzzy.Match{Str: "x", Index: 2}
	p.SetMatch(m)
	require.Greater(t, p.Version(), afterFocus)
	afterMatch := p.Version()
	p.SetMatch(m)
	require.Equal(t, afterMatch, p.Version(), "an identical match must not bump again")
}
