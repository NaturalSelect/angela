package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	fang "charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/event"
	angelalog "github.com/NaturalSelect/angela/internal/log"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/projects"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/logo"
	ui "github.com/NaturalSelect/angela/internal/ui/model"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/version"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/charmtone"
	xstrings "github.com/charmbracelet/x/exp/strings"
	"github.com/charmbracelet/x/term"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.PersistentFlags().StringP("cwd", "c", "", "Current working directory")
	rootCmd.PersistentFlags().StringP("data-dir", "D", "", "Custom angela data directory")
	rootCmd.PersistentFlags().BoolP("debug", "d", false, "Debug")
	rootCmd.Flags().BoolP("help", "h", false, "Help")
	// Persistent so `angela run` and other subcommands can see it too:
	// the permission ladder now has no other way to skip a prompt a
	// headless run cannot answer, so --yolo must reach every command
	// that can end up gating a permission request.
	rootCmd.PersistentFlags().BoolP("yolo", "y", false, "Automatically accept all permissions (dangerous mode)")
	rootCmd.PersistentFlags().Bool("yolo-merge", false, "Auto-approve the merge tool in yolo mode (by default, merge still requires approval)")
	rootCmd.PersistentFlags().Bool("auto-accept-edits", false, "Automatically accept file edits but still ask about everything else (same as cycling Shift+Tab once)")
	rootCmd.MarkFlagsMutuallyExclusive("yolo", "auto-accept-edits")
	rootCmd.Flags().Bool("no-vscode-diff", false, "Do not open edit diffs in VS Code even when running inside its terminal")
	rootCmd.Flags().Bool("disable-image-tools", false, "Disable the built-in image generation and editing tools")
	rootCmd.Flags().Bool("kitty-placeholders", false, "Force Kitty graphics Unicode placeholder support on or off (needed for inline images and the ImageGenerate/ImageEdit tools); omit to use options.tui.kitty_placeholders or auto-detection")
	rootCmd.PersistentFlags().StringSlice("channels", nil, "MCP servers to enable as channels (repeatable), e.g. --channels server:webhook")
	_ = rootCmd.PersistentFlags().MarkHidden("channels")
	rootCmd.Flags().StringP("session", "s", "", "Continue a previous session by ID")
	rootCmd.Flags().BoolP("continue", "C", false, "Continue the most recent session")
	rootCmd.MarkFlagsMutuallyExclusive("session", "continue")
	addSandboxFlags(rootCmd)

	rootCmd.AddCommand(
		runCmd,
		commitCmd,
		dirsCmd,
		projectsCmd,
		updateProvidersCmd,
		logsCmd,
		logoutCmd,
		schemaCmd,
		loginCmd,
		statsCmd,
		sessionCmd,
		acpCmd,
	)
}

// kittyPlaceholdersFlag returns the tri-state value of --kitty-placeholders:
// nil when the flag was not passed (defer to options.tui.kitty_placeholders
// or auto-detection), or a pointer to the flag's boolean value when it was.
func kittyPlaceholdersFlag(cmd *cobra.Command) *bool {
	if !cmd.Flags().Changed("kitty-placeholders") {
		return nil
	}
	v, _ := cmd.Flags().GetBool("kitty-placeholders")
	return &v
}

