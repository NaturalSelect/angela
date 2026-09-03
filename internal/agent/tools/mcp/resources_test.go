package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// liveSessionWithResourceHandler is like liveSessionWithCapabilities but lets
// the caller supply the resource's read handler directly. The shared
// capabilities helper's handler returns an empty result (fine for listing,
// which never invokes it), which the SDK rejects as invalid once actually
// read; tests exercising ReadResource need real content instead.
func liveSessionWithResourceHandler(t *testing.T, resourceURI string, handler mcp.ResourceHandler) *ClientSession {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "srv"}, nil)
	server.AddResource(&mcp.Resource{Name: "res", URI: resourceURI}, handler)
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	client := mcp.NewClient(&mcp.Implementation{Name: "angela-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	return &ClientSession{ClientSession: clientSession, cancel: cancel}
}

func TestResources(t *testing.T) {
	t.Parallel()

	const name = "test-resources-getter"
	t.Cleanup(func() { allResources.Del(name) })

	allResources.Set(name, []*Resource{{Name: "r1", URI: "res://r1"}})

	found := false
	for k, v := range Resources() {
		if k == name {
			found = true
			require.Len(t, v, 1)
			require.Equal(t, "r1", v[0].Name)
		}
	}
	require.True(t, found, "Resources() must expose entries registered in allResources")
}

func TestListResources(t *testing.T) {
	const name = "test-list-resources"
	t.Cleanup(func() {
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
		allResources.Del(name)
		states.Del(name)
	})

	sess := liveSessionWithCapabilities(t, "a_tool", "a_prompt", "res://thing")
	sessions.Set(name, sess)
	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

	resources, err := ListResources(context.Background(), cfg, name)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.Equal(t, "res://thing", resources[0].URI)

	got, ok := allResources.Get(name)
	require.True(t, ok, "a successful list must register the resources")
	require.Len(t, got, 1)

	info, ok := GetState(name)
	require.True(t, ok)
	require.Equal(t, StateConnected, info.State)
	require.Equal(t, 1, info.Counts.Resources)
}

func TestReadResource(t *testing.T) {
	const name = "test-read-resource"
	t.Cleanup(func() {
		if s, ok := sessions.Take(name); ok {
			_ = s.Close()
		}
	})

	sess := liveSessionWithResourceHandler(t, "res://thing", func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{URI: "res://thing", Text: "resource body"}},
		}, nil
	})
	sessions.Set(name, sess)
	cfg := config.NewTestStore(&config.Config{MCP: config.MCPs{name: {Type: config.MCPStdio}}})

	contents, err := ReadResource(context.Background(), cfg, name, "res://thing")
	require.NoError(t, err)
	require.Len(t, contents, 1)
	require.Equal(t, "resource body", contents[0].Text)
}

func TestReadResource_NoSession(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{})
	_, err := ReadResource(context.Background(), cfg, "missing", "res://x")
	require.Error(t, err)
}

func TestRefreshResources(t *testing.T) {
	t.Parallel()
	t.Run("no session logs and returns without touching state", func(t *testing.T) {
		t.Parallel()

		const name = "test-refresh-resources-no-session"
		RefreshResources(context.Background(), name)
		_, ok := allResources.Get(name)
		require.False(t, ok)
	})

	t.Run("populates resources and marks connected", func(t *testing.T) {
		const name = "test-refresh-resources-connected"
		t.Cleanup(func() {
			if s, ok := sessions.Take(name); ok {
				_ = s.Close()
			}
			allResources.Del(name)
			states.Del(name)
		})

		sess := liveSessionWithCapabilities(t, "a_tool", "a_prompt", "res://thing")
		sessions.Set(name, sess)

		RefreshResources(context.Background(), name)

		got, ok := allResources.Get(name)
		require.True(t, ok)
		require.Len(t, got, 1)

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateConnected, info.State)
		require.Equal(t, 1, info.Counts.Resources)
	})

	t.Run("list error marks the server errored", func(t *testing.T) {
		const name = "test-refresh-resources-error"
		t.Cleanup(func() {
			if s, ok := sessions.Take(name); ok {
				_ = s.Close()
			}
			states.Del(name)
		})

		sess := liveSessionWithCapabilities(t, "a_tool", "a_prompt", "res://thing")
		require.NoError(t, sess.Close())
		sessions.Set(name, sess)

		RefreshResources(context.Background(), name)

		info, ok := GetState(name)
		require.True(t, ok)
		require.Equal(t, StateError, info.State)
	})
}

func TestIsMethodNotFoundError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "method not found",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "not found"},
			want: true,
		},
		{
			name: "wrapped method not found",
			err:  fmt.Errorf("wrap: %w", &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound}),
			want: true,
		},
		{
			name: "different rpc error code",
			err:  &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams},
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, isMethodNotFoundError(tt.err))
		})
	}
}
