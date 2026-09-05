package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// configWorkspace is the least workspace the models dialog needs: the
// config it reads, plus a record of any config field it writes.
type configWorkspace struct {
	workspace.Workspace

	cfg    *config.Config
	writes int
	pruned []config.SelectedModel
}

func (w *configWorkspace) Config() *config.Config { return w.cfg }

func (w *configWorkspace) SetConfigField(config.Scope, string, any) error {
	w.writes++
	return nil
}

func (w *configWorkspace) PruneRecentModels(_ config.Scope, _ config.SlotName, stale []config.SelectedModel) error {
	w.pruned = stale
	return nil
}

// IsInSandbox stubs the sandbox check defaultCommands reads to decide
// whether to offer the /sandbox command; none of these dialogs exercise
// sandbox behavior itself.
func (w *configWorkspace) IsInSandbox() bool { return false }

const (
	globalModelID  = "global-model"
	sessionModelID = "session-model"
	testProviderID = "acme"
)

// modelsConfig builds a config with one provider serving both models and
// main pointed at the global one.
func modelsConfig(t *testing.T, recents ...config.SelectedModel) *config.Config {
	t.Helper()

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set(testProviderID, config.ProviderConfig{
		ID:   testProviderID,
		Name: "Acme",
		Models: []config.ProviderModel{
			{Model: catwalk.Model{ID: globalModelID, Name: "Global Model"}},
			{Model: catwalk.Model{ID: sessionModelID, Name: "Session Model"}},
		},
	})

	cfg := &config.Config{
		Providers: providers,
		Slots: map[config.SlotName]config.SelectedModel{
			config.SlotMain: {Provider: testProviderID, Model: globalModelID},
		},
	}
	if len(recents) > 0 {
		cfg.RecentModels = map[config.SlotName][]config.SelectedModel{
			config.SlotMain: recents,
		}
	}
	return cfg
}

func newModelsDialog(t *testing.T, ws *configWorkspace, active *workspace.ActiveAgent) *Models {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewModels(&common.Common{Workspace: ws, Styles: &s}, false, active)
}

// newOnboardingModelsDialog builds the dialog as the third onboarding
// step opens it: restricted to the provider picked one step earlier.
func newOnboardingModelsDialog(t *testing.T, ws *configWorkspace) *Models {
	t.Helper()
	s := styles.CharmtonePantera()
	m := NewModels(&common.Common{Workspace: ws, Styles: &s}, true, nil)
	m.SetProviders(catalogFor())
	m.RestrictToProvider(testProviderID)
	return m
}

// typeQuery drives the dialog the way the user does, one key at a time,
// so the custom entry is judged by the same path the TUI takes.
func typeQuery(m *Models, query string) {
	for _, r := range query {
		m.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// catalogFor mirrors the configured provider as the fetched catalog
// would report it.
func catalogFor() []catwalk.Provider {
	return []catwalk.Provider{{
		ID:   testProviderID,
		Name: "Acme",
		Models: []catwalk.Model{
			{ID: globalModelID, Name: "Global Model"},
			{ID: sessionModelID, Name: "Session Model"},
		},
	}}
}

// selectedModelID reports which model the list opens on.
func selectedModelID(t *testing.T, m *Models) string {
	t.Helper()
	item := m.list.SelectedItem()
	require.NotNil(t, item, "the list must have a selection")
	modelItem, ok := item.(*ModelItem)
	require.True(t, ok)
	return modelItem.model.ID
}

// TestTheListOpensOnTheSessionsModel is B4. The dialog used to read the
// global main slot, so a session pinned to another model opened with the
// global one highlighted — confirming it looked like a no-op while it
// silently moved the session off its own model.
func TestTheListOpensOnTheSessionsModel(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	active := &workspace.ActiveAgent{
		Slot:     config.SlotMain,
		ModelCfg: config.SelectedModel{Provider: testProviderID, Model: sessionModelID},
	}
	m := newModelsDialog(t, ws, active)
	m.SetProviders(catalogFor())

	require.Equal(t, sessionModelID, selectedModelID(t, m))
}

// TestTheListFallsBackToTheGlobalModel covers the two cases where the
// session cannot answer: its agent is not known yet, and the pick is for
// a slot the session does not own.
func TestTheListFallsBackToTheGlobalModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		active *workspace.ActiveAgent
	}{
		{
			name:   "the agent is not known yet",
			active: nil,
		},
		{
			name: "the session owns a different slot",
			active: &workspace.ActiveAgent{
				Slot:     config.SlotChore,
				ModelCfg: config.SelectedModel{Provider: testProviderID, Model: sessionModelID},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws := &configWorkspace{cfg: modelsConfig(t)}
			m := newModelsDialog(t, ws, tt.active)
			m.SetProviders(catalogFor())

			require.Equal(t, globalModelID, selectedModelID(t, m))
		})
	}
}

