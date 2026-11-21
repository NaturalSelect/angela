package containermcp

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func validGroup() Group {
	return Group{Server: "srv", Main: Service{Name: "srv", Image: "img:1", Pull: PullMissing}}
}

func TestGroupValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Group)
		wantErr string
	}{
		{"valid", func(*Group) {}, ""},
		{"no image", func(g *Group) { g.Main.Image = "  " }, "has no image"},
		{"bad network", func(g *Group) { g.Network = "container:abc" }, "network must be"},
		{"host network", func(g *Group) { g.Network = "host" }, ""},
		{"bad pull", func(g *Group) { g.Main.Pull = "sometimes" }, "pull must be"},
		{"empty pull", func(g *Group) { g.Main.Pull = "" }, ""},
		{"relative target", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: MountVolume, Source: "v", Target: "data"}}
		}, "absolute path"},
		{"named volume", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: MountVolume, Source: "cache_1", Target: "/cache"}}
		}, ""},
		{"bad volume name", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: MountVolume, Source: "-x", Target: "/cache"}}
		}, "invalid volume name"},
		{"tmpfs with source", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: MountTmpfs, Source: "x", Target: "/tmp"}}
		}, "cannot have a source"},
		{"tmpfs", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: MountTmpfs, Target: "/tmp"}}
		}, ""},
		{"bad relabel", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: MountTmpfs, Target: "/tmp", Relabel: "x"}}
		}, "relabel"},
		{"unknown type", func(g *Group) {
			g.Main.Mounts = []Mount{{Type: "npipe", Source: "x", Target: "/x"}}
		}, "unknown mount type"},
		{"dependency without image", func(g *Group) {
			g.Deps = []Service{{Name: "db"}}
		}, `service "db" has no image`},
		{"shm size and init", func(g *Group) {
			g.Main.ShmSize, g.Main.Init = 1<<30, true
		}, ""},
		{"negative shm size", func(g *Group) { g.Main.ShmSize = -1 }, "shm size"},
		{"host gateway", func(g *Group) {
			g.Main.ExtraHosts = []string{"host.docker.internal:host-gateway", "v6.local:::1"}
		}, ""},
		{"extra host without ip", func(g *Group) { g.Main.ExtraHosts = []string{"db.local"} }, "must be host:ip"},
		{"extra host with empty ip", func(g *Group) { g.Main.ExtraHosts = []string{"db.local:"} }, "must be host:ip"},
		{"extra host with empty name", func(g *Group) { g.Main.ExtraHosts = []string{":10.0.0.1"} }, "must be host:ip"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := validGroup()
			tc.mutate(&g)
			err := g.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestValidateBindSource(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "run", "user", "1000")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")

	other := filepath.Join(runtimeDir, "other")
	require.NoError(t, os.MkdirAll(other, 0o755))
	podmanSock := filepath.Join(runtimeDir, "podman", "podman.sock")
	listenUnix(t, podmanSock)
	plain := t.TempDir()

	t.Run("plain directory", func(t *testing.T) {
		require.NoError(t, validateBindSource(plain))
	})
	t.Run("relative", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource("data"), "absolute path")
	})
	t.Run("missing", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource(filepath.Join(plain, "nope")), "does not exist")
	})
	t.Run("socket file", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource(podmanSock), "is a socket")
	})
	t.Run("directory holding a socket", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource(filepath.Dir(podmanSock)), "would expose")
	})
	t.Run("runtime dir", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource(runtimeDir), "would expose")
	})
	t.Run("parent of runtime dir", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource(filepath.Dir(runtimeDir)), "would expose")
	})
	t.Run("sibling inside runtime dir", func(t *testing.T) {
		require.NoError(t, validateBindSource(other))
	})
	t.Run("filesystem root", func(t *testing.T) {
		require.ErrorContains(t, validateBindSource("/"), "would expose")
	})
	t.Run("symlink to runtime dir", func(t *testing.T) {
		link := filepath.Join(plain, "link")
		require.NoError(t, os.Symlink(runtimeDir, link))
		require.ErrorContains(t, validateBindSource(link), "would expose")
	})
}

func TestCanonicalPath_ResolvesSymlinkedPrefixOfMissingPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	require.NoError(t, os.Mkdir(real, 0o755))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(real, link))

	want, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(want, "missing", "x.sock"), canonicalPath(filepath.Join(link, "missing", "x.sock")))
}

func TestWithin(t *testing.T) {
	t.Parallel()

	require.True(t, within("/a/b", "/a"))
	require.True(t, within("/a", "/a"))
	require.True(t, within("/a/b/c", "/"))
	require.False(t, within("/a", "/a/b"))
	require.False(t, within("/ab", "/a"))
}
