package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ContainerGrants lists the extra paths the Docker, Podman and Buildah
// command line tools need once a sandbox is entered. Both slices only
// hold paths that existed when they were detected.
type ContainerGrants struct {
	// ReadWrite lists directories the tools write their own state to.
	ReadWrite []string
	// UnixSockets lists the daemon sockets the tools connect to.
	UnixSockets []string
}

// Empty reports whether g grants nothing.
func (g ContainerGrants) Empty() bool {
	return len(g.ReadWrite) == 0 && len(g.UnixSockets) == 0
}

// WithContainerGrants returns cfg with g's paths appended to ReadWrite
// and UnixSockets.
func (c Config) WithContainerGrants(g ContainerGrants) Config {
	c.ReadWrite = DedupePaths(append(append([]string{}, c.ReadWrite...), g.ReadWrite...))
	c.UnixSockets = DedupePaths(append(append([]string{}, c.UnixSockets...), g.UnixSockets...))
	return c
}

// DetectContainerGrants looks for the Docker, Podman and Buildah command
// line tools on this machine and returns the directories and sockets
// they need. A socket grant is an escape hatch: whoever can talk to a
// container daemon can start a container that mounts any host path.
func DetectContainerGrants() ContainerGrants {
	home, _ := os.UserHomeDir()
	return detectContainerGrants(containerProbe{
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		home:     home,
		root:     "/",
		euid:     os.Geteuid(),
	})
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
	euid int
}

func (p containerProbe) has(tool string) bool {
	_, err := p.lookPath(tool)
	return err == nil
}

// sys returns the fixed system path under p.root.
func (p containerProbe) sys(path string) string {
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

func detectContainerGrants(p containerProbe) ContainerGrants {
	runtimeDir := p.getenv("XDG_RUNTIME_DIR")
	dataHome := p.getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = join(p.home, ".local", "share")
	}
	configHome := p.getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = join(p.home, ".config")
	}

	var dirs, sockets []string

	if p.has("docker") {
		dockerConfig := p.getenv("DOCKER_CONFIG")
		if dockerConfig == "" {
			dockerConfig = join(p.home, ".docker")
		}
		dirs = append(dirs, dockerConfig)
		sockets = append(
			sockets,
			p.sys("/var/run/docker.sock"),
			p.sys("/run/docker.sock"),
			join(runtimeDir, "docker.sock"),
			join(p.home, ".docker", "run", "docker.sock"),
			join(p.home, ".docker", "desktop", "docker.sock"),
			unixHostPath(p.getenv("DOCKER_HOST")),
		)
	}

	if p.has("podman") || p.has("buildah") {
		dirs = append(
			dirs,
			join(dataHome, "containers"),
			join(runtimeDir, "containers"),
			join(runtimeDir, "libpod"),
			join(configHome, "containers"),
			p.sys("/var/tmp"),
		)
		sockets = append(
			sockets,
			join(runtimeDir, "podman", "podman.sock"),
			p.sys("/run/podman/podman.sock"),
			unixHostPath(p.getenv("CONTAINER_HOST")),
		)
		if p.euid == 0 {
			dirs = append(
				dirs,
				p.sys("/var/lib/containers/storage"),
				p.sys("/run/containers/storage"),
				p.sys("/run/libpod"),
			)
		}
	}

	return ContainerGrants{
		ReadWrite:   existingPaths(dirs, func(fi os.FileInfo) bool { return fi.IsDir() }),
		UnixSockets: existingPaths(sockets, func(fi os.FileInfo) bool { return fi.Mode()&os.ModeSocket != 0 }),
	}
}

// existingPaths returns the cleaned, deduplicated entries of paths whose
// os.Stat result satisfies keep. Empty entries are skipped.
func existingPaths(paths []string, keep func(os.FileInfo) bool) []string {
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if fi, err := os.Stat(p); err == nil && keep(fi) {
			cleaned = append(cleaned, p)
		}
	}
	out := DedupePaths(cleaned)
	if len(out) == 0 {
		return nil
	}
	return out
}
