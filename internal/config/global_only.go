package config

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

// NOTE: The first two reach the same engine as options.container_host. The next three decide where the engine sockets are looked up, which the bind mount check and the endpoint search rely on. The last two decide where the global config is found, and a reload trusts whatever it finds there.
var globalOnlyEnvKeys = []string{
	"DOCKER_HOST", "CONTAINER_HOST",
	"XDG_RUNTIME_DIR", "HOME", "USERPROFILE",
	"ANGELA_GLOBAL_CONFIG", "XDG_CONFIG_HOME",
}

// NOTE: Matched without regard to case on every platform, because Windows environment names ignore case and a lower-case spelling is harmless elsewhere.
func isGlobalOnlyEnv(name string) bool {
	return slices.ContainsFunc(globalOnlyEnvKeys, func(key string) bool { return strings.EqualFold(key, name) })
}

// NOTE: Decoded like the loader decodes a layer, so a spelling the loader accepts, such as ENV for env, cannot slip past a lookup on exact keys.
type globalOnlyProbe struct {
	Options *struct {
		ContainerHost *string `json:"container_host"`
	} `json:"options"`
	Env map[string]string `json:"env"`
}

// NOTE: A project config is untrusted and could aim the engine at another host or move the global config, so only trusted layers set the engine address or a global-only variable. Run it before applyEnv. The workspace sidecar is untrusted here because a repository can ship it.
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
			if !isGlobalOnlyEnv(name) {
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
		if isGlobalOnlyEnv(name) {
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
