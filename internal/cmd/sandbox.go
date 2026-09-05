package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/spf13/cobra"
)

// sandboxFlagNames lists the --sandbox-* refinement flags, i.e. every
// sandbox flag except the --sandbox switch itself.
var sandboxFlagNames = []string{"sandbox-rw", "sandbox-ro", "sandbox-no-network"}

// addSandboxFlags registers the --sandbox flag and its refinements on
// cmd.
func addSandboxFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("sandbox", false, "Restrict this process to the working directory, Angela's own data directories, and (by default) outbound network access, using OS-level sandboxing (Linux/Landlock only)")
	cmd.Flags().StringSlice("sandbox-rw", nil, "Additional read-write directory for --sandbox, on top of the default set (repeatable)")
	cmd.Flags().StringSlice("sandbox-ro", nil, "Additional read-only directory for --sandbox, on top of the default set (repeatable)")
	cmd.Flags().Bool("sandbox-no-network", false, "Block outbound network access under --sandbox; since the restriction is process-wide, this also blocks Angela's own provider requests")
}

// sandboxConfigFromFlags builds the sandbox config from cmd's --sandbox
// flags. The returned bool reports whether --sandbox was requested; when
// false, cfg is the zero value. workingDir and dataDir seed the same
// default read-write set the /sandbox TUI dialog pre-fills, and
// --sandbox-rw/--sandbox-ro add to it rather than replacing it.
func sandboxConfigFromFlags(cmd *cobra.Command, workingDir, dataDir string) (sandbox.Config, bool, error) {
	enabled, _ := cmd.Flags().GetBool("sandbox")
	if !enabled {
		for _, name := range sandboxFlagNames {
			if cmd.Flags().Changed(name) {
				return sandbox.Config{}, false, fmt.Errorf("--%s requires --sandbox", name)
			}
		}
		return sandbox.Config{}, false, nil
	}

	rw, _ := cmd.Flags().GetStringSlice("sandbox-rw")
	ro, _ := cmd.Flags().GetStringSlice("sandbox-ro")
	noNetwork, _ := cmd.Flags().GetBool("sandbox-no-network")

	cfg := sandbox.DefaultConfig(workingDir, dataDir, filepath.Dir(config.GlobalConfig()))
	cfg.ReadWrite = sandbox.DedupePaths(append(cfg.ReadWrite, rw...))
	cfg.ReadOnly = sandbox.DedupePaths(append(cfg.ReadOnly, ro...))
	if noNetwork {
		cfg.AllowNetwork = false
	}

	warnMissingSandboxPaths(cfg)

	return cfg, true, nil
}

// warnMissingSandboxPaths logs a warning for every path in cfg that
// doesn't exist. Landlock's IgnoreIfMissing mode silently drops rules
// for missing paths, so entering the sandbox would otherwise succeed
// without actually restricting (or granting access to) them.
func warnMissingSandboxPaths(cfg sandbox.Config) {
	for _, p := range cfg.ReadWrite {
		if _, err := os.Stat(p); err != nil {
			slog.Warn("Sandbox path does not exist, restriction will not apply to it", "path", p)
		}
	}
	for _, p := range cfg.ReadOnly {
		if _, err := os.Stat(p); err != nil {
			slog.Warn("Sandbox path does not exist, restriction will not apply to it", "path", p)
		}
	}
}
