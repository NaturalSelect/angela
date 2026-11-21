package containermcp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/sandbox"
)

// NOTE: Adds the sockets container servers connect to, which the CLI based detection misses when no docker or podman CLI is installed.
func SandboxGrants(store *config.ConfigStore, grantSockets bool) sandbox.ContainerGrants {
	g := sandbox.DetectContainerGrants(grantSockets)
	cfg := store.Config()
	if cfg == nil {
		return g
	}
	return g.WithSockets(serverSockets(cfg.MCP, store.ContainerFieldResolver, existingCandidateSockets), grantSockets)
}

func serverSockets(servers config.MCPs, resolver func(name string) config.VariableResolver, discover func() []string) []string {
	var sockets []string
	for name, m := range servers {
		if m.Type != config.MCPContainer || m.Disabled {
			continue
		}
		host, err := resolver(name).ResolveValue(m.ContainerHost)
		if err != nil {
			continue
		}
		ep, err := resolveEndpoint(strings.TrimSpace(host), os.Getenv, discover)
		if err != nil || ep.Socket == "" {
			continue
		}
		if fi, err := os.Stat(ep.Socket); err == nil && fi.Mode()&os.ModeSocket != 0 {
			sockets = append(sockets, filepath.Clean(ep.Socket))
		}
	}
	return sockets
}
