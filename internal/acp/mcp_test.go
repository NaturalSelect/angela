package acp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
)

func TestMCPConfigs(t *testing.T) {
	t.Parallel()

	t.Run("converts each transport", func(t *testing.T) {
		t.Parallel()
		got := mcpConfigs([]protocol.MCPServer{
			{
				Name: "files", Transport: protocol.MCPTransportStdio,
				Command: "/usr/bin/files", Args: []string{"--root", "."},
				Env: []protocol.NameValue{{Name: "TOKEN", Value: "abc"}},
			},
			{
				Name: "docs", Transport: protocol.MCPTransportHTTP,
				URL:     "https://example.com/mcp",
				Headers: []protocol.NameValue{{Name: "Authorization", Value: "Bearer x"}},
			},
			{Name: "events", Transport: protocol.MCPTransportSSE, URL: "https://example.com/sse"},
		})
		require.Equal(t, map[string]config.MCPConfig{
			"files": {
				Type: config.MCPStdio, Command: "/usr/bin/files", Args: []string{"--root", "."},
				Env: map[string]string{"TOKEN": "abc"},
			},
			"docs": {
				Type: config.MCPHttp, URL: "https://example.com/mcp",
				Headers: map[string]string{"Authorization": "Bearer x"},
			},
			"events": {Type: config.MCPSSE, URL: "https://example.com/sse"},
		}, got)
	})

	t.Run("names are made safe for tool names", func(t *testing.T) {
		t.Parallel()
		got := mcpConfigs([]protocol.MCPServer{
			{Name: "my server/v2", Transport: protocol.MCPTransportStdio, Command: "run"},
		})
		require.Contains(t, got, "my-server-v2")
		require.Len(t, got, 1)
	})

	t.Run("servers without a usable name or address are dropped", func(t *testing.T) {
		t.Parallel()
		got := mcpConfigs([]protocol.MCPServer{
			{Name: "", Transport: protocol.MCPTransportStdio, Command: "run"},
			{Name: "no-command", Transport: protocol.MCPTransportStdio},
			{Name: "no-url", Transport: protocol.MCPTransportHTTP},
			{Name: "no-sse-url", Transport: protocol.MCPTransportSSE},
			{Name: "ok", Transport: protocol.MCPTransportStdio, Command: "run"},
		})
		require.Len(t, got, 1)
		require.Contains(t, got, "ok")
	})

	t.Run("no servers", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, mcpConfigs(nil))
	})

	t.Run("names that collide once sanitized keep the first", func(t *testing.T) {
		t.Parallel()
		got := mcpConfigs([]protocol.MCPServer{
			{Name: "my server", Transport: protocol.MCPTransportStdio, Command: "first"},
			{Name: "my-server", Transport: protocol.MCPTransportStdio, Command: "second"},
		})
		require.Len(t, got, 1)
		require.Equal(t, "first", got["my-server"].Command)
	})

	t.Run("a name with no letter or digit left is dropped", func(t *testing.T) {
		t.Parallel()
		got := mcpConfigs([]protocol.MCPServer{
			{Name: "日本語", Transport: protocol.MCPTransportStdio, Command: "first"},
			{Name: "한국어", Transport: protocol.MCPTransportStdio, Command: "second"},
			{Name: "___", Transport: protocol.MCPTransportStdio, Command: "third"},
			{Name: "-", Transport: protocol.MCPTransportStdio, Command: "fourth"},
			{Name: "ok", Transport: protocol.MCPTransportStdio, Command: "run"},
		})
		require.Len(t, got, 1)
		require.Contains(t, got, "ok")
	})

	t.Run("an unknown transport is dropped", func(t *testing.T) {
		t.Parallel()
		got := mcpConfigs([]protocol.MCPServer{
			{Name: "odd", Transport: protocol.MCPTransport(99), Command: "run", URL: "https://example.com"},
		})
		require.Empty(t, got)
	})
}

