package cmd

import (
	"testing"

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

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data")
	require.NoError(t, err)
	require.False(t, enabled)
	require.Zero(t, cfg)
}

func TestSandboxConfigFromFlags_DefaultsOnly(t *testing.T) {
	t.Parallel()

	cmd := newSandboxTestCmd(t)
	require.NoError(t, cmd.Flags().Set("sandbox", "true"))

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data")
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

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data")
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

	cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data")
	require.NoError(t, err)
	require.True(t, enabled)
	require.False(t, cfg.AllowNetwork)
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

			cfg, enabled, err := sandboxConfigFromFlags(cmd, "/work", "/data")
			require.Error(t, err)
			require.Contains(t, err.Error(), "--sandbox")
			require.False(t, enabled)
			require.Zero(t, cfg)
		})
	}
}
