package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newModelsTestCmd builds a standalone command carrying only the flags
// modelsCmd's RunE reads directly (via the cmd parameter), bypassing
// cobra's normal parent/child persistent-flag inheritance the way
// newDirsTestCmd and newSessionRunCmd do.
func newModelsTestCmd(t *testing.T, cwd, dataDir string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("cwd", cwd, "")
	cmd.Flags().String("data-dir", dataDir, "")
	cmd.Flags().Bool("debug", false, "")
	return cmd
}

// writeModelsFixtureConfig writes an angela.json defining a custom,
// fully-configured provider with two explicit models. The models are
// non-empty so config loading never triggers live model discovery
// against the fake base_url.
func writeModelsFixtureConfig(t *testing.T, dir string) {
	t.Helper()
	body := `{
  "providers": {
    "zztestprovider": {
      "name": "ZZ Test Provider",
      "type": "openai",
      "api_key": "test-key-not-real",
      "base_url": "https://zztest.invalid/v1",
      "models": [
        {"id": "zztest-model-large", "name": "ZZ Test Model Large"},
        {"id": "zztest-model-small", "name": "ZZ Test Model Small"}
      ]
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(body), 0o644))
}

// TestModelsCmd_SearchFiltering covers modelsCmd's non-tty listing
// branch (the one exercised under `go test`, since stdout is never a
// terminal there): no search term lists every configured model, a term
// narrows to matching models by id or provider id, and matching is
// case-insensitive.
func TestModelsCmd_SearchFiltering(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeModelsFixtureConfig(t, cwd)

	tests := []struct {
		name        string
		args        []string
		wantContain []string
		wantAbsent  []string
	}{
		{
			name: "no search term lists everything configured",
			args: nil,
			wantContain: []string{
				"zztestprovider/zztest-model-large",
				"zztestprovider/zztest-model-small",
			},
		},
		{
			name:        "search term matches one model by id",
			args:        []string{"zztest-model-large"},
			wantContain: []string{"zztestprovider/zztest-model-large"},
			wantAbsent:  []string{"zztestprovider/zztest-model-small"},
		},
		{
			name: "search term matches by provider id",
			args: []string{"zztestprovider"},
			wantContain: []string{
				"zztestprovider/zztest-model-large",
				"zztestprovider/zztest-model-small",
			},
		},
		{
			name:        "search is case-insensitive",
			args:        []string{"ZZTEST-MODEL-LARGE"},
			wantContain: []string{"zztestprovider/zztest-model-large"},
			wantAbsent:  []string{"zztestprovider/zztest-model-small"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newModelsTestCmd(t, cwd, t.TempDir())
			getOutput := swapStdoutPipe(t)

			err := modelsCmd.RunE(cmd, tt.args)
			require.NoError(t, err)

			out := getOutput()
			for _, want := range tt.wantContain {
				require.Contains(t, out, want)
			}
			for _, absent := range tt.wantAbsent {
				require.NotContains(t, out, absent)
			}
		})
	}
}

// TestModelsCmd_NoMatchingProviders covers the "no providers found
// matching %q" error path. The search term is deliberately nonsensical
// so it cannot match any real Catwalk-known provider or model either,
// keeping the assertion valid regardless of what the process-wide
// Catwalk provider cache happens to hold from other tests.
func TestModelsCmd_NoMatchingProviders(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeModelsFixtureConfig(t, cwd)

	cmd := newModelsTestCmd(t, cwd, t.TempDir())

	term := "zz-definitely-not-a-real-model-xyz-987"
	err = modelsCmd.RunE(cmd, []string{term})
	require.Error(t, err)
	require.Contains(t, err.Error(), `no providers found matching "zz-definitely-not-a-real-model-xyz-987"`)
}

// TestModelsCmd_PropagatesConfigLoadError covers the error return from
// config.Init: a malformed angela.json must fail the command rather
// than being silently ignored.
func TestModelsCmd_PropagatesConfigLoadError(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "angela.json"), []byte("{not valid json"), 0o644))

	cmd := newModelsTestCmd(t, cwd, t.TempDir())

	err = modelsCmd.RunE(cmd, nil)
	require.Error(t, err)
}

// TestModelsCmd_ResolveCwdErrorPropagates covers the ResolveCwd error
// path: an explicit --cwd flag pointing at a non-existent directory
// must fail the command before any config loading is attempted.
func TestModelsCmd_ResolveCwdErrorPropagates(t *testing.T) {
	isolateSessionEnv(t)
	cmd := newModelsTestCmd(t, filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir())

	err := modelsCmd.RunE(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to change directory")
}