var rootCmd = &cobra.Command{
	Use:   "angela",
	Short: "A terminal-first AI assistant for software development",
	Long:  "A glamorous, terminal-first AI assistant for software development and adjacent tasks",
	Example: `
# Run in interactive mode
angela

# Run non-interactively
angela run "Guess my 5 favorite Pokémon"

# Run a non-interactively with pipes and redirection
cat README.md | angela run "make this more glamorous" > GLAMOROUS_README.md

# Run with debug logging in a specific directory
angela --debug --cwd /path/to/project

# Run in yolo mode (auto-accept all permissions; use with care)
angela --yolo

# Run in yolo mode and also auto-approve branch merges
angela --yolo --yolo-merge

# Auto-accept file edits but still ask about commands, network, etc.
angela --auto-accept-edits

# Run without opening edit diffs in VS Code's own diff viewer
angela --no-vscode-diff

# Force-enable inline images in a terminal Angela doesn't recognize
angela --kitty-placeholders

# Run inside an OS-level sandbox instead of relying on prompts
angela --sandbox

# Run the sandbox even when already inside a Docker/OCI container
angela --sandbox --no-docker-sandbox

# Run with custom data directory
angela --data-dir /path/to/custom/.angela

# Continue a previous session
angela --session {session-id}

# Continue the most recent session
angela --continue
  `,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer serveProfiles()()

		sessionID, _ := cmd.Flags().GetString("session")
		continueLast, _ := cmd.Flags().GetBool("continue")

		ws, cleanup, err := setupWorkspaceWithProgressBar(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		if sessionID != "" {
			sess, err := resolveWorkspaceSessionID(cmd.Context(), ws, sessionID)
			if err != nil {
				return err
			}
			sessionID = sess.ID
		}

		event.AppInitialized()

		com := common.DefaultCommon(ws)
		com.KittyPlaceholders = kittyPlaceholdersFlag(cmd)
		model := ui.New(com, sessionID, continueLast)

		// Chrome/xdg-open inherit our stdout/stderr by default; without this,
		// their diagnostic output (e.g. from an MCP OAuth browser flow)
		// prints straight into the TUI's alt-screen and corrupts it.
		browser.Stdout = io.Discard
		browser.Stderr = io.Discard

		inputFilter := ui.NewFilter()
		var env uv.Environ = os.Environ()
		program := tea.NewProgram(
			model,
			tea.WithEnvironment(env),
			tea.WithContext(cmd.Context()),
			tea.WithFilter(inputFilter.Filter),
		)
		go ws.Subscribe(program)

		if _, err := program.Run(); err != nil {
			event.Error(err)
			slog.Error("TUI run error", "error", err)
			return errors.New("Angela crashed. If metrics are enabled, we were notified about it. If you'd like to report it, please copy the stacktrace above and open an issue at https://github.com/NaturalSelect/angela/issues/new?template=bug.yml") //nolint:staticcheck
		}
		printSessionResume(model)
		return nil
	},
}

// printSessionResume prints the session title and resume hint to stdout after
// the TUI exits, so the user can resume the session with `angela -s <id>`.
// Nothing is printed when there is no active session.
func printSessionResume(model *ui.UI) {
	out := colorprofile.NewWriter(os.Stderr, os.Environ())

	t := styles.AngelaTeal()
	angelaLogo := logo.Render(t.Logo.GradCanvas, version.Version, true, logo.Opts{
		TitleColorA:  t.Logo.TitleColorA,
		TitleColorB:  t.Logo.TitleColorB,
		VersionColor: t.Logo.VersionColor,
	})

	sess := model.CurrentSession()
	hasSession := sess != nil && sess.ID != ""

	tw, _, _ := term.GetSize(os.Stdout.Fd())
	style := lipgloss.NewStyle().Padding(1, 3)
	contentWidth := tw - style.GetHorizontalFrameSize()

	info := angelaLogo +
		"\nThanks for using Angela! " +
		lipgloss.NewStyle().Width(contentWidth).Render(randomExitMessage())

	if hasSession {
		title := strings.ReplaceAll(sess.Title, "\n", " ")

		labelWidth := lipgloss.Width("Session  ")
		titleWidth := contentWidth - labelWidth
		if titleWidth > 0 {
			title = ansi.Truncate(title, titleWidth, "…")
		}

		hash := session.HashID(sess.ID)[:7]
		sessionLine := lipgloss.NewStyle().Foreground(charmtone.Charple).Render("Session  ") + title
		continueLine := lipgloss.NewStyle().Foreground(charmtone.Charple).Render("Continue ") + "angela -s " + hash
		info += "\n\n" + sessionLine + "\n" + continueLine
	}

	body := style.Width(tw).Render(info)

	fmt.Fprintln(out, body)
}

