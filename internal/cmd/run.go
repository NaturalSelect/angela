package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"charm.land/log/v2"
	"github.com/NaturalSelect/angela/internal/event"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Aliases: []string{"r"},
	Use:     "run [prompt...]",
	Short:   "Run a single non-interactive prompt",
	Long: `Run a single prompt in non-interactive mode and exit.
The prompt can be provided as arguments or piped from stdin.`,
	Example: `
# Run a simple prompt
angela run "Guess my 5 favorite Pokémon"

# Pipe input from stdin
curl https://charm.land | angela run "Summarize this website"

# Read from a file
angela run "What is this code doing?" <<< prrr.go

# Redirect output to a file
angela run "Generate a hot README for this project" > MY_HOT_README.md

# Save the final response to a file (stdout still shows the whole conversation)
angela run --output MY_HOT_README.md "Generate a hot README for this project"

# Run in quiet mode (hide the spinner)
angela run --quiet "Generate a README for this project"

# Run in verbose mode (show logs)
angela run --verbose "Generate a README for this project"

# Run inside an OS-level sandbox instead of relying on prompts
angela run --sandbox "Generate a README for this project"

# Run in yolo mode (auto-accept all permissions; use with care). Without
# it, a headless run has no one to answer a prompt and refuses anything
# it cannot already settle on its own.
angela run --yolo "Refactor this package and run the tests"

# Auto-accept file edits only; commands, network, etc. that need
# approval are still refused since there is no one to answer.
angela run --auto-accept-edits "Fix the typos in README.md"

# Continue a previous session
angela run --session {session-id} "Follow up on your last response"

# Continue the most recent session
angela run --continue "Follow up on your last response"

  `,
	RunE: func(cmd *cobra.Command, args []string) (retErr error) {
		defer serveProfiles()()

		var (
			quiet, _      = cmd.Flags().GetBool("quiet")
			verbose, _    = cmd.Flags().GetBool("verbose")
			debug, _      = cmd.Flags().GetBool("debug")
			largeModel, _ = cmd.Flags().GetString("model")
			smallModel, _ = cmd.Flags().GetString("small-model")
			sessionID, _  = cmd.Flags().GetString("session")
			useLast, _    = cmd.Flags().GetBool("continue")
			outputPath, _ = cmd.Flags().GetString("output")
		)

		// Cancel on SIGINT or SIGTERM.
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
		defer cancel()

		prompt := strings.Join(args, " ")

		prompt, err := MaybePrependStdin(prompt)
		if err != nil {
			slog.Error("Failed to read from stdin", "error", err)
			return err
		}

		if prompt == "" {
			return fmt.Errorf("no prompt provided")
		}

		// NOTE: Opened before the run so an unwritable path fails fast
		// instead of after a long run, and before --cwd takes effect so a
		// relative path resolves like a shell redirect. It is not truncated
		// here: the run may need to read the file it is about to replace.
		var resultFile *os.File
		if outputPath != "" {
			resultFile, err = os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE, 0o644)
			if err != nil {
				return fmt.Errorf("failed to open output file: %w", err)
			}
			defer func() { retErr = errors.Join(retErr, resultFile.Close()) }()
		}

		event.SetNonInteractive(true)

		switch {
		case sessionID != "":
			event.SetContinueBySessionID(true)
		case useLast:
			event.SetContinueLastSession(true)
		}

		ws, cleanup, err := setupLocalWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		event.AppInitialized()

		if !ws.Config().IsConfigured() {
			return fmt.Errorf("no providers configured - please run 'angela' to set up a provider interactively")
		}

		if verbose {
			logToStderr(debug)
		}

		appWs := ws.(*workspace.AppWorkspace)

		if sessionID != "" {
			sess, err := resolveSessionID(ctx, appWs.App().Sessions, sessionID)
			if err != nil {
				return err
			}
			sessionID = sess.ID
		}

		final, err := appWs.App().RunNonInteractive(ctx, os.Stdout, prompt, largeModel, smallModel, quiet || verbose, sessionID, useLast)
		return saveRunResult(resultFile, final, err, cmd.ErrOrStderr())
	},
}

func init() {
	runCmd.Flags().BoolP("quiet", "q", false, "Hide spinner")
	runCmd.Flags().BoolP("verbose", "v", false, "Show logs on stderr; combine with --debug for debug level")
	runCmd.Flags().StringP("output", "o", "", "Replace this file with the final response when the run succeeds; stdout still streams the whole conversation")
	runCmd.Flags().StringP("model", "m", "", "Model to use. Accepts 'model' or 'provider/model' to disambiguate models with the same name across providers")
	runCmd.Flags().String("small-model", "", "Small model to use. If not provided, uses the default small model for the provider")
	runCmd.Flags().StringP("session", "s", "", "Continue a previous session by ID")
	runCmd.Flags().BoolP("continue", "C", false, "Continue the most recent session")
	runCmd.MarkFlagsMutuallyExclusive("session", "continue")
	addSandboxFlags(runCmd)
}

// saveRunResult replaces the contents of f with a successful run's final
// response. A failed or cancelled run leaves f as it was, because its error
// already tells a script not to use the file. A successful run always
// rewrites f, so a zero exit status never leaves an earlier run's text
// behind as if it were this one's. A run that finished with nothing to say
// empties f and warns on warn. A nil f means --output was not given.
func saveRunResult(f *os.File, final string, runErr error, warn io.Writer) error {
	if runErr != nil || f == nil {
		return runErr
	}
	// NOTE: Only a regular file can be truncated. A device such as
	// /dev/stdout is a valid target and has nothing to clear.
	if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
		if err := f.Truncate(0); err != nil {
			return fmt.Errorf("failed to clear output file: %w", err)
		}
	}
	final = strings.TrimSpace(final)
	if final == "" {
		fmt.Fprintf(warn, "Warning: the run produced no final response, so %s was left empty\n", f.Name())
		return nil
	}
	if _, err := f.WriteString(final + "\n"); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}
	return nil
}

// logToStderr sends logs to stderr, where a headless run's stdout is
// usually already redirected to capture the answer.
//
// It replaces the handler angelalog.Setup installed rather than adding to
// it, so the level has to be restated here: reading --debug from the flag
// is what keeps `--debug --verbose` from showing less than --debug alone.
func logToStderr(debug bool) {
	level := log.InfoLevel
	if debug {
		level = log.DebugLevel
	}
	slog.SetDefault(slog.New(log.NewWithOptions(os.Stderr, log.Options{Level: level})))
}
