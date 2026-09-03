package config

import (
	"encoding/json"
	"os"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// readProviderModels reads one provider's model list back out of the
// config file.
func readProviderModels(t *testing.T, path, providerID string) []catwalk.Model {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)

	var out struct {
		Providers map[string]struct {
			Models []catwalk.Model `json:"models"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(b, &out))
	return out.Providers[providerID].Models
}

func newUpsertStore(t *testing.T, providers ...string) *ConfigStore {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{}
	cfg.setDefaults(dir, "")
	for _, id := range providers {
		cfg.Providers.Set(id, ProviderConfig{
			ID:      id,
			BaseURL: "http://localhost",
		})
	}
	return testStoreWithPath(cfg, dir)
}

func TestUpsertProviderModel_AddsAModelTheCatalogNeverListed(t *testing.T) {
	t.Parallel()

	store := newUpsertStore(t, "acme")
	model := catwalk.Model{
		ID:               "typed-model",
		Name:             "typed-model",
		ContextWindow:    1048576,
		DefaultMaxTokens: 32768,
	}
	require.NoError(t, store.UpsertProviderModel(ScopeGlobal, "acme", ProviderModel{Model: model}))

	models := readProviderModels(t, store.globalDataPath, "acme")
	require.Equal(t, []catwalk.Model{model}, models)
}

func TestUpsertProviderModel_ReplacesTheEntryWithTheSameID(t *testing.T) {
	t.Parallel()

	store := newUpsertStore(t, "acme")
	require.NoError(t, store.UpsertProviderModel(ScopeGlobal, "acme",
		ProviderModel{Model: catwalk.Model{ID: "m1", ContextWindow: 1000}}))
	require.NoError(t, store.UpsertProviderModel(ScopeGlobal, "acme",
		ProviderModel{Model: catwalk.Model{ID: "m2", ContextWindow: 2000}}))
	require.NoError(t, store.UpsertProviderModel(ScopeGlobal, "acme",
		ProviderModel{Model: catwalk.Model{ID: "m1", ContextWindow: 9000}}))

	models := readProviderModels(t, store.globalDataPath, "acme")
	require.Len(t, models, 2, "re-registering a model must not append a duplicate")
	require.Equal(t, "m1", models[0].ID)
	require.Equal(t, int64(9000), models[0].ContextWindow)
	require.Equal(t, "m2", models[1].ID)
}

// TestUpsertProviderModel_LeavesTheCatalogOutOfTheConfigFile is the one
// that matters. The loader merges the whole catalog into the in-memory
// ProviderConfig.Models; writing that back would freeze every catalog
// model into the user's config and stop them tracking catalog updates.
func TestUpsertProviderModel_LeavesTheCatalogOutOfTheConfigFile(t *testing.T) {
	t.Parallel()

	store := newUpsertStore(t, "acme")
	// A store whose in-memory provider carries a full catalog, as it
	// does after a real load.
	cfg := store.Config()
	cfg.Providers.Set("acme", ProviderConfig{
		ID:      "acme",
		BaseURL: "http://localhost",
		Models: []ProviderModel{
			{Model: catwalk.Model{ID: "catalog-a"}}, {Model: catwalk.Model{ID: "catalog-b"}}, {Model: catwalk.Model{ID: "catalog-c"}},
		},
	})

	require.NoError(t, store.UpsertProviderModel(ScopeGlobal, "acme",
		ProviderModel{Model: catwalk.Model{ID: "typed-model"}}))

	models := readProviderModels(t, store.globalDataPath, "acme")
	require.Equal(t, []catwalk.Model{{ID: "typed-model"}}, models,
		"only the registered model belongs in the config file")
}

func TestUpsertProviderModel_RejectsEmptyIdentifiers(t *testing.T) {
	t.Parallel()

	store := newUpsertStore(t, "acme")
	require.Error(t, store.UpsertProviderModel(ScopeGlobal, "", ProviderModel{Model: catwalk.Model{ID: "m"}}))
	require.Error(t, store.UpsertProviderModel(ScopeGlobal, "acme", ProviderModel{}))
}

// TestUpsertProviderModel_RejectsUnknownProvider verifies that writing a
// model for a provider that is neither configured nor in the catalog
// returns an error instead of leaving an unusable fragment.
func TestUpsertProviderModel_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	store := newUpsertStore(t)
	err := store.UpsertProviderModel(ScopeGlobal, "no-such-provider", ProviderModel{Model: catwalk.Model{ID: "m"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not configured or known")
}

// TestUpsertProviderModel_EscapesTheProviderKey keeps a provider id with
// a dot in it from addressing a nested path instead of its own key.
func TestUpsertProviderModel_EscapesTheProviderKey(t *testing.T) {
	t.Parallel()

	store := newUpsertStore(t, "my.provider")
	require.NoError(t, store.UpsertProviderModel(ScopeGlobal, "my.provider",
		ProviderModel{Model: catwalk.Model{ID: "m1"}}))

	models := readProviderModels(t, store.globalDataPath, "my.provider")
	require.Equal(t, []catwalk.Model{{ID: "m1"}}, models)
}
