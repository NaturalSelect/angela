package mcp

import (
	"cmp"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// NOTE: A stdio MCP server in plain sh, so the test needs nothing but a shell image.
const shellMCPServer = `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
  *'"method":"initialize"'*)
    printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"shell-mcp","version":"1"}}}\n' "$id" ;;
  *'"method":"tools/list"'*)
    printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"ping","description":"Ping.","inputSchema":{"type":"object"}}]}}\n' "$id" ;;
  *)
    if [ -n "$id" ]; then
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}\n' "$id"
    fi ;;
  esac
done
`

// NOTE: Runs only when ANGELA_TEST_CONTAINER_HOST names a Docker or Podman API endpoint. Not parallel: it uses the package-global state maps.
func TestCreateSession_ContainerOnARealEngine(t *testing.T) {
	host := os.Getenv("ANGELA_TEST_CONTAINER_HOST")
	if host == "" {
		t.Skip("ANGELA_TEST_CONTAINER_HOST is not set")
	}
	image := cmp.Or(os.Getenv("ANGELA_TEST_CONTAINER_IMAGE"), "docker.io/library/alpine:3")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server.sh"), []byte(shellMCPServer), 0o644))

	const name = "box-real-engine"
	cleanupServer(t, name)

	m := config.MCPConfig{
		Type:          config.MCPContainer,
		Image:         image,
		Pull:          "missing",
		Args:          []string{"sh", "/srv/server.sh"},
		Volumes:       []string{dir + ":/srv:ro"},
		Network:       "none",
		ContainerHost: host,
		Timeout:       120,
	}
	sess, err := createSession(t.Context(), nil, name, m, currentGen(name), shellResolverWithPath(t, nil), false)
	require.NoError(t, err)
	require.Equal(t, "shell-mcp", sess.InitializeResult().ServerInfo.Name)

	tools, err := sess.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	require.Equal(t, "ping", tools.Tools[0].Name)

	done := make(chan error, 1)
	go func() { done <- sess.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("closing the session did not return")
	}
}
