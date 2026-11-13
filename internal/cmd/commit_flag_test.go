package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCommitDiffRunesFlag(t *testing.T) {
	t.Parallel()

	t.Run("nil when the command does not define the flag", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, commitDiffRunesFlag(&cobra.Command{Use: "x"}))
	})

	t.Run("nil when the flag was not passed", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().Int("max-diff-runes", 0, "")
		require.Nil(t, commitDiffRunesFlag(cmd))
	})

	t.Run("value when the flag was passed", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().Int("max-diff-runes", 0, "")
		require.NoError(t, cmd.Flags().Set("max-diff-runes", "5000"))
		got := commitDiffRunesFlag(cmd)
		require.NotNil(t, got)
		require.Equal(t, 5000, *got)
	})
}

func TestCommitCmd_DefinesMaxDiffRunesFlag(t *testing.T) {
	t.Parallel()
	flag := commitCmd.Flags().Lookup("max-diff-runes")
	require.NotNil(t, flag)
	require.Equal(t, "int", flag.Value.Type())
}

// TestCommitCmd_RejectsNonPositiveMaxDiffRunes pins that a nonsensical
// budget fails before any git or provider work happens.
//
// NOTE: Not parallel because it mutates the package-level command's flag.
func TestCommitCmd_RejectsNonPositiveMaxDiffRunes(t *testing.T) {
	flag := commitCmd.Flags().Lookup("max-diff-runes")
	require.NotNil(t, flag)
	t.Cleanup(func() {
		_ = flag.Value.Set("0")
		flag.Changed = false
	})

	for _, v := range []string{"0", "-3"} {
		require.NoError(t, commitCmd.Flags().Set("max-diff-runes", v))
		err := commitCmd.RunE(commitCmd, nil)
		require.Error(t, err, "--max-diff-runes=%s must be rejected", v)
		require.Contains(t, err.Error(), "--max-diff-runes must be positive")
	}
}
