package acp

import (
	"context"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/config"
)

// NOTE: A server name becomes part of a tool name that goes to the model,
// and providers reject anything outside this set.
var invalidMCPNameChars = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

// mcpConfigs turns the servers an editor passes into Angela's own MCP
// config, keyed by a name that is safe to use in a tool name. A server
// without a usable name or address is dropped, and so is one whose name
// matches an earlier server once sanitized. Each drop is logged, because the
// editor shows nothing when a server's tools never appear.
func mcpConfigs(servers []protocol.MCPServer) map[string]config.MCPConfig {
	out := make(map[string]config.MCPConfig, len(servers))
	origin := make(map[string]string, len(servers))
	for _, s := range servers {
		name := invalidMCPNameChars.ReplaceAllString(s.Name, "-")
		if name == "" {
			slog.Warn("Editor MCP server ignored: its name has no usable characters", "name", s.Name)
			continue
		}
		var cfg config.MCPConfig
		switch s.Transport {
		case protocol.MCPTransportStdio:
			if s.Command == "" {
				slog.Warn("Editor MCP server ignored: it has no command", "name", s.Name)
				continue
			}
			cfg = config.MCPConfig{
				Type:    config.MCPStdio,
				Command: s.Command,
				Args:    s.Args,
				Env:     nameValues(s.Env),
			}
		case protocol.MCPTransportHTTP, protocol.MCPTransportSSE:
			if s.URL == "" {
				slog.Warn("Editor MCP server ignored: it has no URL", "name", s.Name)
				continue
			}
			typ := config.MCPHttp
			if s.Transport == protocol.MCPTransportSSE {
				typ = config.MCPSSE
			}
			cfg = config.MCPConfig{Type: typ, URL: s.URL, Headers: nameValues(s.Headers)}
		default:
			slog.Warn("Editor MCP server ignored: its transport is not supported", "name", s.Name)
			continue
		}
		if first, dup := origin[name]; dup {
			slog.Warn("Editor MCP server ignored: an earlier server has the same name once sanitized",
				"name", s.Name, "kept", first, "as", name)
			continue
		}
		origin[name] = s.Name
		out[name] = cfg
	}
	return out
}

func nameValues(pairs []protocol.NameValue) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.Name] = p.Value
	}
	return out
}

// attachMCP starts the servers an editor passed with a session request and
// waits for them, so the first prompt can see their tools. The wait is cut
// short when the request ends or after mcpWait. A server that is not ready by
// then keeps connecting, and its tools reach later runs.
//
// NOTE: The servers are started on the connection context, not the request
// context, because their sessions must outlive the request.
func (a *Agent) attachMCP(ctx context.Context, ws Workspace, servers []protocol.MCPServer) {
	configs := mcpConfigs(servers)
	if len(configs) == 0 {
		return
	}
	settled := ws.AttachMCP(a.ctx, configs)

	timer := time.NewTimer(a.mcpWait)
	defer timer.Stop()
	select {
	case <-settled:
	case <-ctx.Done():
	case <-timer.C:
		slog.Warn("Editor MCP servers are still connecting; their tools will appear once they are ready",
			"servers", slices.Sorted(maps.Keys(configs)), "waited", a.mcpWait)
	}
}
