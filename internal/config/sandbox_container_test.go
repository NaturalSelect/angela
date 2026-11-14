package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConfigStore_SandboxContainerPermissionDisabled covers the
// precedence of the --no-sandbox-container-permission override over
// options.no_sandbox_container_permission, and that a Config with a nil
// Options does not panic.
func TestConfigStore_SandboxContainerPermissionDisabled(t *testing.T) {
	t.Parallel()

	t.Run("enabled by default", func(t *testing.T) {
		t.Parallel()
		store := &ConfigStore{config: &Config{Options: &Options{}}}
		require.False(t, store.SandboxContainerPermissionDisabled())
	})

	t.Run("persisted true disables", func(t *testing.T) {
		t.Parallel()
		store := &ConfigStore{config: &Config{Options: &Options{NoSandboxContainerPermission: true}}}
		require.True(t, store.SandboxContainerPermissionDisabled())
	})

	t.Run("override true beats persisted false", func(t *testing.T) {
		t.Parallel()
		store := &ConfigStore{config: &Config{Options: &Options{}}}
		store.Overrides().NoSandboxContainerPermission = new(true)
		require.True(t, store.SandboxContainerPermissionDisabled())
	})

	t.Run("override false beats persisted true", func(t *testing.T) {
		t.Parallel()
		store := &ConfigStore{config: &Config{Options: &Options{NoSandboxContainerPermission: true}}}
		store.Overrides().NoSandboxContainerPermission = new(false)
		require.False(t, store.SandboxContainerPermissionDisabled())
	})

	t.Run("nil Options does not panic", func(t *testing.T) {
		t.Parallel()
		store := &ConfigStore{config: &Config{}}
		require.False(t, store.SandboxContainerPermissionDisabled())
	})
}
