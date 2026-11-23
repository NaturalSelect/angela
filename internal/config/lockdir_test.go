package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLockDir(t *testing.T) {
	t.Parallel()

	t.Run("beside the global config and shared with the refresh lock", func(t *testing.T) {
		t.Parallel()
		global := filepath.Join(t.TempDir(), "angela.json")
		store := &ConfigStore{globalDataPath: global}

		dir := store.LockDir()
		require.Equal(t, filepath.Join(filepath.Dir(global), "locks"), dir)
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.True(t, info.IsDir())
		require.Equal(t, dir, filepath.Dir(store.refreshLockPath("p")))
	})

	t.Run("empty without a global config path", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, (&ConfigStore{}).LockDir())
	})
}
