package containermcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "angela-grants")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "e.sock")
	listenUnix(t, path)
	return path
}

func TestWithServerSockets(t *testing.T) {
	sock := shortSocket(t)
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	none := func() []string { return nil }
	container := config.MCPs{"a": {Type: config.MCPContainer, Image: "i"}}
	detected := sandbox.ContainerGrants{OptionalSockets: []string{"/run/podman/podman.sock"}}

	t.Run("offered and preselected when sockets are not granted", func(t *testing.T) {
		got := withServerSockets(detected, container, none, false)
		require.Empty(t, got.UnixSockets)
		require.Equal(t, []string{"/run/podman/podman.sock", sock}, got.OptionalSockets)
		require.Equal(t, []string{sock}, got.PreselectedSockets, "only the server's own socket starts allowed")
	})
	t.Run("granted when sockets are granted", func(t *testing.T) {
		got := withServerSockets(detected, container, none, true)
		require.Equal(t, []string{sock}, got.UnixSockets)
		require.Equal(t, []string{"/run/podman/podman.sock"}, got.OptionalSockets)
	})
	t.Run("nothing preselected without a container server", func(t *testing.T) {
		servers := config.MCPs{
			"stdio": {Type: config.MCPStdio, Command: "x"},
			"off":   {Type: config.MCPContainer, Image: "i", Disabled: true},
		}
		require.Equal(t, detected, withServerSockets(detected, servers, none, false))
	})
}

func TestServerSockets(t *testing.T) {
	sock := shortSocket(t)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	none := func() []string { return nil }
	container := config.MCPs{"a": {Type: config.MCPContainer, Image: "i"}}

	t.Run("host of an existing socket", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "unix://"+sock)
		require.Equal(t, []string{sock}, serverSockets(container, none))
	})
	t.Run("discovered socket when no host is set", func(t *testing.T) {
		require.Equal(t, []string{sock}, serverSockets(container, func() []string { return []string{sock} }))
	})
	t.Run("one socket however many servers use it", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "unix://"+sock)
		servers := config.MCPs{
			"a": {Type: config.MCPContainer, Image: "i"},
			"b": {Type: config.MCPContainer, Image: "j"},
		}
		require.Equal(t, []string{sock}, serverSockets(servers, none))
	})
	t.Run("ignores other types and disabled servers", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "unix://"+sock)
		servers := config.MCPs{
			"stdio": {Type: config.MCPStdio, Command: "x"},
			"off":   {Type: config.MCPContainer, Image: "i", Disabled: true},
		}
		require.Empty(t, serverSockets(servers, none))
	})
	t.Run("ignores missing sockets and tcp hosts", func(t *testing.T) {
		for _, host := range []string{
			"unix://" + filepath.Join(t.TempDir(), "nope.sock"),
			"tcp://127.0.0.1:2375",
			"ftp://x",
		} {
			t.Setenv("DOCKER_HOST", host)
			require.Empty(t, serverSockets(container, none), host)
		}
	})
}
