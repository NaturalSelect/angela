package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ContainerGrants lists the daemon sockets the Docker and Podman command
// line tools connect to once a sandbox is entered. Both slices only hold
// paths that existed when they were detected. No directory is ever
// granted: image storage belongs to the daemon, which runs outside the
// sandbox.
type ContainerGrants struct {
	// UnixSockets lists the daemon sockets the tools connect to.
	UnixSockets []string
	// NOTE: Detected daemon sockets that stay ungranted until the user opts in.
	OptionalSockets []string
}

// Empty reports whether g grants nothing.
func (g ContainerGrants) Empty() bool {
	return len(g.UnixSockets) == 0
}

// WithContainerGrants returns cfg with g's sockets appended to UnixSockets.
func (c Config) WithContainerGrants(g ContainerGrants) Config {
	c.UnixSockets = DedupePaths(append(append([]string{}, c.UnixSockets...), g.UnixSockets...))
	return c
}

// DetectContainerGrants looks for the Docker and Podman command line tools
// on this machine and returns the daemon sockets they connect to. A socket
// grant is an escape hatch: whoever can talk to a container daemon can
// start a container that mounts any host path, so the detected sockets are
// only granted when grantSockets is set and are otherwise returned as
// OptionalSockets.
func DetectContainerGrants(grantSockets bool) ContainerGrants {
	home, _ := os.UserHomeDir()
	g := detectContainerGrants(containerProbe{
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		home:     home,
		root:     systemRoot(),
	})
	if !grantSockets {
		g = g.withSocketsOptional()
	}
	return g
}

// NOTE: Adds sockets found by other means; they are granted or only offered by the same opt-in as detected ones.
func (g ContainerGrants) WithSockets(paths []string, grant bool) ContainerGrants {
	if len(paths) == 0 {
		return g
	}
	if grant {
		g.UnixSockets = DedupePaths(append(append([]string{}, g.UnixSockets...), paths...))
		return g
	}
	g.OptionalSockets = DedupePaths(append(append([]string{}, g.OptionalSockets...), paths...))
	return g
}

// withSocketsOptional returns g with its granted sockets moved to
// OptionalSockets.
func (g ContainerGrants) withSocketsOptional() ContainerGrants {
	g.OptionalSockets = DedupePaths(append(append([]string{}, g.OptionalSockets...), g.UnixSockets...))
	g.UnixSockets = nil
	return g
}

// containerProbe is the view of the host detectContainerGrants reads,
// so tests can stand in for it.
type containerProbe struct {
	lookPath func(string) (string, error)
	getenv   func(string) string
	home     string
	// root is the directory the fixed system paths such as
	// /var/run/docker.sock are resolved under. It is "/" outside tests.
	root string
}

func (p containerProbe) has(tool string) bool {
	_, err := p.lookPath(tool)
	return err == nil
}

// NOTE: Fixed Unix paths like /var/run/docker.sock are drive-relative on Windows and do not exist there.
func systemRoot() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/"
}

// sys returns the fixed system path under p.root, or "" when no root applies.
func (p containerProbe) sys(path string) string {
	if p.root == "" {
		return ""
	}
	return filepath.Join(p.root, path)
}

// join returns the join of elem under the base directory, or "" when
// base is empty so callers never produce a relative path.
func join(base string, elem ...string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, elem...)...)
}

// unixHostPath returns the socket path of a unix:// host URL, or "".
func unixHostPath(host string) string {
	path, ok := strings.CutPrefix(host, "unix://")
	if !ok || !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func (p containerProbe) dockerSockets() []string {
	runtimeDir := p.getenv("XDG_RUNTIME_DIR")
	return []string{
		p.sys("/var/run/docker.sock"),
		p.sys("/run/docker.sock"),
		join(runtimeDir, "docker.sock"),
		join(p.home, ".docker", "run", "docker.sock"),
		join(p.home, ".docker", "desktop", "docker.sock"),
		unixHostPath(p.getenv("DOCKER_HOST")),
	}
}

func (p containerProbe) podmanSockets() []string {
	return []string{
		join(p.getenv("XDG_RUNTIME_DIR"), "podman", "podman.sock"),
		p.sys("/run/podman/podman.sock"),
		unixHostPath(p.getenv("CONTAINER_HOST")),
	}
}

func detectContainerGrants(p containerProbe) ContainerGrants {
	var sockets []string

	if p.has("docker") {
		sockets = append(sockets, p.dockerSockets()...)
	}

	if p.has("podman") {
		sockets = append(sockets, p.podmanSockets()...)
	}

	return ContainerGrants{UnixSockets: existingSockets(sockets)}
}

// NOTE: Lists every common daemon socket path, installed or not, so bind mounts can avoid them.
func ContainerSocketCandidates() []string {
	home, _ := os.UserHomeDir()
	p := containerProbe{
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		home:     home,
		root:     systemRoot(),
	}
	all := append(p.dockerSockets(), p.podmanSockets()...)
	cleaned := make([]string, 0, len(all))
	for _, s := range all {
		if s != "" {
			cleaned = append(cleaned, filepath.Clean(s))
		}
	}
	return DedupePaths(cleaned)
}

// existingSockets returns the cleaned, deduplicated entries of paths that
// are Unix sockets right now. Empty entries are skipped.
func existingSockets(paths []string) []string {
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			cleaned = append(cleaned, p)
		}
	}
	out := DedupePaths(cleaned)
	if len(out) == 0 {
		return nil
	}
	return out
}
