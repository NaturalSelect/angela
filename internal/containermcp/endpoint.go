package containermcp

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/NaturalSelect/angela/internal/sandbox"
)

type Endpoint struct {
	// Host is the address handed to the engine client.
	Host string
	// Socket is the filesystem path of a unix socket endpoint, else empty.
	Socket string
}

// NOTE: Tries explicit, DOCKER_HOST, CONTAINER_HOST, then the first Docker or Podman socket that exists.
func ResolveEndpoint(explicit string) (Endpoint, error) {
	return resolveEndpoint(explicit, os.Getenv, existingCandidateSockets)
}

func resolveEndpoint(explicit string, getenv func(string) string, discover func() []string) (Endpoint, error) {
	for _, host := range []string{explicit, getenv("DOCKER_HOST"), getenv("CONTAINER_HOST")} {
		if host != "" {
			return parseEndpoint(host)
		}
	}
	if sockets := discover(); len(sockets) > 0 {
		return Endpoint{Host: "unix://" + sockets[0], Socket: sockets[0]}, nil
	}
	return Endpoint{}, errors.New("no container engine found: set container_host, DOCKER_HOST or CONTAINER_HOST, or start the docker or podman socket")
}

func parseEndpoint(host string) (Endpoint, error) {
	u, err := url.Parse(host)
	if err != nil {
		return Endpoint{}, fmt.Errorf("invalid container engine address %q: %w", host, err)
	}
	switch u.Scheme {
	case "unix":
		if !filepath.IsAbs(u.Path) {
			return Endpoint{}, fmt.Errorf("container engine address %q needs an absolute socket path", host)
		}
		return Endpoint{Host: host, Socket: filepath.Clean(u.Path)}, nil
	case "tcp":
		return Endpoint{Host: host}, nil
	case "npipe":
		if runtime.GOOS != "windows" {
			return Endpoint{}, fmt.Errorf("container engine address %q is only supported on Windows", host)
		}
		return Endpoint{Host: host}, nil
	}
	return Endpoint{}, fmt.Errorf("container engine address %q must start with unix://, tcp:// or npipe://", host)
}

func existingCandidateSockets() []string {
	var found []string
	for _, p := range sandbox.ContainerSocketCandidates() {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			found = append(found, p)
		}
	}
	return found
}

// NOTE: Refuses an endpoint the sandbox would block, so the server fails with advice instead of a bare connection error.
func CheckSandbox(server string, ep Endpoint) error {
	return checkSandbox(server, ep, sandbox.Enforced(), sandbox.AllowsUnixSocket)
}

func checkSandbox(server string, ep Endpoint, enforced bool, allows func(string) bool) error {
	if !enforced {
		return nil
	}
	if ep.Socket == "" {
		return fmt.Errorf("container mcp server %q cannot use %s inside the sandbox, which can only grant unix sockets", server, ep.Host)
	}
	if !allows(ep.Socket) {
		return fmt.Errorf("container mcp server %q needs the container engine socket %s, which this sandbox does not allow; start Angela with --sandbox-allow-docker-socket", server, ep.Socket)
	}
	return nil
}
