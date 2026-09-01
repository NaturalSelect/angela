package config

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestCatwalkSync_Init(t *testing.T) {
	t.Parallel()

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	path := "/tmp/test.json"

	syncer.Init(client, path, true)

	require.True(t, syncer.init.Load())
	require.Equal(t, client, syncer.client)
	require.Equal(t, path, syncer.cache.path)
	require.True(t, syncer.autoupdate)
}

func TestCatwalkSync_GetPanicIfNotInit(t *testing.T) {
	t.Parallel()

	syncer := &catwalkSync{}
	require.Panics(t, func() {
		_, _ = syncer.Get(t.Context())
	})
}

func TestCatwalkSync_GetWithAutoUpdateDisabled(t *testing.T) {
	t.Parallel()

	syncer := &catwalkSync{}
	// No GetProviders expectation is set up: gomock fails the test if
	// disabling autoupdate doesn't skip the network call.
	client := NewMockCatwalkClient(gomock.NewController(t))
	path := t.TempDir() + "/providers.json"

	syncer.Init(client, path, false)

	providers, err := syncer.Get(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, providers)

	// Should return embedded providers.
	for _, p := range providers {
		require.NotEqual(t, "should-not-be-used", p.Name)
	}
}

func TestCatwalkSync_GetFreshProviders(t *testing.T) {
	t.Parallel()

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	client.EXPECT().GetProviders(gomock.Any(), gomock.Any()).
		Return([]catwalk.Provider{{Name: "Fresh Provider", ID: "fresh"}}, nil).Times(1)
	path := t.TempDir() + "/providers.json"

	syncer.Init(client, path, true)

	providers, err := syncer.Get(t.Context())
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Equal(t, "Fresh Provider", providers[0].Name)

	// Verify cache was written.
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.False(t, fileInfo.IsDir())
}

func TestCatwalkSync_GetNotModifiedUsesCached(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := tmpDir + "/providers.json"

	// Create cache file.
	cachedProviders := []catwalk.Provider{
		{Name: "Cached Provider", ID: "cached"},
	}
	data, err := json.Marshal(cachedProviders)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	client.EXPECT().GetProviders(gomock.Any(), gomock.Any()).
		Return(nil, catwalk.ErrNotModified).Times(1)

	syncer.Init(client, path, true)

	providers, err := syncer.Get(t.Context())
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Equal(t, "Cached Provider", providers[0].Name)
}

func TestCatwalkSync_GetEmptyResultFallbackToCached(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := tmpDir + "/providers.json"

	// Create cache file.
	cachedProviders := []catwalk.Provider{
		{Name: "Cached Provider", ID: "cached"},
	}
	data, err := json.Marshal(cachedProviders)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	client.EXPECT().GetProviders(gomock.Any(), gomock.Any()).
		Return([]catwalk.Provider{}, nil).Times(1) // Empty result.

	syncer.Init(client, path, true)

	providers, err := syncer.Get(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty providers list from catwalk")
	require.Len(t, providers, 1)
	require.Equal(t, "Cached Provider", providers[0].Name)
}

func TestCatwalkSync_GetEmptyCacheDefaultsToEmbedded(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := tmpDir + "/providers.json"

	// Create empty cache file.
	emptyProviders := []catwalk.Provider{}
	data, err := json.Marshal(emptyProviders)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	client.EXPECT().GetProviders(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("network error")).Times(1)

	syncer.Init(client, path, true)

	providers, err := syncer.Get(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, providers, "Should fall back to embedded providers")

	// Verify it's embedded providers by checking we have multiple common ones.
	require.Greater(t, len(providers), 5)
}

func TestCatwalkSync_GetClientError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := tmpDir + "/providers.json"

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	client.EXPECT().GetProviders(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("network error")).Times(1)

	syncer.Init(client, path, true)

	providers, err := syncer.Get(t.Context())
	require.NoError(t, err) // Should fall back to embedded.
	require.NotEmpty(t, providers)
}

func TestCatwalkSync_GetCalledMultipleTimesUsesOnce(t *testing.T) {
	t.Parallel()

	syncer := &catwalkSync{}
	client := NewMockCatwalkClient(gomock.NewController(t))
	// Times(1) enforces the sync.Once guarantee: however many times
	// Get is called, the client is only reached once.
	client.EXPECT().GetProviders(gomock.Any(), gomock.Any()).
		Return([]catwalk.Provider{{Name: "Provider", ID: "test"}}, nil).Times(1)
	path := t.TempDir() + "/providers.json"

	syncer.Init(client, path, true)

	// Call Get multiple times.
	providers1, err1 := syncer.Get(t.Context())
	require.NoError(t, err1)
	require.Len(t, providers1, 1)

	providers2, err2 := syncer.Get(t.Context())
	require.NoError(t, err2)
	require.Len(t, providers2, 1)
}
