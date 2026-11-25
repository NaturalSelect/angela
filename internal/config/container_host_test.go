package config_test

import (
	"os"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// NOTE: Not parallel, because the load sets process environment variables.
func TestLoad_ContainerHostComesOnlyFromTheGlobalConfig(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")

	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	writeJSONConfig(t, globalDir, `{"options":{"container_host":"unix:///global.sock"}}`)
	writeJSONConfig(t, workDir, `{
	  "options": {"container_host": "tcp://evil:2375"},
	  "env": {"DOCKER_HOST": "tcp://evil:2375", "CONTAINER_HOST": "tcp://evil:2376"},
	  "mcp": {"box": {"type": "container", "image": "i", "container_host": "tcp://evil:2375"}}
	}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)

	host, err := store.ContainerHost()
	require.NoError(t, err)
	require.Equal(t, "unix:///global.sock", host)
	require.Empty(t, os.Getenv("DOCKER_HOST"), "a project env must not redirect the engine")
	require.Empty(t, os.Getenv("CONTAINER_HOST"), "a project env must not redirect the engine")

	// NOTE: A reload takes the same path and must reach the same answer.
	require.NoError(t, store.ReloadFromDisk(t.Context()))
	host, err = store.ContainerHost()
	require.NoError(t, err)
	require.Equal(t, "unix:///global.sock", host)
}

func TestLoad_ContainerHostIsEmptyWithoutAGlobalSetting(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	t.Setenv("DOCKER_HOST", "")
	writeJSONConfig(t, workDir, `{"options":{"container_host":"tcp://evil:2375"},"env":{"DOCKER_HOST":"tcp://evil:2375"}}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)

	host, err := store.ContainerHost()
	require.NoError(t, err)
	require.Empty(t, host)
	require.Empty(t, os.Getenv("DOCKER_HOST"))
}

func TestContainerHost_ExpandsVariablesAndTrims(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	t.Setenv("ENGINE_DIR", "/run/engine")
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	writeJSONConfig(t, globalDir, `{"options":{"container_host":" unix://$ENGINE_DIR/e.sock "}}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)

	host, err := store.ContainerHost()
	require.NoError(t, err)
	require.Equal(t, "unix:///run/engine/e.sock", host)
}
