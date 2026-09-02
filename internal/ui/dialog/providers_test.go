package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func newProvidersDialog(t *testing.T, ws *configWorkspace) *Providers {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewProviders(&common.Common{Workspace: ws, Styles: &s}, true)
}

// visibleProviderIDs reports the provider rows currently in the list.
func visibleProviderIDs(m *Providers) []string {
	ids := make([]string, 0, m.list.Len())
	for i := range m.list.Len() {
		if item, ok := m.list.ItemAt(i).(*ProviderItem); ok {
			ids = append(ids, item.ID())
		}
	}
	return ids
}

// TestTheProviderListOpensBeforeTheCatalogLands mirrors the models
// dialog: fetching the catalog can block for as long as a refresh takes,
// and during onboarding this dialog is the only way in.
func TestTheProviderListOpensBeforeTheCatalogLands(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newProvidersDialog(t, ws)

	require.False(t, m.catalogLoaded)
	require.NotNil(t, m.InitialCmd(), "the dialog must ask for the catalog")
	require.Equal(t, []string{testProviderID}, visibleProviderIDs(m),
		"the configured provider carries the list until the catalog arrives")
}

// TestTheCatalogAddsProvidersWithoutDuplicating pins the merge: a
// configured provider also present in the catalog must appear once.
func TestTheCatalogAddsProvidersWithoutDuplicating(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newProvidersDialog(t, ws)
	m.SetProviders(twoProviderCatalog())

	require.Equal(t, []string{testProviderID, otherProviderID}, visibleProviderIDs(m),
		"the configured provider leads, and appears exactly once")
}

// TestConfiguredProvidersAreMarked separates the two groups the user has
// to tell apart: which providers already hold credentials.
func TestConfiguredProvidersAreMarked(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newProvidersDialog(t, ws)
	m.SetProviders(twoProviderCatalog())

	require.True(t, m.items[0].configured, testProviderID+" is in the config")
	require.False(t, m.items[1].configured, otherProviderID+" has no credentials yet")
}

// TestConfirmingAProviderReportsItsCredentialState is what lets the flow
// skip the credential step for a provider that already has one.
func TestConfirmingAProviderReportsItsCredentialState(t *testing.T) {
	t.Parallel()

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	ctrlE := tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}

	t.Run("a configured provider", func(t *testing.T) {
		t.Parallel()

		ws := &configWorkspace{cfg: modelsConfig(t)}
		m := newProvidersDialog(t, ws)
		m.SetProviders(twoProviderCatalog())
		m.list.SetSelected(0)

		action, ok := m.HandleMsg(enter).(ActionSelectProvider)
		require.True(t, ok)
		require.Equal(t, testProviderID, string(action.Provider.ID))
		require.True(t, action.Configured)
		require.False(t, action.ReAuthenticate)
	})

	t.Run("an unconfigured provider", func(t *testing.T) {
		t.Parallel()

		ws := &configWorkspace{cfg: modelsConfig(t)}
		m := newProvidersDialog(t, ws)
		m.SetProviders(twoProviderCatalog())
		m.list.SetSelected(1)

		action, ok := m.HandleMsg(enter).(ActionSelectProvider)
		require.True(t, ok)
		require.Equal(t, otherProviderID, string(action.Provider.ID))
		require.False(t, action.Configured)
	})

	t.Run("editing a configured provider", func(t *testing.T) {
		t.Parallel()

		ws := &configWorkspace{cfg: modelsConfig(t)}
		m := newProvidersDialog(t, ws)
		m.SetProviders(twoProviderCatalog())
		m.list.SetSelected(0)

		action, ok := m.HandleMsg(ctrlE).(ActionSelectProvider)
		require.True(t, ok)
		require.True(t, action.ReAuthenticate,
			"ctrl+e is how a user reaches the base URL of a provider that already works")
	})
}

// TestFilteringNarrowsTheProviderList pins the search box, the only way
// to reach a provider far down a long catalog.
func TestFilteringNarrowsTheProviderList(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newProvidersDialog(t, ws)
	m.SetProviders(twoProviderCatalog())

	for _, r := range "globex" {
		m.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	require.Equal(t, []string{otherProviderID}, visibleProviderIDs(m))
}

// TestTheAddProviderRowIsAlwaysPresent pins the way out when nothing in
// the catalog is what the user wants: unlike every other row, it must
// survive both an empty query and one that matches nothing.
func TestTheAddProviderRowIsAlwaysPresent(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newProvidersDialog(t, ws)
	m.SetProviders(twoProviderCatalog())

	requireTrailingAddRow := func(t *testing.T) {
		t.Helper()
		require.Positive(t, m.list.Len())
		_, ok := m.list.ItemAt(m.list.Len() - 1).(*AddProviderItem)
		require.True(t, ok, "the add-provider row must be the last item")
	}

	requireTrailingAddRow(t)
	require.Equal(t, len(twoProviderCatalog())+1, m.list.Len(),
		"the add-provider row is one more than the provider count")

	for _, r := range "nonexistentquery" {
		m.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	require.Equal(t, 1, m.list.Len(), "a query matching nothing still leaves the add-provider row")
	requireTrailingAddRow(t)
}

// TestSelectingTheAddProviderRowReturnsAddCustomProvider is how the
// list hands off to the custom provider form instead of a catalog pick.
func TestSelectingTheAddProviderRowReturnsAddCustomProvider(t *testing.T) {
	t.Parallel()

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	ctrlE := tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newProvidersDialog(t, ws)
	m.SetProviders(twoProviderCatalog())
	m.list.SetSelected(m.list.Len() - 1)

	require.Nil(t, m.HandleMsg(ctrlE), "ctrl+e has no credentials to edit on the add-provider row")

	action, ok := m.HandleMsg(enter).(ActionAddCustomProvider)
	require.True(t, ok)
	require.Equal(t, twoProviderCatalog(), action.Catalog,
		"the already-fetched catalog is carried along instead of being fetched again")
}
