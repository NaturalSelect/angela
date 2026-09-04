package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newAgentTestCmd builds a standalone command carrying only the flags
// agentListCmd's RunE reads directly, the same way newModelsTestCmd
// does for modelsCmd.
func newAgentTestCmd(t *testing.T, cwd, dataDir string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("cwd", cwd, "")
	cmd.Flags().String("data-dir", dataDir, "")
	cmd.Flags().Bool("debug", false, "")
	return cmd
}

// TestAgentListCmd_ListsBuiltinAndCustomAgents covers the normal
// listing path: the header, the always-present builtin "coder"
// agent, and a custom agent defined in angela.json with an explicit
// mode/slot/description all appear in the rendered table.
func TestAgentListCmd_ListsBuiltinAndCustomAgents(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	body := `{
  "agents": {
    "myagent": {
      "description": "Custom test agent for listing",
      "mode": "subagent",
      "slot": "main"
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "angela.json"), []byte(body), 0o644))

	cmd := newAgentTestCmd(t, cwd, t.TempDir())
	getOutput := swapStdoutPipe(t)

	err = agentListCmd.RunE(cmd, nil)
	require.NoError(t, err)

	out := getOutput()
	require.Contains(t, out, "ID")
	require.Contains(t, out, "MODE")
	require.Contains(t, out, "MODEL")
	require.Contains(t, out, "DESCRIPTION")
	require.Contains(t, out, "coder")
	require.Contains(t, out, "primary")
	require.Contains(t, out, "myagent")
	require.Contains(t, out, "subagent")
	require.Contains(t, out, "Custom test agent for listing")
}

// TestAgentListCmd_ResolveCwdErrorPropagates covers the ResolveCwd
// error path: an explicit --cwd flag pointing at a non-existent
// directory must fail the command before any config loading is
// attempted.
func TestAgentListCmd_ResolveCwdErrorPropagates(t *testing.T) {
	isolateSessionEnv(t)
	cmd := newAgentTestCmd(t, filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir())

	err := agentListCmd.RunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to change directory")
}

// TestAgentListCmd_PropagatesConfigLoadError covers the error return
// from config.Init: a malformed angela.json must fail the command
// rather than being silently ignored.
func TestAgentListCmd_PropagatesConfigLoadError(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "angela.json"), []byte("{not valid json"), 0o644))

	cmd := newAgentTestCmd(t, cwd, t.TempDir())

	err = agentListCmd.RunE(cmd, nil)
	require.Error(t, err)
}
