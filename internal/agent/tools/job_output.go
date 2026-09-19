package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/shell"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed job_output.md
var jobOutputDescription string

type JobOutputParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to retrieve output from"`
	Wait    bool   `json:"wait" description:"If true, block until the background shell completes before returning output"`
}

type JobOutputResponseMetadata struct {
	ShellID          string `json:"shell_id"`
	Command          string `json:"command"`
	Description      string `json:"description"`
	Done             bool   `json:"done"`
	WorkingDirectory string `json:"working_directory"`
}

func NewJobOutputTool() fantasy.AgentTool {
	return NewTool(
		toolnames.JobOutput,
		jobOutputDescription,
		func(ctx context.Context, params JobOutputParams, call fantasy.ToolCall) Result {
			if params.ShellID == "" {
				return Fail("missing shell_id")
			}

			bgManager := shell.GetBackgroundShellManager()
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for reading background shell output")
			}
			bgShell, ok := bgManager.Get(params.ShellID, sessionID)
			if !ok {
				return Failf("background shell not found: %s", params.ShellID)
			}

			if params.Wait {
				bgShell.WaitContext(ctx)
			}

			stdout, stderr, done, err := bgShell.GetOutput()

			var outputParts []string
			if stdout != "" {
				outputParts = append(outputParts, stdout)
			}
			if stderr != "" {
				outputParts = append(outputParts, stderr)
			}

			status := "running"
			if done {
				status = "completed"
				if err != nil {
					exitCode := shell.ExitCode(err)
					if exitCode != 0 {
						outputParts = append(outputParts, fmt.Sprintf("Exit code %d", exitCode))
					}
				}
			}

			output := strings.Join(outputParts, "\n")
			output = TruncateOutput(output)

			metadata := JobOutputResponseMetadata{
				ShellID:          params.ShellID,
				Command:          bgShell.Command,
				Description:      bgShell.Description,
				Done:             done,
				WorkingDirectory: bgShell.WorkingDir,
			}

			if output == "" {
				output = BashNoOutput
			}

			result := fmt.Sprintf("Status: %s\n\n%s", status, output)
			return Ok(result).WithMetadata(metadata)
		},
	)
}
