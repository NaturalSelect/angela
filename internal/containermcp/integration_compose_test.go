package containermcp

import (
	"bufio"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestIntegration_ComposeProfileInitShmAndHosts(t *testing.T) {
	ep, image := integrationEndpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	dir := t.TempDir()
	seedDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  seed:
    image: `+image+`
    pull_policy: missing
    network_mode: none
    entrypoint: ["sh", "-c", "echo done > /seed/ok"]
    volumes:
      - `+seedDir+`:/seed
  main:
    image: `+image+`
    pull_policy: missing
    profiles: [stdio]
    init: true
    shm_size: 64m
    extra_hosts:
      - "svc.test:10.1.2.3"
      - "gw.test:host-gateway"
    volumes:
      - `+seedDir+`:/seed:ro
    entrypoint:
      - sh
      - -c
      - |
        echo comm=$$(cat /proc/1/comm)
        echo shm=$$(df -k /dev/shm | awk 'NR==2{print $$2}')
        echo host=$$(getent hosts svc.test | awk '{print $$1}')
        echo gateway=$$(getent hosts gw.test | awk '{print $$1}')
        echo seed=$$(cat /seed/ok)
        exec cat
    depends_on:
      seed:
        condition: service_completed_successfully
`)
	g, err := FromCompose(ctx, "integration-profile", "compose.yaml", "main", dir)
	require.NoError(t, err)

	s, err := Open(ctx, ep, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	tr := s.Transport().(*mcp.IOTransport)
	r := bufio.NewReader(tr.Reader)
	got := map[string]string{}
	for range 5 {
		line, err := r.ReadString('\n')
		require.NoError(t, err)
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		got[k] = v
	}
	require.NotEqual(t, "sh", got["comm"], "init runs as PID 1 instead of the entrypoint")
	require.Equal(t, "65536", got["shm"], "shm_size is applied to /dev/shm")
	require.Equal(t, "10.1.2.3", got["host"], "extra_hosts entries resolve")
	require.NotEmpty(t, got["gateway"], "host-gateway resolves")
	require.Equal(t, "done", got["seed"], "the completed dependency ran first and shared its volume")
}

func TestIntegration_OrphanedDependenciesAreSweptOnTheNextStart(t *testing.T) {
	ep, image := integrationEndpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  main:
    image: `+image+`
    pull_policy: missing
    entrypoint: ["cat"]
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
	g, err := FromCompose(ctx, "integration-orphan", "compose.yaml", "main", dir)
	require.NoError(t, err)

	first, err := Open(ctx, ep, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	mainID, depID, firstNet := first.id, first.deps[0].id, first.networkID

	cli, err := client.New(client.WithHost(ep.Host))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	// NOTE: Killing the main container stands in for Angela dying, which leaves the dependency and the network behind.
	_, err = cli.ContainerKill(ctx, mainID, client.ContainerKillOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := cli.ContainerInspect(ctx, mainID, client.ContainerInspectOptions{})
		return cerrdefs.IsNotFound(err)
	}, 30*time.Second, 200*time.Millisecond, "the main container is auto-removed once it exits")
	_, err = cli.ContainerInspect(ctx, depID, client.ContainerInspectOptions{})
	require.NoError(t, err, "the dependency outlives the main container")

	second, err := Open(ctx, ep, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	_, err = cli.ContainerInspect(ctx, depID, client.ContainerInspectOptions{})
	require.True(t, cerrdefs.IsNotFound(err), "the orphaned dependency is removed by the next start, got %v", err)
	nets, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("label", LabelServer+"=integration-orphan")})
	require.NoError(t, err)
	var ids []string
	for _, n := range nets.Items {
		ids = append(ids, n.ID)
	}
	require.Contains(t, ids, second.networkID, "the live group keeps its network")
	require.NotContains(t, ids, firstNet, "the orphaned group's network is removed")
}

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
