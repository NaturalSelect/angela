package cmd

import (
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newSandboxTestCmd builds a standalone command carrying only the
// flags sandboxConfigFromFlags reads.
func newSandboxTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	addSandboxFlags(cmd)
	return cmd
}

func TestSandboxConfigFromFlags_Disabled(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", nil)
	require.NoError(t, err)
	require.False(t, enabled)
	require.Zero(t, cfg)
}

func TestSandboxConfigFromFlags_DefaultsOnly(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("sandbox", "true"))

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", nil)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Contains(t, cfg.ReadWrite, "/work")
	require.Contains(t, cfg.ReadWrite, "/data")
	require.Equal(t, []string{"/"}, cfg.ReadOnly)
	require.True(t, cfg.AllowNetwork)
}

// TestSandboxConfigFromFlags_AppendsExtraPaths covers the additive
// semantics: --sandbox-rw/--sandbox-ro add to the default set rather
// than replacing it, so Angela's own data directory always stays
// writable.
func TestSandboxConfigFromFlags_AppendsExtraPaths(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("sandbox", "true"))
	require.NoError(t, cmd.Flags().Set("sandbox-rw", "/extra-rw"))
	require.NoError(t, cmd.Flags().Set("sandbox-ro", "/extra-ro"))

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", nil)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Contains(t, cfg.ReadWrite, "/work")
	require.Contains(t, cfg.ReadWrite, "/data")
	require.Contains(t, cfg.ReadWrite, "/extra-rw")
	require.Contains(t, cfg.ReadOnly, "/")
	require.Contains(t, cfg.ReadOnly, "/extra-ro")
}

func TestSandboxConfigFromFlags_NoNetwork(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("sandbox", "true"))
	require.NoError(t, cmd.Flags().Set("sandbox-no-network", "true"))

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", nil)
	require.NoError(t, err)
	require.True(t, enabled)
	require.False(t, cfg.AllowNetwork)
}

// TestSandboxConfigFromFlags_NoDockerSandboxDoesNotRequireSandbox
// covers a case --no-docker-sandbox is deliberately exempt from:
// unlike the --sandbox-* refinement flags, it also governs the
// standalone App.Sandbox behind IsInSandbox() (e.g. the /sandbox TUI
// command's visibility), which applies whether or not --sandbox was
// passed at startup. So, unlike sandboxFlagNames entries, it must
// stay usable on its own instead of being rejected.
func TestSandboxConfigFromFlags_NoDockerSandboxDoesNotRequireSandbox(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("no-docker-sandbox", "true"))

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", nil)
	require.NoError(t, err)
	require.False(t, enabled)
	require.Zero(t, cfg)
}

// TestSandboxConfigFromFlags_RequiresSandboxFlag covers every
// refinement flag: setting it without --sandbox is rejected rather
// than silently ignored.
func TestSandboxConfigFromFlags_RequiresSandboxFlag(t *testing.T) {
	t.Parallel()

	for _, name := range sandboxFlagNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := newSandboxTestCmd(t)
			value := "true"
			if name != "sandbox-no-network" {
				value = "/extra"
			}
			require.NoError(t, cmd.Flags().Set(name, value))

			cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "--sandbox")
			require.False(t, enabled)
			require.Zero(t, cfg)
		})
	}
}

// TestSandboxConfigFromFlags_PermissionRulesAddPaths covers the
// permission-rule overlay: an unconditional allow rule for a
// filesystem category widens the sandbox to match it, so a path the
// permission policy already approves without a prompt doesn't also
// need an OS-level grant added by hand. A rule for a non-filesystem
// category (execute) contributes nothing, since its pattern isn't a
// path.
func TestSandboxConfigFromFlags_PermissionRulesAddPaths(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("sandbox", "true"))

	permissions := &config.Permissions{
		Rules: []permission.Rule{
			{Action: permission.RuleAllow, Tool: "edit", Pattern: "external/**"},
			{Action: permission.RuleAllow, Tool: "read", Pattern: "/etc/angela/**"},
			{Action: permission.RuleAllow, Tool: "execute", Pattern: "ls *"},
		},
	}

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", permissions)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Contains(t, cfg.ReadWrite, filepath.Join("/work", "external"))
	require.Contains(t, cfg.ReadOnly, filepath.Clean("/etc/angela"))
}

// TestSandboxConfigFromFlags_LiteralFilePermissionRuleGrantsExactFile
// is the regression test for the vulnerability where a permission
// rule approving edits to a single literal file ended up widening the
// OS-level sandbox to cover its whole parent directory instead: the
// rule must land in ReadWriteFiles/ReadOnlyFiles, naming the exact
// file, and must not also appear in ReadWrite/ReadOnly.
func TestSandboxConfigFromFlags_LiteralFilePermissionRuleGrantsExactFile(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("sandbox", "true"))

	permissions := &config.Permissions{
		Rules: []permission.Rule{
			{Action: permission.RuleAllow, Tool: "edit", Pattern: "secrets/key.txt"},
		},
	}

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data", permissions)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Contains(t, cfg.ReadWriteFiles, filepath.Join("/work", "secrets", "key.txt"))
	require.NotContains(t, cfg.ReadWrite, filepath.Join("/work", "secrets"),
		"a literal file rule must not widen its whole parent directory")
}
