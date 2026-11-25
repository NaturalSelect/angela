package containermcp

import (
	"os"
	"path/filepath"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/sandbox"
)

// NOTE: Adds the socket container servers connect to, which the CLI based detection misses when no docker or podman CLI is installed.
func SandboxGrants(store *config.ConfigStore, grantSockets bool) sandbox.ContainerGrants {
	g := sandbox.DetectContainerGrants(grantSockets)
	cfg := store.Config()
	if cfg == nil {
		return g
	}
	return g.WithSockets(serverSockets(cfg.MCP, existingCandidateSockets), grantSockets)
}

func serverSockets(servers config.MCPs, discover func() []string) []string {
	enabled := false
	for _, m := range servers {
		if m.Type == config.MCPContainer && !m.Disabled {
			enabled = true
			break
		}
	}
	if !enabled {
		return nil
	}
	ep, err := resolveEndpoint(os.Getenv, discover)
	if err != nil || ep.Socket == "" {
		return nil
	}
	if fi, err := os.Stat(ep.Socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
		return []string{filepath.Clean(ep.Socket)}
	}
	return nil
}
