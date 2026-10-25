package cmd

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/NaturalSelect/angela/internal/acp"
	"github.com/NaturalSelect/angela/internal/acp/caelis"
	"github.com/NaturalSelect/angela/internal/acp/protocol"
)

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Run as an Agent Client Protocol agent over stdio, for editors such as Zed and Neovim",
	Long: `Run Angela as an Agent Client Protocol (ACP) agent.

The editor starts this command as a child process and talks to it over
stdin and stdout. Nothing else is written to stdout; logs go to Angela's log
file and, before the project is open, to stderr.

One process serves one project: the first session request names its
directory. Branch agents and background agents are not available, and the
question tool is available only when the editor supports forms.`,
	Example: `
# Zed: add to settings.json
#   "agent_servers": { "Angela": { "command": "angela", "args": ["acp"] } }

# Let the agent edit files without asking
angela acp --auto-accept-edits
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		defer serveProfiles()()

		protocolOut := os.Stdout
		// NOTE: Anything that prints to os.Stdout by mistake lands on stderr
		// instead of corrupting the protocol stream.
		os.Stdout = os.Stderr
		defer func() { os.Stdout = protocolOut }()

		// NOTE: Until the workspace is open the file logger does not exist yet.
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

		return runACP(cmd.Context(), os.Stdin, protocolOut, localWorkspaceOptionsFromFlags(cmd, ""))
	},
}

func init() {
	addSandboxFlags(acpCmd)
}

// runACP serves one editor connection until the editor closes its end.
func runACP(ctx context.Context, in io.Reader, out io.Writer, base localWorkspaceOptions) error {
	open := func(ctx context.Context, cwd string, caps acp.ClientCaps) (acp.Workspace, error) {
		return openACPWorkspace(ctx, base, cwd, caps)
	}

	var served *acp.Agent
	err := caelis.Serve(ctx, in, out, func(c protocol.Client) protocol.Agent {
		served = acp.NewAgent(ctx, c, open)
		return served
	})
	if served != nil {
		served.Close()
	}
	return err
}

// openACPWorkspace builds the one workspace of an ACP process.
//
// NOTE: The process changes into the project directory. The editor starts
// the agent from wherever it likes, and a few places read the process
// directory directly, such as a shell command without an explicit one. The
// agent refuses a second project, so this happens at most once.
func openACPWorkspace(ctx context.Context, base localWorkspaceOptions, cwd string, caps acp.ClientCaps) (acp.Workspace, error) {
	if err := os.Chdir(cwd); err != nil {
		return nil, err
	}

	o := base
	o.Cwd = cwd
	o.NonInteractive = !caps.Elicitation
	o.NoBranchAgents = true

	ws, _, err := openLocalWorkspace(ctx, o)
	if err != nil {
		return nil, err
	}
	return ws, nil
}
