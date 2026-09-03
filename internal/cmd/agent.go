package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

func init() {
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentCreateCmd)
	rootCmd.AddCommand(agentCmd)
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agents",
	Long:  "List, inspect, and manage agent configurations.",
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured agents",
	Example: `# List all agents
angela agent list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}

		dataDir, _ := cmd.Flags().GetString("data-dir")
		debug, _ := cmd.Flags().GetBool("debug")

		cfg, err := config.Init(cwd, dataDir, debug)
		if err != nil {
			return err
		}

		agents := cfg.Config().Agents
		if len(agents) == 0 {
			fmt.Println("No agents configured.")
			return nil
		}

		// Sort by ID for stable output.
		ids := make([]string, 0, len(agents))
		for id := range agents {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		// Header.
		fmt.Printf("%-15s %-12s %-8s %s\n", "ID", "MODE", "MODEL", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 70))

		for _, id := range ids {
			a := agents[id]
			mode := string(a.Mode)
			if mode == "" {
				mode = string(config.AgentModeSubagent)
			}
			model := string(a.Slot)
			if model == "" {
				model = string(config.SlotMain)
			}
			fmt.Printf("%-15s %-12s %-8s %s\n", id, mode, model, truncateDescription(a.Description))
		}

		return nil
	},
}

// truncateDescription shortens a description to fit the list column.
// Slicing by byte offset would cut a multi-byte rune in half and emit
// invalid UTF-8; ansi.Truncate measures display width, which is also
// what the column is actually made of.
func truncateDescription(desc string) string {
	return ansi.Truncate(desc, 50, "...")
}

var agentCreateCmd = &cobra.Command{
	Use:   "create <description>",
	Short: "Generate a new agent from a natural language description",
	Long: `Generate a new subagent definition using the configured LLM and write
it to .angela/agents/<id>.md.

Runs locally and requires an already configured provider.`,
	Args: cobra.ExactArgs(1),
	Example: `# Generate an agent
angela agent create "Reviews Go code for concurrency bugs"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, cleanup, err := setupLocalWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		appWs := ws.(*workspace.AppWorkspace)

		agent, path, err := appWs.App().AgentCoordinator.GenerateAgent(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Created agent %q (mode: %s)\n", agent.ID, agent.Mode)
		fmt.Printf("Written to: %s\n", path)

		return nil
	},
}
