package containermcp

import (
	"bufio"
	"cmp"
	"context"
	"io"
	"os"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// NOTE: Runs only when ANGELA_TEST_CONTAINER_HOST names a Docker or Podman API endpoint.
func integrationEndpoint(t *testing.T) (Endpoint, string) {
	t.Helper()
	host := os.Getenv("ANGELA_TEST_CONTAINER_HOST")
	if host == "" {
		t.Skip("ANGELA_TEST_CONTAINER_HOST is not set")
	}
	ep, err := parseEndpoint(host)
	require.NoError(t, err)
	return ep, cmp.Or(os.Getenv("ANGELA_TEST_CONTAINER_IMAGE"), "docker.io/library/alpine:3")
}

func TestIntegration_RoundTripAndCleanup(t *testing.T) {
	ep, image := integrationEndpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	s, err := Open(ctx, ep, Group{
		Server: "integration",
		Main:   Service{Name: "integration", Image: image, Entrypoint: []string{"cat"}, Pull: PullMissing},
	})
	require.NoError(t, err)
	id := s.id

	tr := s.Transport().(*mcp.IOTransport)
	_, err = io.WriteString(tr.Writer, "hello\n")
	require.NoError(t, err)
	line, err := bufio.NewReader(tr.Reader).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "hello\n", line)

	require.NoError(t, s.Close())

	cli, err := client.New(client.WithHost(ep.Host))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	_, err = cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	require.True(t, cerrdefs.IsNotFound(err), "the container is removed after Close, got %v", err)
}

func TestIntegration_ExitCodeAndStderr(t *testing.T) {
	ep, image := integrationEndpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	s, err := Open(ctx, ep, Group{
		Server: "integration",
		Main: Service{
			Name:       "integration",
			Image:      image,
			Entrypoint: []string{"sh", "-c", "echo oops >&2; exit 7"},
			Pull:       PullMissing,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	tr := s.Transport().(*mcp.IOTransport)
	_, err = io.ReadAll(tr.Reader)
	require.NoError(t, err)

	exitErr := s.ExitErr()
	require.Error(t, exitErr)
	require.ErrorContains(t, exitErr, "code 7")
	require.ErrorContains(t, exitErr, "oops")
}
