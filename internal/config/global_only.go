package config

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

// NOTE: These reach the same engine as options.container_host, so a project layer may not set them either.
var containerEngineEnvKeys = []string{"DOCKER_HOST", "CONTAINER_HOST"}

// NOTE: Matched without regard to case on every platform, because Windows environment names ignore case and a lower-case spelling is harmless elsewhere.
func isContainerEngineEnv(name string) bool {
	return slices.ContainsFunc(containerEngineEnvKeys, func(key string) bool { return strings.EqualFold(key, name) })
}

// NOTE: Decoded like the loader decodes a layer, so a spelling the loader accepts, such as ENV for env, cannot slip past a lookup on exact keys.
type globalOnlyProbe struct {
	Options *struct {
		ContainerHost *string `json:"container_host"`
	} `json:"options"`
	Env map[string]string `json:"env"`
}

// NOTE: A project config is untrusted and could aim the engine at another host, so only trusted layers set the engine address. Run it before applyEnv. The workspace sidecar is untrusted here because a repository can ship it.
func applyGlobalOnly(cfg *Config, paths []string, layers [][]byte) {
	var host string
	trustedEnv := map[string]string{}
	for i, layer := range layers {
		path := paths[i]
		trusted := trustedConfigLayer(path)

		var probe globalOnlyProbe
		// NOTE: A layer that does not decode never loaded, so it has nothing to filter.
		if err := json.Unmarshal(layer, &probe); err != nil {
			continue
		}

		if probe.Options != nil && probe.Options.ContainerHost != nil {
			if trusted {
				host = *probe.Options.ContainerHost
			} else {
				slog.Warn("Ignoring options.container_host outside the system and global config", "path", path)
			}
		}

		for name, value := range probe.Env {
			if !isContainerEngineEnv(name) {
				continue
			}
			if trusted {
				trustedEnv[name] = value
			} else {
				slog.Warn("Ignoring env var outside the system and global config", "key", name, "path", path)
			}
		}

		gjson.GetBytes(layer, "mcp").ForEach(func(name, entity gjson.Result) bool {
			if entity.Get("container_host").Exists() {
				slog.Warn("Ignoring container_host on an MCP server; set options.container_host in the global config instead", "server", name.String(), "path", path)
			}
			return true
		})
	}

	if cfg.Options == nil {
		cfg.Options = &Options{}
	}
	cfg.Options.ContainerHost = host

	// NOTE: Rebuilt from the trusted layers alone, so no spelling an untrusted layer used can survive the merge.
	for name := range cfg.Env {
		if isContainerEngineEnv(name) {
			delete(cfg.Env, name)
		}
	}
	for name, value := range trustedEnv {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env[name] = value
	}
}
