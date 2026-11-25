package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// NOTE: The engine address comes from the environment alone, so what stands between a project config and the engine is the env map. Not parallel, because the load sets process environment variables.
func TestLoad_EngineEnvComesOnlyFromTheTrustedLayers(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")

	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	writeJSONConfig(t, globalDir, `{"env":{"DOCKER_HOST":"unix:///global.sock"}}`)
	writeJSONConfig(t, workDir, `{"env":{"DOCKER_HOST":"tcp://evil:2375","CONTAINER_HOST":"tcp://evil:2376"}}`)

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	require.Equal(t, "unix:///global.sock", os.Getenv("DOCKER_HOST"), "the global config may name the engine")
	require.Empty(t, os.Getenv("CONTAINER_HOST"), "a project env must not name the engine")

	// NOTE: A reload takes the same path and must reach the same answer.
	require.NoError(t, store.ReloadFromDisk(t.Context()))
	require.Equal(t, "unix:///global.sock", os.Getenv("DOCKER_HOST"))
	require.Empty(t, os.Getenv("CONTAINER_HOST"))
}

func TestLoad_ProjectEnvLeavesTheEngineUnsetWithoutAGlobalSetting(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	t.Setenv("DOCKER_HOST", "")
	require.NoError(t, os.Unsetenv("DOCKER_HOST"))
	writeJSONConfig(t, workDir, `{"env":{"DOCKER_HOST":"tcp://evil:2375"}}`)

	_, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	_, set := os.LookupEnv("DOCKER_HOST")
	require.False(t, set)
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

// NOTE: The bind mount check lists the engine sockets from these variables, so a project that could move them could hide a socket from it. Not parallel, because the load sets environment variables.
func TestLoad_ProjectEnvCannotMoveTheEngineLocations(t *testing.T) {
	workDir, dataDir := isolateReloadEnv(t)
	userHome := os.Getenv("HOME")
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("KEEP", "")
	writeJSONConfig(t, workDir, `{"env":{
	  "XDG_RUNTIME_DIR": "/nonexistent",
	  "xdg_runtime_dir": "/nonexistent",
	  "HOME": "/nonexistent",
	  "USERPROFILE": "/nonexistent",
	  "KEEP": "1"
	}}`)

	requireUnmoved := func(t *testing.T) {
		t.Helper()
		require.Equal(t, runtimeDir, os.Getenv("XDG_RUNTIME_DIR"))
		require.Equal(t, userHome, os.Getenv("HOME"))
		require.Equal(t, userHome, os.Getenv("USERPROFILE"))
		require.Equal(t, "1", os.Getenv("KEEP"), "other variables still apply")
	}

	store, err := config.Load(workDir, dataDir, false)
	require.NoError(t, err)
	requireUnmoved(t)

	require.NoError(t, store.ReloadFromDisk(t.Context()))
	requireUnmoved(t)
}

// NOTE: A reload finds the global config through these variables, and anything found there is trusted, so a project that could move them could hand itself the global layer. The global env map shows which file a reload read. Not parallel, because the load sets environment variables.
func TestLoad_ProjectEnvCannotMoveTheGlobalConfig(t *testing.T) {
	levers := map[string]func(t *testing.T, fake string) string{
		"ANGELA_GLOBAL_CONFIG": func(t *testing.T, fake string) string {
			writeJSONConfig(t, fake, `{"env":{"PLANTED":"yes"}}`)
			return fmt.Sprintf(`{"env":{"ANGELA_GLOBAL_CONFIG":%q}}`, fake)
		},
		"XDG_CONFIG_HOME": func(t *testing.T, fake string) string {
			writeJSONConfig(t, filepath.Join(fake, "angela"), `{"env":{"PLANTED":"yes"}}`)
			return fmt.Sprintf(`{"env":{"XDG_CONFIG_HOME":%q}}`, fake)
		},
	}
	for name, plant := range levers {
		t.Run(name, func(t *testing.T) {
			workDir, dataDir := isolateReloadEnv(t)
			t.Setenv("REAL", "")
			t.Setenv("PLANTED", "")

			realConfig := t.TempDir()
			if name == "XDG_CONFIG_HOME" {
				t.Setenv("ANGELA_GLOBAL_CONFIG", "")
				require.NoError(t, os.Unsetenv("ANGELA_GLOBAL_CONFIG"))
				t.Setenv("XDG_CONFIG_HOME", realConfig)
				writeJSONConfig(t, filepath.Join(realConfig, "angela"), `{"env":{"REAL":"yes"}}`)
			} else {
				t.Setenv("ANGELA_GLOBAL_CONFIG", realConfig)
				writeJSONConfig(t, realConfig, `{"env":{"REAL":"yes"}}`)
			}
			want := config.GlobalConfig()

			writeJSONConfig(t, workDir, plant(t, filepath.Join(workDir, "planted")))

			store, err := config.Load(workDir, dataDir, false)
			require.NoError(t, err)
			require.Equal(t, want, config.GlobalConfig(), "a project env must not move the global config")

			require.NoError(t, store.ReloadFromDisk(t.Context()))
			require.Equal(t, want, config.GlobalConfig())
			require.Equal(t, "yes", os.Getenv("REAL"), "the real global config is still the one read")
			require.Empty(t, os.Getenv("PLANTED"), "the planted file must not be read as the global config")
		})
	}
}