func TestEditorMCPServersAreAttached(t *testing.T) {
	t.Parallel()

	servers := []protocol.MCPServer{
		{Name: "docs", Transport: protocol.MCPTransportHTTP, URL: "https://example.com/mcp"},
	}
	want := []map[string]config.MCPConfig{
		{"docs": {Type: config.MCPHttp, URL: "https://example.com/mcp"}},
	}
	attached := func(h *harness) (got []map[string]config.MCPConfig) {
		h.ws.snapshot(func() { got = append(got, h.ws.attached...) })
		return got
	}

	t.Run("new session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd, MCPServers: servers})
		require.NoError(t, err)
		require.Equal(t, want, attached(h))
	})

	t.Run("load session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "old"})
		_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "old", Cwd: testCwd, MCPServers: servers})
		require.NoError(t, err)
		require.Equal(t, want, attached(h))
	})

	t.Run("resume session", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "old"})
		_, err := h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "old", Cwd: testCwd, MCPServers: servers})
		require.NoError(t, err)
		require.Equal(t, want, attached(h))
	})

	t.Run("a session without servers attaches nothing", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.newSession(t)
		require.Empty(t, attached(h))
	})

	t.Run("a refused load attaches nothing", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.addSession(session.Session{ID: "child", ParentSessionID: "root"})
		h.ws.addSession(session.Session{ID: "busy"})
		h.ws.busy["busy"] = true

		for _, id := range []string{"missing", "child", "busy"} {
			_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: id, Cwd: testCwd, MCPServers: servers})
			require.Error(t, err, id)
			_, err = h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: id, Cwd: testCwd, MCPServers: servers})
			require.Error(t, err, id)
		}
		require.Empty(t, attached(h), "servers must not start for a request that is then refused")
	})
}

func TestAttachMCPWaitIsBounded(t *testing.T) {
	t.Parallel()

	servers := []protocol.MCPServer{
		{Name: "slow", Transport: protocol.MCPTransportHTTP, URL: "https://example.com/mcp"},
	}
	stuck := func(t *testing.T) *harness {
		t.Helper()
		h := newHarness(t)
		h.ws.attachSettled = make(chan struct{})
		h.agent.mcpWait = time.Minute
		return h
	}
	returns := func(t *testing.T, run func()) <-chan struct{} {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			run()
		}()
		return done
	}
	requireDone := func(t *testing.T, done <-chan struct{}, msg string) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal(msg)
		}
	}

	t.Run("a server that never settles does not hold the request past the wait", func(t *testing.T) {
		t.Parallel()
		h := stuck(t)
		h.agent.mcpWait = 50 * time.Millisecond
		done := returns(t, func() { h.agent.attachMCP(t.Context(), h.ws, servers) })
		requireDone(t, done, "attachMCP outlived its wait")
	})

	t.Run("a cancelled request stops waiting", func(t *testing.T) {
		t.Parallel()
		h := stuck(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := returns(t, func() { h.agent.attachMCP(ctx, h.ws, servers) })
		select {
		case <-done:
			t.Fatal("attachMCP returned before the servers settled or the request ended")
		case <-time.After(50 * time.Millisecond):
		}
		cancel()
		requireDone(t, done, "attachMCP ignored the cancelled request")
	})

	t.Run("servers that settle release the request at once", func(t *testing.T) {
		t.Parallel()
		h := stuck(t)
		done := returns(t, func() { h.agent.attachMCP(t.Context(), h.ws, servers) })
		close(h.ws.attachSettled)
		requireDone(t, done, "attachMCP did not return after the servers settled")
	})

	t.Run("new session returns while a server is still connecting", func(t *testing.T) {
		t.Parallel()
		h := stuck(t)
		h.agent.mcpWait = 50 * time.Millisecond
		var resp protocol.NewSessionResponse
		var err error
		done := returns(t, func() {
			resp, err = h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd, MCPServers: servers})
		})
		requireDone(t, done, "session/new hung on a server that never connects")
		require.NoError(t, err)
		require.NotEmpty(t, resp.SessionID)
	})
}
