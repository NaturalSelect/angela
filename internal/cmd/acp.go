package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
directory, so --cwd is not accepted. Branch agents and background agents are
not available, and the question tool is available only when the editor
supports forms.`,
	Example: `
# Zed: add to settings.json
#   "agent_servers": { "Angela": { "command": "angela", "args": ["acp"] } }

# Let the agent edit files without asking
angela acp --auto-accept-edits
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// NOTE: --cwd is a global flag, so cobra accepts it here. The editor
		// names the directory in its first session request, so a value given
		// here would be ignored without a word.
		if cwd, _ := cmd.Flags().GetString("cwd"); cwd != "" {
			return errors.New("angela acp does not take --cwd: the editor names the project directory in its first session request")
		}

		defer serveProfiles()()

		protocolOut := os.Stdout
		// NOTE: Anything that prints to os.Stdout by mistake lands on stderr
		// instead of corrupting the protocol stream.
		os.Stdout = os.Stderr
		defer func() { os.Stdout = protocolOut }()

		// NOTE: Until the workspace is open the file logger does not exist yet.
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

		// NOTE: fang only turns SIGINT into a cancelled context. An editor
		// that stops its child with SIGTERM would otherwise kill the process
		// before the workspace flushes pending message updates and stops
		// its background shells and language servers.
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return runACP(ctx, os.Stdin, protocolOut, localWorkspaceOptionsFromFlags(cmd, ""))
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
// agent refuses a second project, so the directory is the same on every call.
// It is called again after any failed attempt, because the agent does not
// remember one. A failure must therefore leave nothing behind that the retry
// would trip over: a missing provider fails before anything starts, and a
// failed build gives its database connection back to the pool.
func openACPWorkspace(ctx context.Context, base localWorkspaceOptions, cwd string, caps acp.ClientCaps) (acp.Workspace, error) {
	if err := os.Chdir(cwd); err != nil {
		return nil, err
	}

	o := base
	o.Cwd = cwd
	o.NonInteractive = !caps.Elicitation
	o.NoBranchAgents = true
	o.RequireProvider = true

	ws, _, err := openLocalWorkspace(ctx, o)
	if err != nil {
		return nil, err
	}
	return ws, nil
}
