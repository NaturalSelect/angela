package config

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
)

// NOTE: The first two name the container engine. The next three decide where the engine sockets are looked up, which the bind mount check and the endpoint search rely on. The last two decide where the global config is found, and a reload trusts whatever it finds there.
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
	Env map[string]string `json:"env"`
}

// NOTE: A project config is untrusted and could aim the engine at another host or move the global config, so only trusted layers set a global-only variable. Run it before applyEnv. The workspace sidecar is untrusted here because a repository can ship it.
func applyGlobalOnly(cfg *Config, paths []string, layers [][]byte) {
	trustedEnv := map[string]string{}
	for i, layer := range layers {
		path := paths[i]
		trusted := trustedConfigLayer(path)

		var probe globalOnlyProbe
		// NOTE: A layer that does not decode never loaded, so it has nothing to filter.
		if err := json.Unmarshal(layer, &probe); err != nil {
			continue
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
	}

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
