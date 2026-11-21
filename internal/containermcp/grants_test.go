package containermcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
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

func TestServerSockets(t *testing.T) {
	sock := shortSocket(t)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	res := func(string) config.VariableResolver { return config.IdentityResolver() }
	none := func() []string { return nil }

	t.Run("explicit host of an existing socket", func(t *testing.T) {
		servers := config.MCPs{"a": {Type: config.MCPContainer, Image: "i", ContainerHost: "unix://" + sock}}
		require.Equal(t, []string{sock}, serverSockets(servers, res, none))
	})
	t.Run("discovered socket when no host is set", func(t *testing.T) {
		servers := config.MCPs{"a": {Type: config.MCPContainer, Image: "i"}}
		require.Equal(t, []string{sock}, serverSockets(servers, res, func() []string { return []string{sock} }))
	})
	t.Run("ignores other types and disabled servers", func(t *testing.T) {
		servers := config.MCPs{
			"stdio": {Type: config.MCPStdio, Command: "x", ContainerHost: "unix://" + sock},
			"off":   {Type: config.MCPContainer, Image: "i", Disabled: true, ContainerHost: "unix://" + sock},
		}
		require.Empty(t, serverSockets(servers, res, none))
	})
	t.Run("ignores missing sockets and tcp hosts", func(t *testing.T) {
		servers := config.MCPs{
			"gone": {Type: config.MCPContainer, Image: "i", ContainerHost: "unix://" + filepath.Join(t.TempDir(), "nope.sock")},
			"tcp":  {Type: config.MCPContainer, Image: "i", ContainerHost: "tcp://127.0.0.1:2375"},
			"bad":  {Type: config.MCPContainer, Image: "i", ContainerHost: "ftp://x"},
		}
		require.Empty(t, serverSockets(servers, res, none))
	})
}