// TestTheRecentEntryIsHighlightedForTheSession pins the recents group,
// which decides the selection on its own path.
func TestTheRecentEntryIsHighlightedForTheSession(t *testing.T) {
	t.Parallel()

	recent := config.SelectedModel{Provider: testProviderID, Model: sessionModelID}
	ws := &configWorkspace{cfg: modelsConfig(t, recent)}
	active := &workspace.ActiveAgent{
		Slot:     config.SlotMain,
		ModelCfg: config.SelectedModel{Provider: testProviderID, Model: sessionModelID},
	}
	m := newModelsDialog(t, ws, active)
	m.SetProviders(catalogFor())

	require.Equal(t, sessionModelID, selectedModelID(t, m))
	require.Zero(t, ws.writes, "a resolvable recent must not be pruned")
}

// TestOpeningTheDialogLoadsNoCatalog is B5. Listing providers can block
// for as long as a catalog refresh takes, and construction runs on the
// Update goroutine.
func TestOpeningTheDialogLoadsNoCatalog(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newModelsDialog(t, ws, nil)

	require.Empty(t, m.providers, "the catalog must arrive through InitialCmd")
	require.False(t, m.catalogLoaded)
	require.NotNil(t, m.InitialCmd(), "the dialog must ask for the catalog")
	// The configured provider still shows, so the dialog is usable
	// before the catalog lands.
	require.Positive(t, m.list.Len())
}

// TestRecentsSurviveAnUnloadedCatalog is the safety half of B5: with no
// catalog yet, nothing resolves, and pruning against that would wipe
// every recent entry the user has.
func TestRecentsSurviveAnUnloadedCatalog(t *testing.T) {
	t.Parallel()

	recents := []config.SelectedModel{
		{Provider: testProviderID, Model: sessionModelID},
		{Provider: "gone", Model: "vanished"},
	}
	ws := &configWorkspace{cfg: modelsConfig(t, recents...)}
	m := newModelsDialog(t, ws, nil)

	require.Nil(t, m.staleRecents, "recents must not be judged against an empty catalog")
	require.Nil(t, m.pruneRecentsCmd())
}

// TestEveryRecentGoingStaleStillClearsTheConfig is B9. A fully stale
// list is the one case where every entry needs removing; it used to be
// indistinguishable from "nothing changed", so the dead list stayed in
// the config file forever.
func TestEveryRecentGoingStaleStillClearsTheConfig(t *testing.T) {
	t.Parallel()

	recents := []config.SelectedModel{
		{Provider: "gone", Model: "vanished"},
		{Provider: "also-gone", Model: "also-vanished"},
	}
	ws := &configWorkspace{cfg: modelsConfig(t, recents...)}
	m := newModelsDialog(t, ws, nil)

	cmd := m.SetProviders(catalogFor())
	require.NotNil(t, cmd, "a fully stale list must still be pruned")
	require.Equal(t, recents, m.staleRecents, "every entry is stale")

	cmd()
	require.Equal(t, recents, ws.pruned)
}

// TestAnUnresolvableRecentIsPrunedOnceTheCatalogLands pins that pruning
// still happens — just not before there is something to judge against.
func TestAnUnresolvableRecentIsPrunedOnceTheCatalogLands(t *testing.T) {
	t.Parallel()

	recents := []config.SelectedModel{
		{Provider: testProviderID, Model: sessionModelID},
		{Provider: "gone", Model: "vanished"},
	}
	ws := &configWorkspace{cfg: modelsConfig(t, recents...)}
	m := newModelsDialog(t, ws, nil)

	cmd := m.SetProviders(catalogFor())
	require.NotNil(t, cmd, "the unresolvable entry must be pruned")
	cmd()
	require.Equal(t, []config.SelectedModel{{Provider: "gone", Model: "vanished"}}, ws.pruned,
		"only the dead entry may be named, never the surviving list")
}