// copied from cobra:
const defaultVersionTemplate = `{{with .DisplayName}}{{printf "%s " .}}{{end}}{{printf "version %s" .Version}}
`

// randomExitMessage returns a random exit message.
func randomExitMessage() string {
	messages := []string{
		"",
		"See ya later.",
		"You look great.",
		"Have a gorgeous time.",
		"Get some rest.",
		"Come back soon.",
		"You worked handsomely.",
		"Time for a snack.",
		"Who’s hungry?",
		"That was fun.",
		"See you at breakfast?",
		"Time for a nap.",
		"Who wants some spaghetti?",
		"Take care of yourself.",
		"Remember to hydrate.",
		"Time for a swim?",
		"You’re quite glamorous, you know.",
		"Nice work.",
		"You’re a sensation.",
		"Where’s my eyeliner?",
		"It’s tea time.",
	}
	return messages[rand.IntN(len(messages))]
}

func Execute() {
	// FIXME: config.Load uses slog internally during provider resolution,
	// but the file-based logger isn't set up until after config is loaded
	// (because the log path depends on the data directory from config).
	// This creates a window where slog calls in config.Load leak to
	// stderr. We discard early logs here as a workaround. The proper
	// fix is to remove slog calls from config.Load and have it return
	// warnings/diagnostics instead of logging them as a side effect.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	// NOTE: very hacky: we create a colorprofile writer with STDOUT, then make
	// it forward to a bytes.Buffer, write the colored wordmark to it, and then
	// finally prepend it in the version template.
	// Unfortunately cobra doesn't give us a way to set a function to handle
	// printing the version, and PreRunE runs after the version is already
	// handled, so that doesn't work either.
	// This is the only way I could find that works relatively well.
	if term.IsTerminal(os.Stdout.Fd()) {
		t := styles.AngelaTeal()
		wordmark := logo.Render(t.Logo.GradCanvas, "", false, logo.Opts{
			TitleColorA: t.Logo.TitleColorA,
			TitleColorB: t.Logo.TitleColorB,
		})

		var b bytes.Buffer
		w := colorprofile.NewWriter(os.Stdout, os.Environ())
		w.Forward = &b
		_, _ = w.WriteString("\n" + wordmark)
		rootCmd.SetVersionTemplate(b.String() + "\n\n" + defaultVersionTemplate)
	}
	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(version.Version),
		fang.WithNotifySignal(os.Interrupt),
	); err != nil {
		os.Exit(1)
	}
}

// supportsProgressBar tries to determine whether the current terminal supports
// progress bars by looking into environment variables.
func supportsProgressBar() bool {
	if !term.IsTerminal(os.Stderr.Fd()) {
		return false
	}
	termProg := os.Getenv("TERM_PROGRAM")
	_, isWindowsTerminal := os.LookupEnv("WT_SESSION")

	return isWindowsTerminal || xstrings.ContainsAnyOf(strings.ToLower(termProg), "ghostty", "iterm2", "rio")
}

// setupWorkspaceWithProgressBar wraps setupLocalWorkspace with an optional
// terminal progress bar shown during initialization.
func setupWorkspaceWithProgressBar(cmd *cobra.Command) (workspace.Workspace, func(), error) {
	showProgress := supportsProgressBar()
	if showProgress {
		_, _ = fmt.Fprintf(os.Stderr, ansi.SetIndeterminateProgressBar)
	}

	ws, cleanup, err := setupLocalWorkspace(cmd)

	if showProgress {
		_, _ = fmt.Fprintf(os.Stderr, ansi.ResetProgressBar)
	}

	return ws, cleanup, err
}

