package sandbox

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fakeProbe(home string, tools []string, env map[string]string, euid int) containerProbe {
	return containerProbe{
		lookPath: func(name string) (string, error) {
			for _, t := range tools {
				if t == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		},
		getenv: func(k string) string { return env[k] },
		home:   home,
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

	g := detectContainerGrants(fakeProbe(home, nil, nil, 1000))
	require.True(t, g.Empty())
}

func TestDetectContainerGrants_Docker(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	mkdirs(t, filepath.Join(home, ".docker"), filepath.Join(home, ".local", "share", "containers"))
	listenUnix(t, filepath.Join(runtimeDir, "docker.sock"))

	g := detectContainerGrants(fakeProbe(home, []string{"docker"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, 1000))
	require.Equal(t, []string{filepath.Join(home, ".docker")}, g.ReadWrite)
	require.Equal(t, []string{filepath.Join(runtimeDir, "docker.sock")}, g.UnixSockets)
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
	g := detectContainerGrants(fakeProbe(home, []string{"docker"}, env, 1000))
	require.Equal(t, []string{cfgDir}, g.ReadWrite)
	require.Equal(t, []string{sock}, g.UnixSockets)
}

func TestDetectContainerGrants_DockerHostIgnoresNonUnix(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	env := map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"}
	g := detectContainerGrants(fakeProbe(home, []string{"docker"}, env, 1000))
	require.Empty(t, g.UnixSockets)
}

func TestDetectContainerGrants_RootlessPodman(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	data := filepath.Join(home, ".local", "share", "containers")
	conf := filepath.Join(home, ".config", "containers")
	rtContainers := filepath.Join(runtimeDir, "containers")
	mkdirs(t, data, conf, rtContainers)
	listenUnix(t, filepath.Join(runtimeDir, "podman", "podman.sock"))

	g := detectContainerGrants(fakeProbe(home, []string{"podman"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, 1000))
	require.ElementsMatch(t, []string{data, conf, rtContainers, "/var/tmp"}, g.ReadWrite)
	require.Equal(t, []string{filepath.Join(runtimeDir, "podman", "podman.sock")}, g.UnixSockets)
}

func TestDetectContainerGrants_BuildahAloneCountsAsPodmanFamily(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	data := filepath.Join(home, "xdg-data", "containers")
	mkdirs(t, data)

	g := detectContainerGrants(fakeProbe(home, []string{"buildah"}, map[string]string{"XDG_DATA_HOME": filepath.Join(home, "xdg-data")}, 1000))
	require.Contains(t, g.ReadWrite, data)
}

func TestDetectContainerGrants_RootfulStorageOnlyForRoot(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"/var/lib/containers/storage", "/run/containers/storage", "/run/libpod"} {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		home := t.TempDir()
		require.NotContains(t, detectContainerGrants(fakeProbe(home, []string{"podman"}, nil, 1000)).ReadWrite, root)
		require.Contains(t, detectContainerGrants(fakeProbe(home, []string{"podman"}, nil, 0)).ReadWrite, root)
	}
}

func TestDetectContainerGrants_SkipsMissingAndNonSocketPaths(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtimeDir := filepath.Join(home, "run")
	require.NoError(t, os.MkdirAll(runtimeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "docker.sock"), []byte("x"), 0o644))

	g := detectContainerGrants(fakeProbe(home, []string{"docker", "podman"}, map[string]string{"XDG_RUNTIME_DIR": runtimeDir}, 1000))
	require.NotContains(t, g.UnixSockets, filepath.Join(runtimeDir, "docker.sock"))
	require.NotContains(t, g.ReadWrite, filepath.Join(home, ".docker"))
	require.NotContains(t, g.ReadWrite, filepath.Join(home, ".local", "share", "containers"))
}

func TestDetectContainerGrants_EmptyHomeAndRuntimeDirYieldNoRelativePaths(t *testing.T) {
	t.Parallel()

	g := detectContainerGrants(fakeProbe("", []string{"docker", "podman"}, nil, 1000))
	for _, p := range append(g.ReadWrite, g.UnixSockets...) {
		require.True(t, filepath.IsAbs(p), "path %q is not absolute", p)
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
