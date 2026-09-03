package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/NaturalSelect/angela/internal/client"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/event"
	"github.com/NaturalSelect/angela/internal/format"
	"github.com/NaturalSelect/angela/internal/herdr"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/google/uuid"
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

# Run in quiet mode (hide the spinner)
angela run --quiet "Generate a README for this project"

# Run in verbose mode (show logs)
angela run --verbose "Generate a README for this project"

# Continue a previous session
angela run --session {session-id} "Follow up on your last response"

# Continue the most recent session
angela run --continue "Follow up on your last response"

  `,
	RunE: func(cmd *cobra.Command, args []string) error {
		var (
			quiet, _      = cmd.Flags().GetBool("quiet")
			verbose, _    = cmd.Flags().GetBool("verbose")
			debug, _      = cmd.Flags().GetBool("debug")
			largeModel, _ = cmd.Flags().GetString("model")
			smallModel, _ = cmd.Flags().GetString("small-model")
			sessionID, _  = cmd.Flags().GetString("session")
			useLast, _    = cmd.Flags().GetBool("continue")
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

		event.SetNonInteractive(true)

		switch {
		case sessionID != "":
			event.SetContinueBySessionID(true)
		case useLast:
			event.SetContinueLastSession(true)
		}

		if useClientServer() {
			c, ws, cleanup, err := connectToServer(cmd)
			if err != nil {
				return err
			}
			defer cleanup()

			event.AppInitialized()

			if !ws.Config.IsConfigured() {
				return fmt.Errorf("no providers configured - please run 'angela' to set up a provider interactively")
			}

			clientWs := workspace.NewClientWorkspace(c, *ws)
			if err := clientWs.InitCoderAgentNonInteractive(ctx); err != nil {
				return fmt.Errorf("failed to initialize agent: %w", err)
			}

			if sessionID != "" {
				sess, err := resolveSessionByID(ctx, c, ws.ID, sessionID)
				if err != nil {
					return err
				}
				sessionID = sess.ID
			}

			if verbose {
				logToStderr(debug)
			}

			return runNonInteractive(ctx, c, ws, prompt, largeModel, smallModel, quiet || verbose, sessionID, useLast)
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

		return appWs.App().RunNonInteractive(ctx, os.Stdout, prompt, largeModel, smallModel, quiet || verbose, sessionID, useLast)
	},
}

func init() {
	runCmd.Flags().BoolP("quiet", "q", false, "Hide spinner")
	runCmd.Flags().BoolP("verbose", "v", false, "Show logs on stderr; combine with --debug for debug level")
	runCmd.Flags().StringP("model", "m", "", "Model to use. Accepts 'model' or 'provider/model' to disambiguate models with the same name across providers")
	runCmd.Flags().String("small-model", "", "Small model to use. If not provided, uses the default small model for the provider")
	runCmd.Flags().StringP("session", "s", "", "Continue a previous session by ID")
	runCmd.Flags().BoolP("continue", "C", false, "Continue the most recent session")
	runCmd.MarkFlagsMutuallyExclusive("session", "continue")
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

// runNonInteractive executes the agent via the server and streams output
// to stdout.
func runNonInteractive(
	ctx context.Context,
	c *client.Client,
	ws *proto.Workspace,
	prompt, largeModel, smallModel string,
	hideSpinner bool,
	continueSessionID string,
	useLast bool,
) error {
	slog.Info("Running in non-interactive mode")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mainOverride *config.SelectedModel
	if largeModel != "" || smallModel != "" {
		var err error
		mainOverride, err = applyModelOverrides(ctx, c, ws, largeModel, smallModel)
		if err != nil {
			return fmt.Errorf("failed to override models: %w", err)
		}
	}

	var (
		spinner   *format.Spinner
		stderrTTY bool
		progress  bool
	)

	stderrTTY = term.IsTerminal(os.Stderr.Fd())
	progress = ws.Config.Options.Progress == nil || *ws.Config.Options.Progress

	if !hideSpinner && stderrTTY {
		t := styles.AngelaTeal()

		spinner = format.NewSpinner(ctx, cancel, anim.Settings{
			Size:        10,
			Label:       "Generating",
			GradColorA:  t.WorkingGradFromColor,
			GradColorB:  t.WorkingGradToColor,
			CycleColors: true,
		})
		spinner.Start()
	}

	stopSpinner := func() {
		if !hideSpinner && spinner != nil {
			spinner.Stop()
			spinner = nil
		}
	}

	// Wait for the agent to become ready (MCP init, etc).
	if err := waitForAgent(ctx, c, ws.ID); err != nil {
		stopSpinner()
		return fmt.Errorf("agent not ready: %w", err)
	}

	// Force-update agent models so MCP tools are loaded.
	if err := c.UpdateAgent(ctx, ws.ID); err != nil {
		slog.Warn("Failed to update agent", "error", err)
	}

	defer stopSpinner()

	sess, err := resolveSession(ctx, c, ws.ID, continueSessionID, useLast)
	if err != nil {
		return fmt.Errorf("failed to resolve session: %w", err)
	}
	if continueSessionID != "" || useLast {
		slog.Info("Continuing session for non-interactive run", "session_id", sess.ID)
	} else {
		slog.Info("Created session for non-interactive run", "session_id", sess.ID)
	}

	// Nothing in this process answers permission prompts: the stream
	// loop below reads messages and run results only. Tell the server
	// before the first prompt goes out, so a request it cannot settle
	// on its own is refused rather than parked on a channel until this
	// run's context dies.
	if err := c.SetSessionUnattended(ctx, ws.ID, sess.ID, true); err != nil {
		return fmt.Errorf("failed to mark the session unattended: %w", err)
	}

	// The model override belongs to this run's session, not to the
	// workspace config: applying it here leaves every other session
	// alone, and a continued session without an override keeps the model
	// it was already on.
	if mainOverride != nil {
		edit := config.ActiveAgentEdit{Slot: config.SlotMain, Model: mainOverride}
		if _, err := c.AgentEditSessionActive(ctx, ws.ID, sess.ID, edit); err != nil {
			return fmt.Errorf("failed to apply the model override: %w", err)
		}
	}

	events, err := c.SubscribeEvents(ctx, ws.ID)
	if err != nil {
		return fmt.Errorf("failed to subscribe to events: %w", err)
	}

	// Mint a per-call RunID so we can correlate the terminal
	// RunComplete with *this* SendMessage even if the session was
	// busy and another turn finished first. Without it the stream
	// loop would exit on whichever RunComplete arrived first for
	// the same session and drop the queued prompt's output.
	runID := uuid.New().String()
	if err := c.SendMessage(ctx, ws.ID, sess.ID, runID, prompt); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	stream := &runStream{
		sessionID: sess.ID,
		runID:     runID,
		out:       os.Stdout,
		read:      make(map[string]int),
	}

	// Start herdr integration when running inside a herdr pane.
	hc := herdr.Init()
	hc.SetSessionID(sess.ID)
	defer hc.Close()

	defer func() {
		if progress && stderrTTY {
			_, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar)
		}
		_, _ = fmt.Fprintln(os.Stdout)
	}()

	for {
		if progress && stderrTTY {
			_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
		}

		select {
		case ev, ok := <-events:
			if !ok {
				stopSpinner()
				return nil
			}

			// Forward events to herdr if running inside a herdr pane.
			if hev := herdr.Translate(ev); hev != nil {
				hc.HandleEvent(hev)
			}

			done, err := stream.handle(ev, stopSpinner)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case <-ctx.Done():
			stopSpinner()
			return ctx.Err()
		}
	}
}

// runStream tracks the per-message stdout cursor and the
// reconciliation state used by [runNonInteractive] to translate
// streaming SSE events into a final, complete stdout for `angela run`.
// It is split out so the state machine can be exercised in unit tests
// without spinning up the full server/client harness.
//
// runID, when non-empty, is the authoritative correlator for the
// terminal RunComplete event: the stream suppresses live message
// events and only exits on a RunComplete whose RunID matches, so a
// turn that finishes first on the same session (e.g. when our prompt
// was queued behind a busy session) cannot contaminate stdout or
// terminate us prematurely. When empty (older servers, tests that
// don't supply one) the stream falls back to SessionID-only matching
// and live message streaming, which is still correct for the
// single-turn case.
type runStream struct {
	sessionID string
	runID     string
	out       io.Writer
	read      map[string]int
	printed   bool
}

// handle processes one SSE event. Returns done=true when the run
// loop should exit (RunComplete observed); returns an error only
// when the agent run failed (not on context cancel — that path is
// handled by the caller's select). stopSpinner is called on the
// first observable assistant output and on completion; passing nil
// is safe for tests.
func (s *runStream) handle(ev any, stopSpinner func()) (done bool, err error) {
	stop := func() {
		if stopSpinner != nil {
			stopSpinner()
		}
	}
	switch e := ev.(type) {
	case pubsub.Event[proto.Message]:
		msg := e.Payload
		if msg.SessionID != s.sessionID || msg.Role != proto.Assistant || len(msg.Parts) == 0 {
			return false, nil
		}
		if s.runID != "" {
			return false, nil
		}
		stop()

		content := msg.Content().String()
		readBytes := s.read[msg.ID]
		if len(content) < readBytes {
			slog.Error("Non-interactive: message content shorter than read bytes",
				"message_length", len(content), "read_bytes", readBytes)
			return false, fmt.Errorf("message content is shorter than read bytes: %d < %d", len(content), readBytes)
		}

		part := content[readBytes:]
		if readBytes == 0 {
			part = strings.TrimLeft(part, " \t")
		}
		if s.printed || strings.TrimSpace(part) != "" {
			s.printed = true
			fmt.Fprint(s.out, part)
		}
		s.read[msg.ID] = len(content)
		return false, nil

	case pubsub.Event[proto.RunComplete]:
		// RunComplete is the authoritative end-of-run signal. We
		// exit on it instead of guessing from message finish parts,
		// which fire on every tool-call step too and were the
		// source of the regression where `angela run` exited
		// mid-turn on finish.reason == tool_use.
		//
		// Correlation:
		//   - if we minted a RunID for this SendMessage, only the
		//     event whose RunID matches is ours; any other turn
		//     finishing first on the same session (busy-session
		//     queue path) must be ignored.
		//   - if we have no RunID (older server, tests), fall back
		//     to SessionID matching.
		if s.runID != "" {
			if e.Payload.RunID != s.runID {
				return false, nil
			}
		} else if e.Payload.SessionID != s.sessionID {
			return false, nil
		}
		stop()
		if e.Payload.Error != "" && !e.Payload.Cancelled {
			return true, fmt.Errorf("agent run failed: %s", e.Payload.Error)
		}
		// Reconcile stdout against the authoritative final
		// assistant text carried in the event. The pubsub fan-in
		// does not serialize publishes across upstream brokers, so
		// the final message event may not have reached this loop
		// yet; the embedded Text field is the backstop that
		// guarantees the full final text always appears on stdout.
		if e.Payload.MessageID != "" {
			full := e.Payload.Text
			readBytes := s.read[e.Payload.MessageID]
			if readBytes < len(full) {
				tail := full[readBytes:]
				if readBytes == 0 {
					tail = strings.TrimLeft(tail, " \t")
				}
				if s.printed || strings.TrimSpace(tail) != "" {
					s.printed = true
					fmt.Fprint(s.out, tail)
				}
			}
		}
		return true, nil

	case pubsub.Event[proto.AgentEvent]:
		if e.Payload.Error == nil {
			return false, nil
		}
		// Attribute the error to our run before treating it as
		// fatal. Async errors from an unrelated workspace run share
		// this channel, so a foreign failure must not abort us:
		//   - if the event carries a RunID, it is the authoritative
		//     correlator: it must match our run exactly, otherwise it
		//     belongs to a different request and we ignore it.
		//   - if the event carries no RunID (older server), fall back
		//     to SessionID: it must be present and match our session,
		//     otherwise we ignore it.
		if e.Payload.RunID != "" {
			if e.Payload.RunID != s.runID {
				return false, nil
			}
		} else if e.Payload.SessionID == "" || e.Payload.SessionID != s.sessionID {
			return false, nil
		}
		stop()
		return true, fmt.Errorf("agent error: %w", e.Payload.Error)
	}
	return false, nil
}

// waitForAgent polls GetAgentInfo until the agent is ready, with a
// timeout.
func waitForAgent(ctx context.Context, c *client.Client, wsID string) error {
	timeout := time.After(30 * time.Second)
	for {
		info, err := c.GetAgentInfo(ctx, wsID)
		if err == nil && info.IsReady {
			return nil
		}
		select {
		case <-timeout:
			if err != nil {
				return fmt.Errorf("timeout waiting for agent: %w", err)
			}
			return fmt.Errorf("timeout waiting for agent readiness")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// applyModelOverrides resolves the --model / --small-model flags and
// applies what can be applied before a session exists. The main model
// belongs to the session and is returned for the caller to apply once
// the session is resolved.
//
// NOTE: --small-model still writes workspace config. It serves the
// internal agents (titling, compaction), which belong to no session, and
// the server exposes no in-memory override channel the way the
// in-process path has. This is the one remaining config write driven by
// a CLI flag.
func applyModelOverrides(
	ctx context.Context,
	c *client.Client,
	ws *proto.Workspace,
	largeModel, smallModel string,
) (*config.SelectedModel, error) {
	cfg, err := c.GetConfig(ctx, ws.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	providers := cfg.Providers.Copy()
	small, large, err := resolveModelOverrides(providers, largeModel, smallModel)
	if err != nil {
		return nil, err
	}

	if small != nil {
		slog.Info("Overriding small model", "provider", small.provider, "model", small.modelID)
		if err := c.UpdatePreferredModel(ctx, ws.ID, config.ScopeWorkspace, config.SlotChore, config.SelectedModel{
			Provider: small.provider,
			Model:    small.modelID,
		}); err != nil {
			return nil, fmt.Errorf("failed to set small model: %w", err)
		}
		if err := c.UpdateAgent(ctx, ws.ID); err != nil {
			return nil, fmt.Errorf("failed to update agent: %w", err)
		}
	}

	if large == nil {
		return nil, nil
	}
	slog.Info("Overriding large model", "provider", large.provider, "model", large.modelID)
	return &config.SelectedModel{
		Provider: large.provider,
		Model:    large.modelID,
	}, nil
}

// resolveModelOverrides validates both model flags without applying
// either. The chore write is persisted server-side, so resolving it and
// writing it before the main flag is even checked would leave a rejected
// invocation with a permanently changed config.
func resolveModelOverrides(
	providers map[string]config.ProviderConfig,
	largeModel, smallModel string,
) (small, large *modelMatch, err error) {
	largeMatches, smallMatches := findModelMatches(providers, largeModel, smallModel)

	if smallModel != "" {
		found, err := validateModelMatches(smallMatches, smallModel, string(config.SlotChore))
		if err != nil {
			return nil, nil, err
		}
		small = &found
	}
	if largeModel != "" {
		found, err := validateModelMatches(largeMatches, largeModel, string(config.SlotMain))
		if err != nil {
			return nil, nil, err
		}
		large = &found
	}
	return small, large, nil
}

type modelMatch struct {
	provider string
	modelID  string
}

// findModelMatches searches providers for matching large/small model
// strings.
func findModelMatches(providers map[string]config.ProviderConfig, largeModel, smallModel string) ([]modelMatch, []modelMatch) {
	largeFilter, largeID := parseModelString(largeModel)
	smallFilter, smallID := parseModelString(smallModel)

	var largeMatches, smallMatches []modelMatch
	for name, provider := range providers {
		if provider.Disable {
			continue
		}
		for _, m := range provider.Models {
			if matchesModel(largeID, largeFilter, m.ID, name) {
				largeMatches = append(largeMatches, modelMatch{provider: name, modelID: m.ID})
			}
			if matchesModel(smallID, smallFilter, m.ID, name) {
				smallMatches = append(smallMatches, modelMatch{provider: name, modelID: m.ID})
			}
		}
	}
	return largeMatches, smallMatches
}

// parseModelString splits "provider/model" into (provider, model) or
// ("", model).
func parseModelString(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	if idx := strings.Index(s, "/"); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return "", s
}

// matchesModel returns true if the model ID matches the filter
// criteria.
func matchesModel(wantID, wantProvider, modelID, providerName string) bool {
	if wantID == "" {
		return false
	}
	if wantProvider != "" && wantProvider != providerName {
		return false
	}
	return strings.EqualFold(modelID, wantID)
}

// validateModelMatches ensures exactly one match exists.
func validateModelMatches(matches []modelMatch, modelID, label string) (modelMatch, error) {
	switch {
	case len(matches) == 0:
		return modelMatch{}, fmt.Errorf("%s model %q not found", label, modelID)
	case len(matches) > 1:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.provider
		}
		return modelMatch{}, fmt.Errorf(
			"%s model: model %q found in multiple providers: %s. Please specify provider using 'provider/model' format",
			label, modelID, strings.Join(names, ", "),
		)
	}
	return matches[0], nil
}

// resolveSession returns the session to use for a non-interactive run.
// If continueSessionID is set it fetches that session; if useLast is set it
// returns the most recently updated top-level session; otherwise it creates a
// new one.
func resolveSession(ctx context.Context, c *client.Client, wsID, continueSessionID string, useLast bool) (*proto.Session, error) {
	switch {
	case continueSessionID != "":
		sess, err := c.GetSession(ctx, wsID, continueSessionID)
		if err != nil {
			return nil, fmt.Errorf("session not found: %s", continueSessionID)
		}
		if sess.ParentSessionID != "" {
			return nil, fmt.Errorf("cannot continue a child session: %s", continueSessionID)
		}
		return sess, nil

	case useLast:
		sessions, err := c.ListSessions(ctx, wsID)
		if err != nil || len(sessions) == 0 {
			return nil, fmt.Errorf("no sessions found to continue")
		}
		last := sessions[0]
		for _, s := range sessions[1:] {
			if s.UpdatedAt > last.UpdatedAt && s.ParentSessionID == "" {
				last = s
			}
		}
		return &last, nil

	default:
		return c.CreateSession(ctx, wsID, "non-interactive")
	}
}

// resolveSessionByID resolves a session ID that may be a full UUID or a hash
// prefix returned by angela session list.
func resolveSessionByID(ctx context.Context, c *client.Client, wsID, id string) (*proto.Session, error) {
	if sess, err := c.GetSession(ctx, wsID, id); err == nil {
		return sess, nil
	}

	sessions, err := c.ListSessions(ctx, wsID)
	if err != nil {
		return nil, err
	}

	var matches []proto.Session
	for _, s := range sessions {
		hash := session.HashID(s.ID)
		if hash == id || strings.HasPrefix(hash, id) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("session %q not found", id)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("session ID %q is ambiguous (%d matches)", id, len(matches))
	}
}