// permissionModeFromFlags maps --yolo / --auto-accept-edits to the
// startup PermissionMode. cobra rejects the two flags being set
// together (see the MarkFlagsMutuallyExclusive call in init), so the
// order checked here only documents that yolo is the wider of the two.
func permissionModeFromFlags(cmd *cobra.Command) permission.PermissionMode {
	if yolo, _ := cmd.Flags().GetBool("yolo"); yolo {
		return permission.ModeYolo
	}
	if autoAcceptEdits, _ := cmd.Flags().GetBool("auto-accept-edits"); autoAcceptEdits {
		return permission.ModeAutoAcceptEdits
	}
	return permission.ModeManual
}

// sandboxResolver builds the sandbox config once the workspace's data
// directory and permission rules are known. It reports false when no
// sandbox was requested.
type sandboxResolver func(workingDir, dataDir string, permissions *config.Permissions) (sandbox.Config, bool, error)

// localWorkspaceOptions is everything openLocalWorkspace needs, so callers
// other than the cobra commands can build a workspace too.
type localWorkspaceOptions struct {
	Cwd               string
	DataDir           string
	Debug             bool
	PermissionMode    permission.PermissionMode
	YoloMerge         bool
	NoVSCodeDiff      bool
	DisableImageTools bool
	NoDockerSandbox   bool
	Channels          []string
	Sandbox           sandboxResolver
	// NonInteractive and NoBranchAgents are zero for the TUI. Only a front
	// end that cannot ask questions or hand a conversation to a branch sets
	// them.
	NonInteractive bool
	NoBranchAgents bool
	// RequireProvider makes openLocalWorkspace fail with
	// workspace.ErrAgentNotInitialized when no provider is configured,
	// before anything is started. The TUI leaves it off because it
	// onboards the user inside an unconfigured workspace.
	RequireProvider bool
}

func localWorkspaceOptionsFromFlags(cmd *cobra.Command, cwd string) localWorkspaceOptions {
	debug, _ := cmd.Flags().GetBool("debug")
	yoloMerge, _ := cmd.Flags().GetBool("yolo-merge")
	noVSCodeDiff, _ := cmd.Flags().GetBool("no-vscode-diff")
	disableImageTools, _ := cmd.Flags().GetBool("disable-image-tools")
	channels, _ := cmd.Flags().GetStringSlice("channels")
	dataDir, _ := cmd.Flags().GetString("data-dir")
	noDockerSandbox, _ := cmd.Flags().GetBool("no-docker-sandbox")

	return localWorkspaceOptions{
		Cwd:               cwd,
		DataDir:           dataDir,
		Debug:             debug,
		PermissionMode:    permissionModeFromFlags(cmd),
		YoloMerge:         yoloMerge,
		NoVSCodeDiff:      noVSCodeDiff,
		DisableImageTools: disableImageTools,
		NoDockerSandbox:   noDockerSandbox,
		Channels:          channels,
		Sandbox: func(workingDir, dataDir string, permissions *config.Permissions) (sandbox.Config, bool, error) {
			return sandboxConfigFromFlags(cmd, workingDir, dataDir, permissions)
		},
	}
}

// setupLocalWorkspace creates an in-process app.App and wraps it in an
// AppWorkspace.
func setupLocalWorkspace(cmd *cobra.Command) (workspace.Workspace, func(), error) {
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return nil, nil, err
	}
	ws, cleanup, err := openLocalWorkspace(cmd.Context(), localWorkspaceOptionsFromFlags(cmd, cwd))
	if err != nil {
		return nil, nil, err
	}
	return ws, cleanup, nil
}

