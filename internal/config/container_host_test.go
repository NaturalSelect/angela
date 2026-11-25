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

// NOTE: Windows environment names ignore case, and the loader matches field names that way too, so each spelling here would reach the process environment. Not parallel, because the load sets environment variables.
func TestLoad_ProjectEnvCannotSetTheEngineUnderAnotherCase(t *testing.T) {
	spellings := map[string]string{
		"lower-case name":    `{"env":{"docker_host":"tcp://attacker:2375","container_host":"tcp://attacker:2376"}}`,
		"mixed-case name":    `{"env":{"Docker_Host":"tcp://attacker:2375","Container_Host":"tcp://attacker:2376"}}`,
		"upper-case env key": `{"ENV":{"DOCKER_HOST":"tcp://attacker:2375","CONTAINER_HOST":"tcp://attacker:2376"}}`,
	}
	for name, doc := range spellings {
		t.Run(name, func(t *testing.T) {
			workDir, dataDir := isolateReloadEnv(t)
			for _, key := range []string{"DOCKER_HOST", "CONTAINER_HOST", "docker_host", "container_host", "Docker_Host", "Container_Host"} {
				t.Setenv(key, "")
				require.NoError(t, os.Unsetenv(key))
			}
			writeJSONConfig(t, workDir, doc)

			store, err := config.Load(workDir, dataDir, false)
			require.NoError(t, err)
			requireEngineEnvEmpty(t)

			require.NoError(t, store.ReloadFromDisk(t.Context()))
			requireEngineEnvEmpty(t)
		})
	}
}

func requireEngineEnvEmpty(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DOCKER_HOST", "CONTAINER_HOST", "docker_host", "container_host", "Docker_Host", "Container_Host"} {
		require.Empty(t, os.Getenv(key), "a project env must not set %s", key)
	}
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
