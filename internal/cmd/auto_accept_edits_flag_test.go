package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestAutoAcceptEditsFlagAvailableOnRunCmd guards against
// --auto-accept-edits being registered as a local root flag
// (rootCmd.Flags) rather than a persistent one — the same trap
// --yolo fell into, where `angela run --yolo` failed with "unknown
// flag" because runCmd does not inherit root's local flags.
func TestAutoAcceptEditsFlagAvailableOnRunCmd(t *testing.T) {
	t.Parallel()

	flag := runCmd.Flags().Lookup("auto-accept-edits")
	require.NotNil(t, flag, "the --auto-accept-edits flag must be available on `angela run` (register it as a persistent flag on rootCmd)")
	require.Equal(t, "bool", flag.Value.Type())
}

// TestAutoAcceptEditsExclusiveWithYolo covers the mutual-exclusion
// guard registered in root.go's init: --yolo already implies
// auto-accepting edits (and everything else), so allowing both flags
// at once would just obscure which one actually wins.
//
// This mutates the flag values shared by rootCmd and runCmd (cobra
// merges persistent flags by pointer, not by copy), so it must not
// run in parallel with anything else that reads or sets these flags.
func TestAutoAcceptEditsExclusiveWithYolo(t *testing.T) {
	for _, cmd := range []*cobra.Command{rootCmd, runCmd} {
		require.NoError(t, cmd.Flags().Set("yolo", "true"))
		require.NoError(t, cmd.Flags().Set("auto-accept-edits", "true"))
		t.Cleanup(func() {
			_ = cmd.Flags().Set("yolo", "false")
			_ = cmd.Flags().Set("auto-accept-edits", "false")
		})

		require.Error(t, cmd.ValidateFlagGroups(), "--yolo and --auto-accept-edits must be mutually exclusive on %s", cmd.Name())
	}
}
