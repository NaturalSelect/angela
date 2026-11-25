package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// NOTE: t.Setenv forbids t.Parallel, and GlobalConfig reads the environment.
func TestApplyGlobalOnly(t *testing.T) {
	t.Setenv("ANGELA_GLOBAL_CONFIG", t.TempDir())
	global := GlobalConfig()
	const (
		project   = "/project/angela.json"
		workspace = "/project/.angela/angela.json"
	)

	tests := []struct {
		name    string
		paths   []string
		layers  []string
		wantEnv map[string]string
	}{
		{
			name:    "project env cannot set the engine variables",
			paths:   []string{project},
			layers:  []string{`{"env":{"DOCKER_HOST":"tcp://evil:2375","CONTAINER_HOST":"tcp://evil:2376","KEEP":"1"}}`},
			wantEnv: map[string]string{"KEEP": "1"},
		},
		{
			name:    "workspace sidecar cannot set the engine variables",
			paths:   []string{workspace},
			layers:  []string{`{"env":{"DOCKER_HOST":"tcp://evil:2375","KEEP":"1"}}`},
			wantEnv: map[string]string{"KEEP": "1"},
		},
		{
			name:  "project env cannot override the global engine variable",
			paths: []string{global, project},
			layers: []string{
				`{"env":{"DOCKER_HOST":"unix:///g.sock"}}`,
				`{"env":{"DOCKER_HOST":"tcp://evil:2375"}}`,
			},
			wantEnv: map[string]string{"DOCKER_HOST": "unix:///g.sock"},
		},
		{
			name:    "global env keeps the engine variables",
			paths:   []string{global},
			layers:  []string{`{"env":{"DOCKER_HOST":"unix:///g.sock","OTHER":"x"}}`},
			wantEnv: map[string]string{"DOCKER_HOST": "unix:///g.sock", "OTHER": "x"},
		},
		{
			name:    "system env keeps the engine variables",
			paths:   []string{systemConfigPath},
			layers:  []string{`{"env":{"CONTAINER_HOST":"unix:///s.sock"}}`},
			wantEnv: map[string]string{"CONTAINER_HOST": "unix:///s.sock"},
		},
		{
			name:  "global env wins over system env",
			paths: []string{systemConfigPath, global},
			layers: []string{
				`{"env":{"DOCKER_HOST":"unix:///s.sock"}}`,
				`{"env":{"DOCKER_HOST":"unix:///g.sock"}}`,
			},
			wantEnv: map[string]string{"DOCKER_HOST": "unix:///g.sock"},
		},
		{
			name:    "project env keeps unrelated variables",
			paths:   []string{project},
			layers:  []string{`{"env":{"OTHER":"x"}}`},
			wantEnv: map[string]string{"OTHER": "x"},
		},
		// NOTE: Windows environment names ignore case, and encoding/json matches field names that way too, so each spelling below reaches the process environment.
		{
			name:    "project env cannot set the engine variables in another case",
			paths:   []string{project},
			layers:  []string{`{"env":{"docker_host":"tcp://evil:2375","Container_Host":"tcp://evil:2376","OTHER":"x"}}`},
			wantEnv: map[string]string{"OTHER": "x"},
		},
		{
			name:    "project env cannot hide behind another case of the env key",
			paths:   []string{project},
			layers:  []string{`{"ENV":{"DOCKER_HOST":"tcp://evil:2375","OTHER":"x"}}`},
			wantEnv: map[string]string{"OTHER": "x"},
		},
		{
			name:  "project env in another case cannot override the global engine variable",
			paths: []string{global, project},
			layers: []string{
				`{"env":{"DOCKER_HOST":"unix:///g.sock"}}`,
				`{"env":{"docker_host":"tcp://evil:2375"}}`,
			},
			wantEnv: map[string]string{"DOCKER_HOST": "unix:///g.sock"},
		},
		{
			name:    "global env keeps an engine variable under another case of the env key",
			paths:   []string{global},
			layers:  []string{`{"ENV":{"DOCKER_HOST":"unix:///g.sock"}}`},
			wantEnv: map[string]string{"DOCKER_HOST": "unix:///g.sock"},
		},
		{
			name:    "project env cannot move where the engine sockets are looked up",
			paths:   []string{project},
			layers:  []string{`{"env":{"XDG_RUNTIME_DIR":"/nonexistent","home":"/nonexistent","UserProfile":"/nonexistent","OTHER":"x"}}`},
			wantEnv: map[string]string{"OTHER": "x"},
		},
		{
			name:    "project env cannot move the global config",
			paths:   []string{project},
			layers:  []string{`{"env":{"ANGELA_GLOBAL_CONFIG":"/project/evil","xdg_config_home":"/project/evil","OTHER":"x"}}`},
			wantEnv: map[string]string{"OTHER": "x"},
		},
		{
			name:  "project env cannot override a global location variable",
			paths: []string{global, project},
			layers: []string{
				`{"env":{"XDG_RUNTIME_DIR":"/run/user/1000"}}`,
				`{"env":{"XDG_RUNTIME_DIR":"/nonexistent"}}`,
			},
			wantEnv: map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"},
		},
		{
			name:    "global env keeps the location variables",
			paths:   []string{global},
			layers:  []string{`{"env":{"XDG_RUNTIME_DIR":"/run/user/1000","HOME":"/home/u","XDG_CONFIG_HOME":"/home/u/cfg"}}`},
			wantEnv: map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "HOME": "/home/u", "XDG_CONFIG_HOME": "/home/u/cfg"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := make([][]byte, len(tt.layers))
			for i, l := range tt.layers {
				raw[i] = []byte(l)
			}
			cfg, err := loadFromBytes(raw)
			require.NoError(t, err)

			applyGlobalOnly(cfg, tt.paths, raw)

			if len(tt.wantEnv) == 0 {
				require.Empty(t, cfg.Env)
			} else {
				require.Equal(t, tt.wantEnv, cfg.Env)
			}
		})
	}
}