// TestPruningNamesTheDeadEntriesNotTheSurvivors is B6. Pruning used to
// overwrite the whole recent-models field with a list computed when the
// catalog landed, so a model picked in that window was erased. Naming
// the dead entries lets the store filter the live list instead.
func TestPruningNamesTheDeadEntriesNotTheSurvivors(t *testing.T) {
	t.Parallel()

	dead := config.SelectedModel{Provider: "gone", Model: "vanished"}
	recents := []config.SelectedModel{
		{Provider: testProviderID, Model: sessionModelID},
		dead,
	}
	ws := &configWorkspace{cfg: modelsConfig(t, recents...)}
	m := newModelsDialog(t, ws, nil)

	cmd := m.SetProviders(catalogFor())
	require.NotNil(t, cmd)
	cmd()

	require.Equal(t, []config.SelectedModel{dead}, ws.pruned)
	require.Zero(t, ws.writes, "pruning must not go through a whole-field write")
}

// visibleModelIDs reports the model rows currently in the list, skipping
// group headers and spacers.
func visibleModelIDs(m *Models) []string {
	var ids []string
	for i := range m.list.List.Len() {
		if item, ok := m.list.ItemAt(i).(*ModelItem); ok {
			ids = append(ids, item.model.ID)
		}
	}
	return ids
}

// TestTheCatalogLandingKeepsTheTypedFilter is B5. The catalog is fetched
// asynchronously, so it can arrive after the user has started typing;
// installing it used to rebuild the list unfiltered, flashing every
// model back on screen until the next keystroke.
func TestTheCatalogLandingKeepsTheTypedFilter(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newModelsDialog(t, ws, nil)

	m.list.SetFilter("session")
	require.Equal(t, []string{sessionModelID}, visibleModelIDs(m),
		"the filter must narrow the list before the catalog lands")

	m.SetProviders(catalogFor())
	require.Equal(t, []string{sessionModelID}, visibleModelIDs(m),
		"the catalog landing must not drop the typed filter")
}

const (
	otherProviderID = "globex"
	otherModelID    = "globex-model"
)

// twoProviderCatalog adds a second provider, so restricting the list has
// something to exclude.
func twoProviderCatalog() []catwalk.Provider {
	return append(catalogFor(), catwalk.Provider{
		ID:     otherProviderID,
		Name:   "Globex",
		Models: []catwalk.Model{{ID: otherModelID, Name: "Globex Model"}},
	})
}

// TestRestrictToProviderHidesEveryOtherProvider pins the third
// onboarding step: the provider is already settled by then, so listing
// the others invites the user to contradict their own earlier choice.
func TestRestrictToProviderHidesEveryOtherProvider(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newModelsDialog(t, ws, nil)
	m.SetProviders(twoProviderCatalog())

	require.Equal(t, []string{globalModelID, sessionModelID, otherModelID}, visibleModelIDs(m),
		"an unrestricted list keeps every provider")

	m.RestrictToProvider(testProviderID)
	require.Equal(t, []string{globalModelID, sessionModelID}, visibleModelIDs(m))
}

// TestRestrictToProviderDropsRecents covers the leak the group would
// otherwise open: recents span providers, so they smuggle rows past the
// restriction.
func TestRestrictToProviderDropsRecents(t *testing.T) {
	t.Parallel()

	recent := config.SelectedModel{Provider: otherProviderID, Model: otherModelID}
	ws := &configWorkspace{cfg: modelsConfig(t, recent)}
	m := newModelsDialog(t, ws, nil)
	m.SetProviders(twoProviderCatalog())

	require.Contains(t, visibleModelIDs(m), otherModelID)

	m.RestrictToProvider(testProviderID)
	require.Equal(t, []string{globalModelID, sessionModelID}, visibleModelIDs(m),
		"a recent from another provider must not survive the restriction")
}

// TestARestrictedListNeverPrunesRecents follows the onboarding order:
// the restriction is applied before the catalog lands. Judging recents
// against a one-provider view would call every other provider's entry
// dead and delete it.
func TestARestrictedListNeverPrunesRecents(t *testing.T) {
	t.Parallel()

	recent := config.SelectedModel{Provider: otherProviderID, Model: otherModelID}
	ws := &configWorkspace{cfg: modelsConfig(t, recent)}
	m := newModelsDialog(t, ws, nil)
	m.RestrictToProvider(testProviderID)

	require.Nil(t, m.SetProviders(twoProviderCatalog()), "a restricted view must not prune")
	require.Nil(t, m.staleRecents)
	require.Empty(t, ws.pruned)
}

