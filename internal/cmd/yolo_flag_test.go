package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestYoloFlagAvailableOnRunCmd guards against --yolo and --no-yolo-merge
// being registered as local root flags (rootCmd.Flags) rather than
// persistent ones. When local, `angela run --yolo` fails with "unknown
// flag" because runCmd does not inherit root's local flags — and a
// headless run has no one to answer a prompt the permission ladder
// cannot already settle on its own, so it needs --yolo to be reachable
// at least as much as interactive mode does.
func TestYoloFlagAvailableOnRunCmd(t *testing.T) {
	t.Parallel()

	flag := runCmd.Flags().Lookup("yolo")
	require.NotNil(t, flag, "the --yolo flag must be available on `angela run` (register it as a persistent flag on rootCmd)")
	require.Equal(t, "bool", flag.Value.Type())

	mergeFlag := runCmd.Flags().Lookup("no-yolo-merge")
	require.NotNil(t, mergeFlag, "the --no-yolo-merge flag must be available on `angela run` (register it as a persistent flag on rootCmd)")
	require.Equal(t, "bool", mergeFlag.Value.Type())
}

// TestYoloFlagAvailableOnRootCmd ensures the flags are still present on
// the root command for interactive mode.
func TestYoloFlagAvailableOnRootCmd(t *testing.T) {
	t.Parallel()

	flag := rootCmd.Flags().Lookup("yolo")
	if flag == nil {
		flag = rootCmd.PersistentFlags().Lookup("yolo")
	}
	require.NotNil(t, flag, "the --yolo flag must be available on `angela` (rootCmd)")
}
