package tools

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

type ReadMCPResourceParams struct {
	MCPName string `json:"mcp_name" description:"The MCP server name"`
	URI     string `json:"uri" description:"The resource URI to read"`
}

type ReadMCPResourcePermissionsParams struct {
	MCPName string `json:"mcp_name"`
	URI     string `json:"uri"`
}

//go:embed read_mcp_resource.md
var readMCPResourceDescription string

func NewReadMCPResourceTool(cfg *config.ConfigStore) fantasy.AgentTool {
	return NewParallelTool(
		toolnames.ReadMCPResource,
		readMCPResourceDescription,
		func(ctx context.Context, params ReadMCPResourceParams, call fantasy.ToolCall) Result {
			params.MCPName = strings.TrimSpace(params.MCPName)
			params.URI = strings.TrimSpace(params.URI)
			if params.MCPName == "" {
				return Fail("mcp_name parameter is required")
			}
			if params.URI == "" {
				return Fail("uri parameter is required")
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for reading MCP resources")
			}

			contents, err := mcp.ReadResource(ctx, cfg, params.MCPName, params.URI)
			if err != nil {
				return Fail(err.Error())
			}
			if len(contents) == 0 {
				return Ok("")
			}

			var textParts []string
			for _, content := range contents {
				if content == nil {
					continue
				}
				if content.Text != "" {
					textParts = append(textParts, content.Text)
					continue
				}
				if len(content.Blob) > 0 {
					textParts = append(textParts, string(content.Blob))
					continue
				}
				slog.Debug("MCP resource content missing text/blob", "uri", content.URI)
			}

			if len(textParts) == 0 {
				return Ok("")
			}

			return Ok(strings.Join(textParts, "\n"))
		},
	)
}
