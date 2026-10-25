package cmd

import (
	"cmp"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

var (
	logoutHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	logoutItemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	logoutPromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("215"))
)

var logoutCmd = &cobra.Command{
	Aliases: []string{"signout"},
	Use:     "logout [platform]",
	Short:   "Logout Angela from a platform",
	Long: `Logout Angela from a specified platform, removing stored credentials.
The platform should be provided as an argument.
If no argument is given, a list of logged-in platforms will be shown.
Available platforms are: copilot.`,
	Example: `
# Sign out from GitHub Copilot
angela logout copilot
  `,
	ValidArgs: []cobra.Completion{
		"copilot",
		"github",
		"github-copilot",
	},
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer exitOnInterrupt()()

		ws, cleanup, err := setupLocalWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		progressEnabled := ws.Config().Options.Progress == nil || *ws.Config().Options.Progress
		if progressEnabled && supportsProgressBar() {
			_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
			defer func() { _, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar) }()
		}

		var provider string
		if len(args) == 0 {
			provider, err = pickLoggedInProvider(ws.Config())
			if err != nil {
				return err
			}
			if provider == "" {
				return nil
			}
		} else {
			provider = args[0]
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Print(logoutPromptStyle.Render(fmt.Sprintf("Are you sure you want to logout %s? (y/N) ", provider)))
			var response string
			_, err := fmt.Scanln(&response)
			if err != nil || (response != "y" && response != "Y" && response != "yes" && response != "Yes" && response != "YES") {
				fmt.Println(logoutHeaderStyle.Render("Logout cancelled."))
				return nil
			}
		}

		switch provider {
		case "copilot", "github", "github-copilot":
			return logoutCopilot(ws)
		default:
			return fmt.Errorf("unknown platform: %s", provider)
		}
	},
}

func logoutCopilot(ws workspace.Workspace) error {
	if err := cmp.Or(
		ws.RemoveConfigField(config.ScopeGlobal, "providers.copilot.api_key"),
		ws.RemoveConfigField(config.ScopeGlobal, "providers.copilot.oauth"),
	); err != nil {
		return err
	}

	fmt.Println(logoutHeaderStyle.Render("Successfully logged out of GitHub Copilot."))
	return nil
}

func pickLoggedInProvider(cfg *config.Config) (string, error) {
	type loggedInProvider struct {
		id   string
		name string
	}

	// Only OAuth-based providers support login/logout. Keep this list in sync
	// with the switch in RunE and the login command.
	oauthProviders := map[string]string{
		"copilot": "GitHub Copilot",
	}

	var loggedIn []loggedInProvider
	for id, name := range oauthProviders {
		if p, ok := cfg.Providers.Get(id); ok && p.OAuthToken != nil {
			loggedIn = append(loggedIn, loggedInProvider{id: id, name: name})
		}
	}

	if len(loggedIn) == 0 {
		fmt.Println(logoutPromptStyle.Render("You are not logged in to any platform."))
		return "", nil
	}

	if len(loggedIn) == 1 {
		return loggedIn[0].id, nil
	}

	fmt.Println(logoutHeaderStyle.Render("Logged-in platforms:"))
	for i, p := range loggedIn {
		fmt.Println(logoutItemStyle.Render(fmt.Sprintf("  %d. %s", i+1, p.name)))
	}
	fmt.Print(logoutPromptStyle.Render(fmt.Sprintf("Select a platform to logout (1-%d): ", len(loggedIn))))

	var choice int
	_, err := fmt.Scanln(&choice)
	if err != nil || choice < 1 || choice > len(loggedIn) {
		fmt.Println(logoutHeaderStyle.Render("Logout cancelled."))
		return "", nil
	}

	return loggedIn[choice-1].id, nil
}

func init() {
	logoutCmd.Flags().BoolP("force", "f", false, "Skip logout confirmation prompt")
}

// exitOnInterrupt ends the process on Ctrl-C until the returned function is
// called.
//
// NOTE: The prompts read stdin with fmt.Scanln, which nothing can wake up,
// and the root command turns SIGINT into a context nobody here reads. The
// process has to exit itself or Ctrl-C does nothing until Enter is pressed.
func exitOnInterrupt() (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	stopWatching := exitOnSignal(signals, os.Exit)
	return func() {
		signal.Stop(signals)
		stopWatching()
	}
}

func exitOnSignal(signals <-chan os.Signal, exit func(code int)) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			select {
			case <-done:
			default:
				exit(1)
			}
		case <-done:
		}
	}()
	return sync.OnceFunc(func() { close(done) })
}
