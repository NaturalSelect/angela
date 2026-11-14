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
// temporary root, so a test never sees the real /var/run, /run or /var/tmp
// of the machine it runs on.
func fakeProbe(t *testing.T, home string, tools []string, env map[string]string, euid int) containerProbe {
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
		euid:   euid,
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
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
}

func TestDetectContainerGrants_NoRuntimeInstalled(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mkdirs(t, filepath.Join(home, ".docker"), filepath.Join(home, ".local", "share", "containers"))

	g := detectContainerGrants(fakeProbe(t, home, nil, nil, 1000))
	require.True(t, g.Empty())
}

func TestDetectContainerGrants_Docker(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	mkdirs(t, filepath.Join(home, ".docker"), filepath.Join(home, ".local", "share", "containers"))
	listenUnix(t, filepath.Join(runtimeDir, "docker.sock"))

	g := detectContainerGrants(fakeProbe(t, home, []string{"docker"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, 1000))
	require.Equal(t, []string{filepath.Join(home, ".docker")}, g.ReadWrite)
	require.Equal(t, []string{filepath.Join(runtimeDir, "docker.sock")}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerSystemSockets(t *testing.T) {
	t.Parallel()

	p := fakeProbe(t, t.TempDir(), []string{"docker"}, nil, 1000)
	varRun := filepath.Join(p.root, "var", "run", "docker.sock")
	run := filepath.Join(p.root, "run", "docker.sock")
	listenUnix(t, varRun)
	listenUnix(t, run)

	g := detectContainerGrants(p)
	require.Equal(t, []string{varRun, run}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerEnvOverrides(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	cfgDir := filepath.Join(home, "dockercfg")
	sock := filepath.Join(home, "d", "docker.sock")
	mkdirs(t, cfgDir, filepath.Join(home, ".docker"))
	listenUnix(t, sock)

	env := map[string]string{
		"DOCKER_CONFIG": cfgDir,
		"DOCKER_HOST":   "unix://" + sock,
	}
	g := detectContainerGrants(fakeProbe(t, home, []string{"docker"}, env, 1000))
	require.Equal(t, []string{cfgDir}, g.ReadWrite)
	require.Equal(t, []string{sock}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerHostIgnoresNonUnix(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"}
	g := detectContainerGrants(fakeProbe(t, t.TempDir(), []string{"docker"}, env, 1000))
	require.Empty(t, g.UnixSockets)
}

func TestDetectContainerGrants_RootlessPodman(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	data := filepath.Join(home, ".local", "share", "containers")
	conf := filepath.Join(home, ".config", "containers")
	rtContainers := filepath.Join(runtimeDir, "containers")
	p := fakeProbe(t, home, []string{"podman"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, 1000)
	varTmp := filepath.Join(p.root, "var", "tmp")
	mkdirs(t, data, conf, rtContainers, varTmp)
	listenUnix(t, filepath.Join(runtimeDir, "podman", "podman.sock"))

	g := detectContainerGrants(p)
	require.ElementsMatch(t, []string{data, conf, rtContainers, varTmp}, g.ReadWrite)
	require.Equal(t, []string{filepath.Join(runtimeDir, "podman", "podman.sock")}, g.UnixSockets)
}

func TestDetectContainerGrants_BuildahAloneCountsAsPodmanFamily(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	data := filepath.Join(home, "xdg-data", "containers")
	mkdirs(t, data)

	g := detectContainerGrants(fakeProbe(t, home, []string{"buildah"}, map[string]string{"XDG_DATA_HOME": filepath.Join(home, "xdg-data")}, 1000))
	require.Contains(t, g.ReadWrite, data)
}

func TestDetectContainerGrants_RootfulStorageOnlyForRoot(t *testing.T) {
	t.Parallel()

	for _, euid := range []int{1000, 0} {
		p := fakeProbe(t, t.TempDir(), []string{"podman"}, nil, euid)
		rootful := []string{
			filepath.Join(p.root, "var", "lib", "containers", "storage"),
			filepath.Join(p.root, "run", "containers", "storage"),
			filepath.Join(p.root, "run", "libpod"),
		}
		mkdirs(t, rootful...)

		g := detectContainerGrants(p)
		if euid == 0 {
			require.ElementsMatch(t, rootful, g.ReadWrite)
		} else {
			require.Empty(t, g.ReadWrite)
		}
	}
}

func TestDetectContainerGrants_SkipsMissingAndNonSocketPaths(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	require.NoError(t, os.MkdirAll(runtimeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "docker.sock"), []byte("x"), 0o644))

	g := detectContainerGrants(fakeProbe(t, home, []string{"docker", "podman"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, 1000))
	require.True(t, g.Empty(), "a plain file is not a socket and absent directories are skipped, got %+v", g)
}

func TestDetectContainerGrants_EmptyHomeAndRuntimeDirYieldNoRelativePaths(t *testing.T) {
	t.Parallel()

	p := fakeProbe(t, "", []string{"docker", "podman"}, nil, 1000)
	mkdirs(t, filepath.Join(p.root, "var", "tmp"))

	g := detectContainerGrants(p)
	require.Equal(t, []string{filepath.Join(p.root, "var", "tmp")}, g.ReadWrite)
	for _, path := range append(g.ReadWrite, g.UnixSockets...) {
		require.True(t, filepath.IsAbs(path), "path %q is not absolute", path)
	}
}

func TestConfig_WithContainerGrants(t *testing.T) {
	t.Parallel()

	base := Config{ReadWrite: []string{"/work", "/shared"}, ReadOnly: []string{"/"}, UnixSockets: []string{"/run/x.sock"}}
	got := base.WithContainerGrants(ContainerGrants{
		ReadWrite:   []string{"/shared", "/home/u/.docker"},
		UnixSockets: []string{"/run/x.sock", "/run/docker.sock"},
	})

	require.Equal(t, []string{"/work", "/shared", "/home/u/.docker"}, got.ReadWrite)
	require.Equal(t, []string{"/run/x.sock", "/run/docker.sock"}, got.UnixSockets)
	require.Equal(t, []string{"/"}, got.ReadOnly)
	require.Equal(t, []string{"/work", "/shared"}, base.ReadWrite, "receiver must not be modified")
}
