package sandbox

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeProbe builds a probe whose fixed system paths resolve under a fresh
// temporary root, so a test never sees the real /var/run or /run of the
// machine it runs on.
func fakeProbe(t *testing.T, home string, tools []string, env map[string]string) containerProbe {
	t.Helper()
	return containerProbe{
		lookPath: func(name string) (string, error) {
			for _, tool := range tools {
				if tool == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		},
		getenv: func(k string) string { return env[k] },
		home:   home,
		root:   t.TempDir(),
	}
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		require.NoError(t, os.MkdirAll(p, 0o755))
	}
}

func listenUnix(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	if len(path) > 100 {
		t.Skipf("unix socket path too long: %s", path)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
}

func TestDetectContainerGrants_NoRuntimeInstalled(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mkdirs(t, filepath.Join(home, ".docker"), filepath.Join(home, ".local", "share", "containers", "storage"))

	g := detectContainerGrants(fakeProbe(t, home, nil, nil))
	require.True(t, g.Empty())
}

// TestDetectContainerGrants_DockerGrantsNoDirectories pins that the Docker
// CLI alone grants nothing: its config holds registry credentials and its
// storage belongs to the daemon.
func TestDetectContainerGrants_DockerGrantsNoDirectories(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfgDir := filepath.Join(home, "dockercfg")
	mkdirs(t, filepath.Join(home, ".docker"), cfgDir)

	g := detectContainerGrants(fakeProbe(t, home, []string{"docker"}, map[string]string{"DOCKER_CONFIG": cfgDir}))
	require.True(t, g.Empty())
}

func TestDetectContainerGrants_DockerSocket(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	listenUnix(t, filepath.Join(runtimeDir, "docker.sock"))

	g := detectContainerGrants(fakeProbe(t, home, []string{"docker"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}))
	require.Equal(t, []string{filepath.Join(runtimeDir, "docker.sock")}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerSystemSockets(t *testing.T) {
	t.Parallel()

	p := fakeProbe(t, t.TempDir(), []string{"docker"}, nil)
	varRun := filepath.Join(p.root, "var", "run", "docker.sock")
	run := filepath.Join(p.root, "run", "docker.sock")
	listenUnix(t, varRun)
	listenUnix(t, run)

	g := detectContainerGrants(p)
	require.Equal(t, []string{varRun, run}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerHostSocket(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	sock := filepath.Join(home, "d", "docker.sock")
	listenUnix(t, sock)

	g := detectContainerGrants(fakeProbe(t, home, []string{"docker"}, map[string]string{"DOCKER_HOST": "unix://" + sock}))
	require.Equal(t, []string{sock}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerHostIgnoresNonUnix(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"}
	g := detectContainerGrants(fakeProbe(t, t.TempDir(), []string{"docker"}, env))
	require.Empty(t, g.UnixSockets)
}

// TestDetectContainerGrants_PodmanGrantsOnlySockets pins that of everything
// Podman keeps on disk, only the daemon socket is granted. Image storage,
// config, auth and shared temp directories stay out even when they exist.
func TestDetectContainerGrants_PodmanGrantsOnlySockets(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	p := fakeProbe(t, home, []string{"podman"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir})
	mkdirs(
		t,
		filepath.Join(home, ".local", "share", "containers", "storage"),
		filepath.Join(home, ".local", "share", "containers", "cache"),
		filepath.Join(home, ".config", "containers"),
		filepath.Join(runtimeDir, "containers"),
		filepath.Join(runtimeDir, "libpod"),
		filepath.Join(p.root, "var", "lib", "containers", "storage"),
		filepath.Join(p.root, "var", "tmp"),
	)
	sock := filepath.Join(runtimeDir, "podman", "podman.sock")
	listenUnix(t, sock)

	g := detectContainerGrants(p)
	require.Equal(t, ContainerGrants{UnixSockets: []string{sock}}, g)
}

func TestDetectContainerGrants_PodmanContainerHostSocket(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	sock := filepath.Join(home, "p", "podman.sock")
	listenUnix(t, sock)

	g := detectContainerGrants(fakeProbe(t, home, []string{"podman"}, map[string]string{"CONTAINER_HOST": "unix://" + sock}))
	require.Equal(t, []string{sock}, g.UnixSockets)
}

// TestDetectContainerGrants_BuildahGrantsNothing pins that Buildah has no
// daemon to talk to, so a host with only Buildah offers nothing.
func TestDetectContainerGrants_BuildahGrantsNothing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	mkdirs(t, filepath.Join(home, ".local", "share", "containers", "storage"))
	listenUnix(t, filepath.Join(runtimeDir, "podman", "podman.sock"))

	g := detectContainerGrants(fakeProbe(t, home, []string{"buildah"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}))
	require.True(t, g.Empty())
}

func TestDetectContainerGrants_SkipsMissingAndNonSocketPaths(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	require.NoError(t, os.MkdirAll(runtimeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "docker.sock"), []byte("x"), 0o644))

	g := detectContainerGrants(fakeProbe(t, home, []string{"docker", "podman"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}))
	require.True(t, g.Empty(), "a plain file is not a socket and absent paths are skipped, got %+v", g)
}

func TestDetectContainerGrants_EmptyHomeAndRuntimeDirYieldNoRelativePaths(t *testing.T) {
	t.Parallel()

	p := fakeProbe(t, "", []string{"docker", "podman"}, nil)
	sock := filepath.Join(p.root, "run", "podman", "podman.sock")
	listenUnix(t, sock)

	g := detectContainerGrants(p)
	require.Equal(t, []string{sock}, g.UnixSockets)
	for _, path := range g.UnixSockets {
		require.True(t, filepath.IsAbs(path), "path %q is not absolute", path)
	}
}

func TestConfig_WithContainerGrants(t *testing.T) {
	t.Parallel()

	base := Config{ReadWrite: []string{"/work", "/shared"}, ReadOnly: []string{"/"}, UnixSockets: []string{"/run/x.sock"}}
	got := base.WithContainerGrants(ContainerGrants{
		UnixSockets: []string{"/run/x.sock", "/run/docker.sock"},
	})

	require.Equal(t, []string{"/work", "/shared"}, got.ReadWrite)
	require.Equal(t, []string{"/run/x.sock", "/run/docker.sock"}, got.UnixSockets)
	require.Equal(t, []string{"/"}, got.ReadOnly)
	require.Equal(t, []string{"/run/x.sock"}, base.UnixSockets, "receiver must not be modified")
}

func TestContainerSocketCandidates_IgnoresInstalledTools(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_HOST", "unix:///custom/docker.sock")
	t.Setenv("CONTAINER_HOST", "tcp://127.0.0.1:2375")

	got := ContainerSocketCandidates()

	require.Contains(t, got, "/var/run/docker.sock")
	require.Contains(t, got, "/run/podman/podman.sock")
	require.Contains(t, got, filepath.Join(runtimeDir, "docker.sock"))
	require.Contains(t, got, filepath.Join(runtimeDir, "podman", "podman.sock"))
	require.Contains(t, got, filepath.Join(home, ".docker", "desktop", "docker.sock"))
	require.Contains(t, got, "/custom/docker.sock")
	require.NotContains(t, got, "")
	for _, path := range got {
		require.True(t, filepath.IsAbs(path), "path %q is not absolute", path)
	}
}

func TestContainerSocketCandidates_NoRuntimeDirYieldsNoRelativePaths(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")

	for _, path := range ContainerSocketCandidates() {
		require.True(t, filepath.IsAbs(path), "path %q is not absolute", path)
	}
}

func TestEnforced(t *testing.T) {
	rs := resolve(Config{ReadWrite: []string{"/work"}})
	empty := resolve(Config{})

	setSandboxStateForTest(t, false, false, nil)
	require.False(t, Enforced())

	appliedRules.Store(&empty)
	require.False(t, Enforced())

	appliedRules.Store(&rs)
	require.True(t, Enforced())
}

func TestAllowsUnixSocket_NotEnforced(t *testing.T) {
	setSandboxStateForTest(t, false, false, nil)
	require.True(t, AllowsUnixSocket("/var/run/docker.sock"))

	empty := resolve(Config{})
	appliedRules.Store(&empty)
	require.True(t, AllowsUnixSocket("/var/run/docker.sock"))
}

func TestAllowsUnixSocket_Enforced(t *testing.T) {
	dir := t.TempDir()
	granted := filepath.Join(dir, "granted.sock")
	other := filepath.Join(dir, "other.sock")
	listenUnix(t, granted)
	listenUnix(t, other)
	link := filepath.Join(dir, "link.sock")
	require.NoError(t, os.Symlink(granted, link))

	rs := resolve(Config{ReadWrite: []string{dir}, UnixSockets: []string{granted}})
	setSandboxStateForTest(t, true, false, &rs)

	require.True(t, AllowsUnixSocket(granted))
	require.True(t, AllowsUnixSocket(granted+"/../granted.sock"), "path is cleaned first")
	require.True(t, AllowsUnixSocket(link), "a symlink to a granted socket is the same file")
	require.False(t, AllowsUnixSocket(other))
	require.False(t, AllowsUnixSocket(filepath.Join(dir, "missing.sock")))
}

func TestAllowsUnixSocket_GrantedThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.sock")
	listenUnix(t, target)
	link := filepath.Join(dir, "link.sock")
	require.NoError(t, os.Symlink(target, link))

	rs := resolve(Config{ReadWrite: []string{dir}, UnixSockets: []string{link}})
	setSandboxStateForTest(t, true, false, &rs)

	require.True(t, AllowsUnixSocket(target))
}
