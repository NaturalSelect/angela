package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newConfigTestCmd builds a standalone command carrying only the flags
// configValidateCmd's RunE reads directly (via the cmd parameter),
// bypassing cobra's normal parent/child persistent-flag inheritance the
// way newModelsTestCmd and newDirsTestCmd do.
func newConfigTestCmd(t *testing.T, cwd, dataDir string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("cwd", cwd, "")
	cmd.Flags().String("data-dir", dataDir, "")
	cmd.Flags().Bool("debug", false, "")
	return cmd
}

// writeConfigFile writes an arbitrary angela.json body, for cases where
// writeModelsFixtureConfig's fixed provider fixture doesn't apply.
func writeConfigFile(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(body), 0o644))
}

// TestConfigValidateCmd_Success covers the happy path: a valid config
// loads without error and the command reports which file it loaded plus
// an ok summary. It still reports warnings — the fixture's temp
// directory isn't a git repository and defines no explicit chore slot,
// each of which legitimately warns on its own — so this only pins the
// error-free, "ok:"-reporting contract; TestConfigValidateCmd_UnknownAgentField
// covers the warning-reporting contract specifically.
func TestConfigValidateCmd_Success(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeModelsFixtureConfig(t, cwd)

	cmd := newConfigTestCmd(t, cwd, t.TempDir())
	getOutput := swapStdoutPipe(t)

	err = configValidateCmd.RunE(cmd, nil)
	require.NoError(t, err)

	out := getOutput()
	require.Contains(t, out, "loaded:")
	require.Contains(t, out, "ok: 1 file(s) loaded")
}

// TestConfigValidateCmd_InvalidJSON covers the hard-error path: invalid
// JSON must fail the command rather than loading a partial config.
func TestConfigValidateCmd_InvalidJSON(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeConfigFile(t, cwd, "{not valid json")

	cmd := newConfigTestCmd(t, cwd, t.TempDir())

	err = configValidateCmd.RunE(cmd, nil)
	require.Error(t, err)
}

// TestConfigValidateCmd_UnknownAgentField covers the soft-warning path:
// an unrecognized field inside an agents.<id> entry drops that whole
// override at load time (load.go's "Ignoring agent with unrecognized
// configuration") without failing the command — the warning is how the
// user finds out their override silently didn't apply.
func TestConfigValidateCmd_UnknownAgentField(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeConfigFile(t, cwd, `{"agents":{"explore":{"allowed_tool":["Bash"]}}}`)

	cmd := newConfigTestCmd(t, cwd, t.TempDir())
	getOutput := swapStdoutPipe(t)

	err = configValidateCmd.RunE(cmd, nil)
	require.NoError(t, err)

	out := getOutput()
	require.Contains(t, out, "warning:")
	require.Contains(t, out, "Ignoring agent with unrecognized configuration")
}

// TestConfigValidateCmd_InvalidHookMatcher covers a hard-error path
// distinct from JSON syntax: a hook with an unparseable matcher regex
// fails validation at load time.
func TestConfigValidateCmd_InvalidHookMatcher(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeConfigFile(t, cwd, `{"hooks":{"PreToolUse":[{"command":"true","matcher":"("}]}}`)

	cmd := newConfigTestCmd(t, cwd, t.TempDir())

	err = configValidateCmd.RunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid matcher regex")
}
