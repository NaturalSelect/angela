package acp

import (
	"testing"

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
}
