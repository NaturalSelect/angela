package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// TestUpdateProvidersCmd_LocalFile exercises the local-file branch of
// `angela update-providers <path>`, which needs no network access. The
// http(s):// and default-URL branches are out of scope here since they
// reach a real Catwalk endpoint.
func TestUpdateProvidersCmd_LocalFile(t *testing.T) {
	restoreLogger(t)

	validProviders := `[{"id":"test-provider","name":"Test Provider","models":[{"id":"m1","name":"Model One"}]}]`

	tests := []struct {
		name          string
		fileContent   string
		skipFile      bool
		wantErrSubstr string
	}{
		{
			name:        "valid provider file is cached",
			fileContent: validProviders,
		},
		{
			name:          "missing file surfaces a read error",
			skipFile:      true,
			wantErrSubstr: "failed to read file",
		},
		{
			name:          "invalid JSON surfaces an unmarshal error",
			fileContent:   `{not json`,
			wantErrSubstr: "failed to unmarshal provider data",
		},
		{
			name:          "empty provider list is rejected",
			fileContent:   `[]`,
			wantErrSubstr: "no providers found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)

			sourceDir := t.TempDir()
			sourcePath := filepath.Join(sourceDir, "providers.json")
			if !tt.skipFile {
				require.NoError(t, os.WriteFile(sourcePath, []byte(tt.fileContent), 0o644))
			}

			err := updateProvidersCmd.RunE(updateProvidersCmd, []string{sourcePath})

			if tt.wantErrSubstr != "" {
				require.ErrorContains(t, err, tt.wantErrSubstr)
				return
			}
			require.NoError(t, err)

			cachePath := filepath.Join(dataHome, "angela", "providers.json")
			cached, err := os.ReadFile(cachePath)
			require.NoError(t, err)

			var providers []catwalk.Provider
			require.NoError(t, json.Unmarshal(cached, &providers))
			require.Len(t, providers, 1)
			require.Equal(t, catwalk.InferenceProvider("test-provider"), providers[0].ID)
			require.Equal(t, "Test Provider", providers[0].Name)
		})
	}
}
