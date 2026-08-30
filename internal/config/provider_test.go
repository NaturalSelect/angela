package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func resetProviderState() {
	providerOnce = sync.Once{}
	providerList = nil
	providerErr = nil
	catwalkSyncer = &catwalkSync{}
}

func TestProviders_Integration_AutoUpdateDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// Use a test-specific instance to avoid global state interference.
	testCatwalkSyncer := &catwalkSync{}

	originalCatwalSyncer := catwalkSyncer
	defer func() {
		catwalkSyncer = originalCatwalSyncer
	}()

	catwalkSyncer = testCatwalkSyncer

	resetProviderState()
	defer resetProviderState()

	cfg := &Config{
		Options: &Options{
			DisableProviderAutoUpdate: true,
		},
	}

	providers, err := Providers(cfg)
	require.NoError(t, err)
	require.NotNil(t, providers)
	require.Greater(t, len(providers), 5, "Expected embedded providers")
}

func TestCache_StoreAndGet(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cachePath := tmpDir + "/test.json"

	cache := newCache[[]catwalk.Provider](cachePath)

	providers := []catwalk.Provider{
		{Name: "Provider1", ID: "p1"},
		{Name: "Provider2", ID: "p2"},
	}

	// Store.
	err := cache.Store(providers)
	require.NoError(t, err)

	// Get.
	result, etag, err := cache.Get()
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "Provider1", result[0].Name)
	require.NotEmpty(t, etag)
}

func TestCache_GetNonExistent(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cachePath := tmpDir + "/nonexistent.json"

	cache := newCache[[]catwalk.Provider](cachePath)

	_, _, err := cache.Get()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read provider cache file")
}

func TestCache_GetInvalidJSON(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cachePath := tmpDir + "/invalid.json"

	require.NoError(t, os.WriteFile(cachePath, []byte("invalid json"), 0o644))

	cache := newCache[[]catwalk.Provider](cachePath)

	_, _, err := cache.Get()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal provider data from cache")
}

func TestCachePathFor(t *testing.T) {
	tests := []struct {
		name        string
		xdgDataHome string
		expected    string
	}{
		{
			name:        "with XDG_DATA_HOME",
			xdgDataHome: "/custom/data",
			expected:    "/custom/data/angela/providers.json",
		},
		{
			name:        "without XDG_DATA_HOME",
			xdgDataHome: "",
			expected:    "", // Will use platform-specific default.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.xdgDataHome != "" {
				t.Setenv("XDG_DATA_HOME", tt.xdgDataHome)
			} else {
				t.Setenv("XDG_DATA_HOME", "")
			}

			result := cachePathFor("providers")
			if tt.expected != "" {
				require.Equal(t, tt.expected, filepath.ToSlash(result))
			} else {
				require.Contains(t, result, "angela")
				require.Contains(t, result, "providers.json")
			}
		})
	}
}

// TestProviders_KeepsCatalogWhenCachingFails covers the case where the
// provider list was fetched successfully but could not be written to the
// on-disk cache: Providers must keep the fetched catalog instead of
// discarding it.
func TestProviders_KeepsCatalogWhenCachingFails(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// A file where a directory needs to be, so every cache write fails.
	blocked := filepath.Join(tmpDir, "blocked")
	require.NoError(t, os.WriteFile(blocked, []byte("block"), 0o644))
	unwritable := filepath.Join(blocked, "subdir", "cache.json")

	resetProviderState()
	defer resetProviderState()

	// Prime the syncer with a mock client so Providers reuses the memoized
	// outcome instead of reaching the network.
	catwalkSyncer.Init(&mockCatwalkClient{
		providers: []catwalk.Provider{{Name: "Provider1", ID: "p1"}},
	}, unwritable, true)

	catwalkProviders, catwalkErr := catwalkSyncer.Get(t.Context())
	require.Error(t, catwalkErr, "cache write should fail")
	require.NotEmpty(t, catwalkProviders, "syncer still returns a usable catalog")

	providers, err := Providers(&Config{Options: &Options{}})

	// The failure is reported, but as a warning alongside a usable catalog.
	require.Error(t, err)
	require.Len(t, providers, 1)
	require.Equal(t, catwalk.InferenceProvider("p1"), providers[0].ID)
}

// TestProviders_HonorsDisableDefaultProviders makes sure disabling default
// providers returns an empty catalog instead of falling back to embedded
// providers.
func TestProviders_HonorsDisableDefaultProviders(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	resetProviderState()
	defer resetProviderState()

	providers, err := Providers(&Config{
		Options: &Options{DisableDefaultProviders: true},
	})
	require.NoError(t, err)
	require.Empty(t, providers)
}

// TestCacheStore_ReplacesFileInsteadOfRewritingIt guards the property that
// several Angela instances depend on: the provider cache is swapped into place
// as a finished file, never truncated and refilled underneath a reader that is
// already reading it. A reader that loses that race cannot parse the catalog
// and silently falls back to the bundled copy.
func TestCacheStore_ReplacesFileInsteadOfRewritingIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	c := newCache[[]catwalk.Provider](path)

	require.NoError(t, c.Store([]catwalk.Provider{{ID: "first", Name: "First"}}))
	before, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, c.Store([]catwalk.Provider{{ID: "second", Name: "Second"}}))
	after, err := os.Stat(path)
	require.NoError(t, err)

	// os.Stat on Windows resolves file identity lazily by reopening the path,
	// so both stats describe whichever file the path points at by the time
	// they are compared and SameFile cannot observe the replacement. The
	// write path is shared, so asserting this on the other platforms covers
	// it. The checks below still run everywhere.
	if runtime.GOOS != "windows" {
		require.False(t, os.SameFile(before, after),
			"the cache should be replaced by a rename, not rewritten in place")
	}

	// The new contents are complete and no temporary files are left behind.
	got, _, err := c.Get()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, catwalk.InferenceProvider("second"), got[0].ID)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the cache file should remain")
	require.Equal(t, "providers.json", entries[0].Name())
}
