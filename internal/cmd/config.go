package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configValidateCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and validate configuration",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Load angela.json and report errors and warnings",
	Long: `Load the effective configuration the same way angela itself does —
merging every discovered layer (system, global, project, workspace) — and
report what happened:

  - hard errors that would stop angela from starting
  - warnings for configuration that loaded but was ignored or adjusted,
    such as an agent override dropped for an unrecognized field

A warning does not fail the command; angela would start anyway, just not
with the setting the warning refers to.

Loading configuration can have side effects also seen when running
"angela models" or "angela agent list": it may fetch the provider catalog
from Catwalk (unless ANGELA_DISABLE_PROVIDER_AUTO_UPDATE is set) and may
write back to the global config file, e.g. to record a valid selected
model.`,
	Example: `# Validate the effective configuration
angela config validate`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}

		dataDir, _ := cmd.Flags().GetString("data-dir")
		debug, _ := cmd.Flags().GetBool("debug")

		// config.Init logs configuration problems as warnings rather
		// than returning them as diagnostics (see the FIXME on
		// Execute's slog.DiscardHandler setup), so this is the only
		// way to surface them here instead of discarding them.
		collector := &warnCollector{}
		prev := slog.Default()
		slog.SetDefault(slog.New(collector))
		defer slog.SetDefault(prev)

		store, err := config.Init(cwd, dataDir, debug)
		if err != nil {
			return err
		}

		paths := store.LoadedPaths()
		for _, p := range paths {
			fmt.Println("loaded:", p)
		}

		warnings := collector.messages()
		for _, w := range warnings {
			fmt.Println("warning:", w)
		}

		fmt.Printf("ok: %d file(s) loaded, %d warning(s)\n", len(paths), len(warnings))
		return nil
	},
}

// warnCollector is a slog.Handler that captures warning-and-above log
// records instead of letting them reach Execute's discard handler, so
// configValidateCmd can print them instead of silently losing them.
type warnCollector struct {
	mu  sync.Mutex
	msg []string
}

func (c *warnCollector) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (c *warnCollector) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " %s=%v", a.Key, a.Value.Any())
		return true
	})

	c.mu.Lock()
	c.msg = append(c.msg, sb.String())
	c.mu.Unlock()
	return nil
}

func (c *warnCollector) WithAttrs(_ []slog.Attr) slog.Handler { return c }

func (c *warnCollector) WithGroup(_ string) slog.Handler { return c }

func (c *warnCollector) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.msg...)
}
