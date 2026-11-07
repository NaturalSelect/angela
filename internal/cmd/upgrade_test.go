package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpgradeCmdRegistered(t *testing.T) {
	t.Parallel()

	found, _, err := rootCmd.Find([]string{"upgrade"})
	require.NoError(t, err)
	require.Same(t, upgradeCmd, found)
}

func TestUpgradeCmdPreReleaseFlag(t *testing.T) {
	t.Parallel()

	flag := upgradeCmd.Flags().Lookup("pre-release")
	require.NotNil(t, flag, "`angela upgrade` must accept --pre-release")
	require.Equal(t, "bool", flag.Value.Type())
	require.Equal(t, "false", flag.DefValue, "pre-releases must be opt-in")
}

func TestUpgradeCmdRejectsArguments(t *testing.T) {
	t.Parallel()

	require.Error(t, upgradeCmd.Args(upgradeCmd, []string{"v0.0.5"}))
}
