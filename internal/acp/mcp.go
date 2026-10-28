package acp

import (
	"regexp"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/config"
)

// NOTE: A server name becomes part of a tool name that goes to the model,
// and providers reject anything outside this set.
var invalidMCPNameChars = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

// mcpConfigs turns the servers an editor passes into Angela's own MCP
// config, keyed by a name that is safe to use in a tool name. A server
// without a usable name or address is dropped.
func mcpConfigs(servers []protocol.MCPServer) map[string]config.MCPConfig {
	out := make(map[string]config.MCPConfig, len(servers))
	for _, s := range servers {
		name := invalidMCPNameChars.ReplaceAllString(s.Name, "-")
		if name == "" {
			continue
		}
		switch s.Transport {
		case protocol.MCPTransportStdio:
			if s.Command == "" {
				continue
			}
			out[name] = config.MCPConfig{
				Type:    config.MCPStdio,
				Command: s.Command,
				Args:    s.Args,
				Env:     nameValues(s.Env),
			}
		case protocol.MCPTransportHTTP, protocol.MCPTransportSSE:
			if s.URL == "" {
				continue
			}
			typ := config.MCPHttp
			if s.Transport == protocol.MCPTransportSSE {
				typ = config.MCPSSE
			}
			out[name] = config.MCPConfig{Type: typ, URL: s.URL, Headers: nameValues(s.Headers)}
		}
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

// attachMCP gives the workspace the servers an editor passed with a session
// request. It runs on the connection context, because the sessions it starts
// must outlive the request.
func (a *Agent) attachMCP(ws Workspace, servers []protocol.MCPServer) {
	configs := mcpConfigs(servers)
	if len(configs) == 0 {
		return
	}
	ws.AttachMCP(a.ctx, configs)
}
