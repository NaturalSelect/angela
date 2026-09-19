package tools

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

type ListMCPResourcesParams struct {
	MCPName string `json:"mcp_name" description:"The MCP server name"`
}

type ListMCPResourcesPermissionsParams struct {
	MCPName string `json:"mcp_name"`
}

//go:embed list_mcp_resources.md
var listMCPResourcesDescription string

func NewListMCPResourcesTool(cfg *config.ConfigStore) fantasy.AgentTool {
	return NewParallelTool(
		toolnames.ListMCPResources,
		listMCPResourcesDescription,
		func(ctx context.Context, params ListMCPResourcesParams, call fantasy.ToolCall) Result {
			params.MCPName = strings.TrimSpace(params.MCPName)
			if params.MCPName == "" {
				return Fail("mcp_name parameter is required")
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for listing MCP resources")
			}

			resources, err := mcp.ListResources(ctx, cfg, params.MCPName)
			if err != nil {
				return Fail(err.Error())
			}
			if len(resources) == 0 {
				return Ok("No resources found")
			}

			lines := make([]string, 0, len(resources))
			for _, resource := range resources {
				if resource == nil {
					continue
				}
				title := cmp.Or(resource.Title, resource.Name, resource.URI)
				line := fmt.Sprintf("- %s", title)
				if resource.URI != "" {
					line = fmt.Sprintf("%s (%s)", line, resource.URI)
				}
				if resource.Description != "" {
					line = fmt.Sprintf("%s: %s", line, resource.Description)
				}
				if resource.MIMEType != "" {
					line = fmt.Sprintf("%s [mime: %s]", line, resource.MIMEType)
				}
				if resource.Size > 0 {
					line = fmt.Sprintf("%s [size: %d]", line, resource.Size)
				}
				lines = append(lines, line)
			}

			sort.Strings(lines)
			return Ok(strings.Join(lines, "\n"))
		},
	)
}
