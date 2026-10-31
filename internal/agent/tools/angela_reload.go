package tools

import (
	"context"
	_ "embed"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed angela_reload.md
var angelaReloadDescription string

type AngelaReloadParams struct{}

// NewAngelaReloadTool reloads configuration, skills and MCP servers from
// disk. reload returns the summary shown to the model.
func NewAngelaReloadTool(reload func(context.Context) (string, error)) fantasy.AgentTool {
	return NewTool(
		toolnames.AngelaReload,
		angelaReloadDescription,
		func(ctx context.Context, _ AngelaReloadParams, _ fantasy.ToolCall) Result {
			summary, err := reload(ctx)
			if err != nil {
				return Fail(err.Error())
			}
			return Ok(summary)
		},
	)
}