// openLocalWorkspace builds an AppWorkspace for o.Cwd. The returned cleanup
// shuts the app down, which AppWorkspace.Shutdown does as well.
func openLocalWorkspace(ctx context.Context, o localWorkspaceOptions) (*workspace.AppWorkspace, func(), error) {
	store, err := config.Init(o.Cwd, o.DataDir, o.Debug)
	if err != nil {
		return nil, nil, err
	}

	cfg := store.Config()

	// NOTE: This must stay ahead of everything that starts or registers
	// something process-wide. Shutting an app down closes the MCP event
	// broker for good, so a workspace that is built and then dropped would
	// break the next one opened in the same process.
	if o.RequireProvider && !cfg.IsConfigured() {
		return nil, nil, workspace.ErrAgentNotInitialized
	}

	overrides := store.Overrides()
	overrides.PermissionMode = o.PermissionMode
	overrides.EnabledChannels = o.Channels
	overrides.NoDockerSandbox = o.NoDockerSandbox
	overrides.YoloMerge = o.YoloMerge
	overrides.NoVSCodeDiff = o.NoVSCodeDiff
	overrides.DisableImageTools = o.DisableImageTools
	overrides.NonInteractive = o.NonInteractive
	overrides.NoBranchAgents = o.NoBranchAgents

	if err := os.MkdirAll(cfg.Options.DataDirectory, 0o700); err != nil {
		return nil, nil, fmt.Errorf("failed to create data directory: %q %w", cfg.Options.DataDirectory, err)
	}

	// Entered before the database connection and any other child
	// process (LSP, MCP, bash tool) starts, so the path restriction
	// covers all of them. Network restriction is narrower: only
	// commands the shell tool spawns afterward are affected (see
	// sandbox.ShouldRestrictChildNetwork); this process itself and
	// any LSP/MCP servers keep whatever network access they'd
	// otherwise have. It must come after MkdirAll above: Landlock's
	// IgnoreIfMissing mode, and the equivalent existing-path check
	// on macOS, both silently drop rules for paths that don't exist
	// yet, so entering any earlier would leave the still-missing
	// data directory unprotected. Both backends restrict the running
	// process in place and return normally on success.
	if o.Sandbox != nil {
		sandboxCfg, sandboxEnabled, err := o.Sandbox(o.Cwd, cfg.Options.DataDirectory, cfg.Permissions)
		if err != nil {
			return nil, nil, err
		}
		if sandboxEnabled {
			if err := sandbox.New(o.NoDockerSandbox).EnterSandbox(sandboxCfg); err != nil {
				return nil, nil, fmt.Errorf("failed to enter sandbox: %w", err)
			}
		}
	}

	gitIgnorePath := filepath.Join(cfg.Options.DataDirectory, ".gitignore")
	if _, err := os.Stat(gitIgnorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitIgnorePath, []byte("*\n"), 0o644); err != nil {
			return nil, nil, fmt.Errorf("failed to create .gitignore file: %q %w", gitIgnorePath, err)
		}
	}

	if err := projects.Register(o.Cwd, cfg.Options.DataDirectory); err != nil {
		slog.Warn("Failed to register project", "error", err)
	}

	conn, err := db.Connect(ctx, cfg.Options.DataDirectory)
	if err != nil {
		return nil, nil, err
	}

	logFile := filepath.Join(cfg.Options.DataDirectory, "logs", "angela.log")
	angelalog.Setup(logFile, o.Debug)

	// Discover skills once before app.New. Local mode hosts a single
	// workspace per process, so WithGlobalMirror keeps the package
	// globals (which the TUI reads via skills.GetLatestStates) in sync
	// with the manager.
	discoveryCfg := localSkillsDiscoveryConfig(store)
	allSkills, activeSkills, skillStates := skills.DiscoverFromConfig(discoveryCfg)
	skillsMgr := skills.NewManager(
		allSkills, activeSkills, skillStates,
		skills.WithGlobalMirror(),
		skills.WithResolvedPaths(discoveryCfg.ResolvePaths()),
		skills.WithWorkingDir(discoveryCfg.WorkingDir),
	)

	appInstance, err := app.New(ctx, conn, store, skillsMgr)
	if err != nil {
		_ = conn.Close()
		slog.Error("Failed to create app instance", "error", err)
		return nil, nil, err
	}

	if shouldEnableMetrics(cfg) {
		event.Init()
	}

	ws := workspace.NewAppWorkspace(appInstance, store)
	cleanup := func() { appInstance.Shutdown() }
	return ws, cleanup, nil
}

