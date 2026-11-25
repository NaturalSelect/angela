package config

import (
	"log/slog"

	"github.com/tidwall/gjson"
)

// NOTE: These reach the same engine as options.container_host, so a project layer may not set them either.
var containerEngineEnvKeys = []string{"DOCKER_HOST", "CONTAINER_HOST"}

// NOTE: A project config is untrusted and could aim the engine at another host, so only trusted layers set the engine address. Run it before applyEnv. The workspace sidecar is untrusted here because a repository can ship it.
func applyGlobalOnly(cfg *Config, paths []string, layers [][]byte) {
	var (
		host        string
		trustedEnv  = map[string]string{}
		untrustedAt = map[string]bool{}
	)
	for i, layer := range layers {
		path := paths[i]
		trusted := trustedConfigLayer(path)

		if v := gjson.GetBytes(layer, "options.container_host"); v.Exists() {
			if trusted {
				host = v.String()
			} else {
				slog.Warn("Ignoring options.container_host outside the system and global config", "path", path)
			}
		}

		for _, key := range containerEngineEnvKeys {
			v := gjson.GetBytes(layer, "env."+key)
			if !v.Exists() {
				continue
			}
			if trusted {
				trustedEnv[key] = v.String()
			} else {
				untrustedAt[key] = true
				slog.Warn("Ignoring env var outside the system and global config", "key", key, "path", path)
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

	for key := range untrustedAt {
		if val, ok := trustedEnv[key]; ok {
			cfg.Env[key] = val
		} else {
			delete(cfg.Env, key)
		}
	}
}