const typedModelID = "acme-experimental-2"

// TestATypedModelIDBecomesSelectable is the whole point of the custom
// entry: the catalog is never complete, and a filter that matches
// nothing used to leave the user with no selection and no way forward.
func TestATypedModelIDBecomesSelectable(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newOnboardingModelsDialog(t, ws)

	typeQuery(m, typedModelID)

	require.Equal(t, []string{typedModelID}, visibleModelIDs(m))
	require.Equal(t, typedModelID, selectedModelID(t, m))
}

// TestConfirmingATypedModelCarriesItsID checks the pick that leaves the
// dialog, not just the row that is drawn.
func TestConfirmingATypedModelCarriesItsID(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newOnboardingModelsDialog(t, ws)

	typeQuery(m, typedModelID)
	action, ok := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}).(ActionSelectModel)

	require.True(t, ok, "confirming a typed model must emit a pick")
	require.Equal(t, typedModelID, action.Model.Model)
	require.Equal(t, testProviderID, action.Model.Provider)
}

// TestATypedQueryThatMatchesIsNotOfferedAsCustom keeps a real catalog
// row from being shadowed by an identical hand-typed one.
func TestATypedQueryThatMatchesIsNotOfferedAsCustom(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newOnboardingModelsDialog(t, ws)

	typeQuery(m, "Global Model")

	require.Equal(t, []string{globalModelID}, visibleModelIDs(m))
	require.Nil(t, m.list.Custom())
}

// TestAnExactModelIDStillReachesTheModel covers the seam between the two
// mechanisms: the list filters on model names, so typing an exact ID
// matches no row. The custom entry is what keeps that from being a dead
// end, and the pick it emits carries the real ID.
func TestAnExactModelIDStillReachesTheModel(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newOnboardingModelsDialog(t, ws)

	typeQuery(m, globalModelID)

	require.Equal(t, []string{globalModelID}, visibleModelIDs(m))
	action, ok := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}).(ActionSelectModel)
	require.True(t, ok)
	require.Equal(t, globalModelID, action.Model.Model)
}

// TestTheCustomEntryIsOnboardingOnly guards the reason it is gated:
// nothing outside onboarding registers the model under its provider, and
// an unregistered model does not survive a config reload.
func TestTheCustomEntryIsOnboardingOnly(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newModelsDialog(t, ws, nil)
	m.SetProviders(catalogFor())
	m.RestrictToProvider(testProviderID)

	typeQuery(m, typedModelID)

	require.Nil(t, m.list.Custom())
	require.Empty(t, visibleModelIDs(m))
}

// TestTheCatalogLandingRetiresACustomEntry covers the async seam: the
// catalog arrives after the user has typed, and may well offer the very
// model the custom entry was standing in for.
func TestTheCatalogLandingRetiresACustomEntry(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	s := styles.CharmtonePantera()
	m := NewModels(&common.Common{Workspace: ws, Styles: &s}, true, nil)
	m.RestrictToProvider(testProviderID)

	typeQuery(m, "Late Model")
	require.NotNil(t, m.list.Custom(), "an unmatched query stands in until the catalog lands")

	m.SetProviders([]catwalk.Provider{{
		ID:     testProviderID,
		Name:   "Acme",
		Models: []catwalk.Model{{ID: otherModelID, Name: "Late Model"}},
	}})

	require.Nil(t, m.list.Custom(), "the catalog entry supersedes the stand-in")
	require.Equal(t, []string{otherModelID}, visibleModelIDs(m))
}

// TestTheCustomEntryActuallyDraws closes the gap between "the item is in
// the list" and "the user can see it": the custom row is appended after
// every group, where the list's height and scroll geometry decide
// whether it lands on screen at all.
func TestTheCustomEntryActuallyDraws(t *testing.T) {
	t.Parallel()

	ws := &configWorkspace{cfg: modelsConfig(t)}
	m := newOnboardingModelsDialog(t, ws)
	typeQuery(m, typedModelID)

	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	m.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, typedModelID, "the typed model must be visible")
	require.Contains(t, view, customModelGroupTitle, "the custom group header must be visible")
}
