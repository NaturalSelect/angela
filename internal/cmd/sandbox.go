package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/spf13/cobra"
)

// sandboxFlagNames lists the --sandbox-* refinement flags, i.e. every
// sandbox flag except the --sandbox switch itself.
var sandboxFlagNames = []string{"sandbox-rw", "sandbox-ro", "sandbox-no-network"}

// addSandboxFlags registers the --sandbox flag and its refinements on
// cmd.
func addSandboxFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("sandbox", false, "Restrict this process and the commands it runs to the working directory, Angela's own data directories, and any path already allowed by a permissions.rules entry, using OS-level sandboxing (Linux via Landlock, macOS via Seatbelt); outbound network is unrestricted by default")
	cmd.Flags().StringSlice("sandbox-rw", nil, "Additional read-write directory for --sandbox, on top of the default set (repeatable)")
	cmd.Flags().StringSlice("sandbox-ro", nil, "Additional read-only directory for --sandbox, on top of the default set (repeatable)")
	cmd.Flags().Bool("sandbox-no-network", false, "Block outbound network access for commands run under --sandbox, without affecting Angela's own provider requests; has no effect inside an auto-detected Docker/OCI container unless --no-docker-sandbox is also set, and is not supported on macOS: --sandbox then fails at startup")
	cmd.Flags().Bool("no-docker-sandbox", false, "Do not treat an existing Docker/OCI container as sufficient sandboxing; apply Landlock restriction as well, both under --sandbox and for the /sandbox command")
}

// sandboxConfigFromFlags builds the sandbox config from cmd's --sandbox
// flags. The returned bool reports whether --sandbox was requested; when
// false, cfg is the zero value. workingDir and dataDir seed the same
// default read-write set the /sandbox TUI dialog pre-fills, and
// --sandbox-rw/--sandbox-ro add to it rather than replacing it.
// permissions, when non-nil, contributes further: every directory its
// rules already allow without a prompt (see
// permission.FilesystemAllowPaths) is folded in too, so entering the
// sandbox never turns an already-approved edit into a confusing I/O
// error.
func sandboxConfigFromFlags(cmd *cobra.Command, workingDir, dataDir string, permissions *config.Permissions) (sandbox.Config, bool, error) {
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
	if permissions != nil {
		ruleReadOnlyDirs, ruleReadWriteDirs, ruleReadOnlyFiles, ruleReadWriteFiles := permission.FilesystemAllowPaths(permissions.Rules, workingDir)
		cfg.ReadOnly = sandbox.DedupePaths(append(cfg.ReadOnly, ruleReadOnlyDirs...))
		cfg.ReadWrite = sandbox.DedupePaths(append(cfg.ReadWrite, ruleReadWriteDirs...))
		cfg.ReadOnlyFiles = sandbox.DedupePaths(append(cfg.ReadOnlyFiles, ruleReadOnlyFiles...))
		cfg.ReadWriteFiles = sandbox.DedupePaths(append(cfg.ReadWriteFiles, ruleReadWriteFiles...))
	}
	if noNetwork {
		cfg.AllowNetwork = false
	}

	warnMissingSandboxPaths(cfg)

	return cfg, true, nil
}

// warnMissingSandboxPaths logs a warning for every path in cfg that
// doesn't exist. Both backends resolve cfg through ruleSet.existing()
// (see internal/sandbox/profile.go), which silently drops rules for
// missing paths, so entering the sandbox would otherwise succeed
// without actually restricting (or granting access to) them.
func warnMissingSandboxPaths(cfg sandbox.Config) {
	for _, p := range slices.Concat(cfg.ReadWrite, cfg.ReadOnly, cfg.ReadWriteFiles, cfg.ReadOnlyFiles) {
		if _, err := os.Stat(p); err != nil {
			slog.Warn("Sandbox path does not exist, restriction will not apply to it", "path", p)
		}
	}
}
