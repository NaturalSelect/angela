package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
)

// httpMCPServer starts an in-process MCP server over streamable HTTP that
// offers one tool.
func httpMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "editor-srv"}, nil)
	mcp.AddTool(
		server,
		&mcp.Tool{Name: "ping", Description: "test tool"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
		},
	)
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)
	return srv
}

func requireSettled(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("MCP attempt did not settle")
	}
}

// NOTE: Not parallel, because newTestConfigStore calls t.Setenv and MCP state
// is process-global.
func TestAppWorkspace_AttachMCP_ConnectsAnHTTPServer(t *testing.T) {
	const name = "editor-docs"
	store := newTestConfigStore(t)
	ws := NewAppWorkspace(nil, store)

	srv := httpMCPServer(t)
	t.Cleanup(func() { _ = mcptools.DisableSingle(store, name) })
	settled := ws.AttachMCP(t.Context(), map[string]config.MCPConfig{
		name: {Type: config.MCPHttp, URL: srv.URL},
	})
	requireSettled(t, settled)

	info, ok := mcptools.GetState(name)
	require.True(t, ok)
	require.Equal(t, mcptools.StateConnected, info.State, "error: %v", info.Error)
	require.Equal(t, 1, info.Counts.Tools)
	require.True(t, store.IsRuntimeMCP(name))
}

// NOTE: A later session that passes a different config for a running editor
// server must not close the connection the earlier session is using.
func TestAppWorkspace_AttachMCP_KeepsARunningServerWhenAnotherConfigArrives(t *testing.T) {
	const name = "editor-shared"
	store := newTestConfigStore(t)
	ws := NewAppWorkspace(nil, store)

	first := httpMCPServer(t)
	other := httpMCPServer(t)
	t.Cleanup(func() { _ = mcptools.DisableSingle(store, name) })

	requireSettled(t, ws.AttachMCP(t.Context(), map[string]config.MCPConfig{
		name: {Type: config.MCPHttp, URL: first.URL},
	}))
	before, ok := mcptools.GetState(name)
	require.True(t, ok)
	require.Equal(t, mcptools.StateConnected, before.State, "error: %v", before.Error)

	requireSettled(t, ws.AttachMCP(t.Context(), map[string]config.MCPConfig{
		name: {Type: config.MCPHttp, URL: other.URL},
	}))

	after, ok := mcptools.GetState(name)
	require.True(t, ok)
	require.Equal(t, mcptools.StateConnected, after.State)
	require.Equal(t, first.URL, after.Config.URL, "the running server keeps its config")
	require.Equal(t, first.URL, store.Config().MCP[name].URL)
	require.True(t, after.ConnectedAt.Equal(before.ConnectedAt), "the connection was not restarted")
}

func TestAppWorkspace_AttachMCP_SameConfigAgainIsHarmless(t *testing.T) {
	const name = "editor-again"
	store := newTestConfigStore(t)
	ws := NewAppWorkspace(nil, store)

	srv := httpMCPServer(t)
	t.Cleanup(func() { _ = mcptools.DisableSingle(store, name) })
	servers := map[string]config.MCPConfig{name: {Type: config.MCPHttp, URL: srv.URL}}

	requireSettled(t, ws.AttachMCP(t.Context(), servers))
	before, _ := mcptools.GetState(name)
	requireSettled(t, ws.AttachMCP(t.Context(), servers))
	after, _ := mcptools.GetState(name)

	require.Equal(t, mcptools.StateConnected, after.State)
	require.True(t, after.ConnectedAt.Equal(before.ConnectedAt), "the connection was not restarted")
}

// countingMCPServer is httpMCPServer that also counts the requests it gets.
func countingMCPServer(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "editor-srv"}, nil)
	mcp.AddTool(
		server,
		&mcp.Tool{Name: "ping", Description: "test tool"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
		},
	)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// NOTE: Sessions that disagree about one server's config, arriving together,
// must not each register their own. Only the first may start, or the second
// restart would close the connection the first session is waiting on.
func TestAppWorkspace_AttachMCP_ConcurrentConfigsStartOneServer(t *testing.T) {
	const (
		name     = "editor-racing"
		sessions = 6
	)
	store := newTestConfigStore(t)
	ws := NewAppWorkspace(nil, store)
	t.Cleanup(func() { _ = mcptools.DisableSingle(store, name) })

	hits := make([]atomic.Int64, sessions)
	urls := make([]string, sessions)
	for i := range sessions {
		urls[i] = countingMCPServer(t, &hits[i]).URL
	}

	start := make(chan struct{})
	settled := make([]<-chan struct{}, sessions)
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			settled[i] = ws.AttachMCP(t.Context(), map[string]config.MCPConfig{
				name: {Type: config.MCPHttp, URL: urls[i]},
			})
		}()
	}
	close(start)
	wg.Wait()
	for _, ch := range settled {
		requireSettled(t, ch)
	}

	info, ok := mcptools.GetState(name)
	require.True(t, ok)
	require.Equal(t, mcptools.StateConnected, info.State, "error: %v", info.Error)
	require.Equal(t, info.Config.URL, store.Config().MCP[name].URL, "the store and the running server agree")

	contacted := 0
	for i := range hits {
		if hits[i].Load() > 0 {
			contacted++
		}
	}
	require.Equal(t, 1, contacted, "only one of the competing servers may be started")
}
