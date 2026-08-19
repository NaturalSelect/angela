package dialog

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
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

func (w *configWorkspace) PruneRecentModels(_ config.Scope, _ config.ModelConfigName, stale []config.SelectedModel) error {
	w.pruned = stale
	return nil
}

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
		Models: []catwalk.Model{
			{ID: globalModelID, Name: "Global Model"},
			{ID: sessionModelID, Name: "Session Model"},
		},
	})

	cfg := &config.Config{
		Providers: providers,
		Models: map[config.ModelConfigName]config.SelectedModel{
			config.ModelMain: {Provider: testProviderID, Model: globalModelID},
		},
	}
	if len(recents) > 0 {
		cfg.RecentModels = map[config.ModelConfigName][]config.SelectedModel{
			config.ModelMain: recents,
		}
	}
	return cfg
}

func newModelsDialog(t *testing.T, ws *configWorkspace, active *workspace.ActiveAgent) *Models {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewModels(&common.Common{Workspace: ws, Styles: &s}, false, active)
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
		ModelName: config.ModelMain,
		ModelCfg:  config.SelectedModel{Provider: testProviderID, Model: sessionModelID},
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
				ModelName: config.ModelChore,
				ModelCfg:  config.SelectedModel{Provider: testProviderID, Model: sessionModelID},
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
		ModelName: config.ModelMain,
		ModelCfg:  config.SelectedModel{Provider: testProviderID, Model: sessionModelID},
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