// localSkillsDiscoveryConfig adapts a *config.ConfigStore to the inputs
// skills.DiscoverFromConfig expects.
func localSkillsDiscoveryConfig(store *config.ConfigStore) skills.DiscoveryConfig {
	opts := store.Config().Options
	var paths, disabled []string
	if opts != nil {
		paths = opts.SkillsPaths
		disabled = opts.DisabledSkills
	}
	var resolver func(string) (string, error)
	if r := store.Resolver(); r != nil {
		resolver = r.ResolveValue
	}
	return skills.DiscoveryConfig{
		SkillsPaths:    paths,
		DisabledSkills: disabled,
		WorkingDir:     store.WorkingDir(),
		Resolver:       resolver,
	}
}

func shouldEnableMetrics(cfg *config.Config) bool {
	if v, _ := strconv.ParseBool(os.Getenv("ANGELA_DISABLE_METRICS")); v {
		return false
	}
	if v, _ := strconv.ParseBool(os.Getenv("DO_NOT_TRACK")); v {
		return false
	}
	if cfg.Options.DisableMetrics {
		return false
	}
	return true
}

func MaybePrependStdin(prompt string) (string, error) {
	if term.IsTerminal(os.Stdin.Fd()) {
		return prompt, nil
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return prompt, err
	}
	// Check if stdin is a named pipe ( | ) or regular file ( < ).
	if fi.Mode()&os.ModeNamedPipe == 0 && !fi.Mode().IsRegular() {
		return prompt, nil
	}
	bts, err := io.ReadAll(os.Stdin)
	if err != nil {
		return prompt, err
	}
	return string(bts) + "\n\n" + prompt, nil
}

// resolveWorkspaceSessionID resolves a session ID that may be a full
// UUID, full hash, or hash prefix. Works against the Workspace
// interface.
func resolveWorkspaceSessionID(ctx context.Context, ws workspace.Workspace, id string) (session.Session, error) {
	if sess, err := ws.GetSession(ctx, id); err == nil {
		return sess, nil
	}

	sessions, err := ws.ListSessions(ctx)
	if err != nil {
		return session.Session{}, err
	}

	var matches []session.Session
	for _, s := range sessions {
		hash := session.HashID(s.ID)
		if hash == id || strings.HasPrefix(hash, id) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return session.Session{}, fmt.Errorf("session not found: %s", id)
	case 1:
		return matches[0], nil
	default:
		return session.Session{}, fmt.Errorf("session ID %q is ambiguous (%d matches)", id, len(matches))
	}
}

func ResolveCwd(cmd *cobra.Command) (string, error) {
	cwd, _ := cmd.Flags().GetString("cwd")
	if cwd != "" {
		err := os.Chdir(cwd)
		if err != nil {
			return "", fmt.Errorf("failed to change directory: %v", err)
		}
		return cwd, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %v", err)
	}
	return cwd, nil
}

func createDotAngelaDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create data directory: %q %w", dir, err)
	}

	gitIgnorePath := filepath.Join(dir, ".gitignore")
	content, err := os.ReadFile(gitIgnorePath)

	// create or update if old version
	if os.IsNotExist(err) || string(content) == oldGitIgnore {
		if err := os.WriteFile(gitIgnorePath, []byte(defaultGitIgnore), 0o644); err != nil {
			return fmt.Errorf("failed to create .gitignore file: %q %w", gitIgnorePath, err)
		}
	}

	return nil
}

//go:embed gitignore/old
var oldGitIgnore string

//go:embed gitignore/default
var defaultGitIgnore string
