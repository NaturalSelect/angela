package tools

import (
	"context"
	_ "embed"
	"fmt"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/shell"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed job_kill.md
var jobKillDescription string

type JobKillParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to terminate"`
}

type JobKillResponseMetadata struct {
	ShellID     string `json:"shell_id"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

func NewJobKillTool() fantasy.AgentTool {
	return NewTool(
		toolnames.JobKill,
		jobKillDescription,
		func(ctx context.Context, params JobKillParams, call fantasy.ToolCall) Result {
			if params.ShellID == "" {
				return Fail("missing shell_id")
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for terminating a background shell")
			}

			bgManager := shell.GetBackgroundShellManager()

			bgShell, ok := bgManager.Get(params.ShellID, sessionID)
			if !ok {
				return Failf("background shell not found: %s", params.ShellID)
			}

			metadata := JobKillResponseMetadata{
				ShellID:     params.ShellID,
				Command:     bgShell.Command,
				Description: bgShell.Description,
			}

			err := bgManager.Kill(params.ShellID, sessionID)
			if err != nil {
				return Fail(err.Error())
			}

			result := fmt.Sprintf("Background shell %s terminated successfully", params.ShellID)
			return Ok(result).WithMetadata(metadata)
		},
	)
}
