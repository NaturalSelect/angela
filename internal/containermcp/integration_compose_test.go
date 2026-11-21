package containermcp

import (
	"bufio"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestIntegration_ComposeGroup(t *testing.T) {
	ep, image := integrationEndpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  main:
    image: `+image+`
    pull_policy: missing
    entrypoint: ["sh", "-c", "getent hosts dep >/dev/null && echo resolved; cat"]
    depends_on:
      dep:
        condition: service_healthy
  dep:
    image: `+image+`
    pull_policy: missing
    entrypoint: ["sleep", "300"]
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 2s
      retries: 3
`)
	g, err := FromCompose(ctx, "integration-group", "compose.yaml", "main", dir)
	require.NoError(t, err)

	s, err := Open(ctx, ep, g)
	require.NoError(t, err)
	mainID, depID, networkID := s.id, s.deps[0].id, s.networkID
	require.NotEmpty(t, networkID)

	tr := s.Transport().(*mcp.IOTransport)
	r := bufio.NewReader(tr.Reader)
	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "resolved\n", line, "the main service reaches the dependency by its service name")
	_, err = io.WriteString(tr.Writer, "ping\n")
	require.NoError(t, err)
	line, err = r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ping\n", line)

	require.NoError(t, s.Close())

	cli, err := client.New(client.WithHost(ep.Host))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	for name, id := range map[string]string{"main": mainID, "dependency": depID} {
		_, err = cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		require.True(t, cerrdefs.IsNotFound(err), "the %s container is removed after Close, got %v", name, err)
	}
	nets, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("label", LabelServer+"=integration-group")})
	require.NoError(t, err)
	require.Empty(t, nets.Items, "the group network is removed after Close")
}
