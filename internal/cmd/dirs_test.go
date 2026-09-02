package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestDirLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		i    int
		want string
	}{
		{"index zero is Config", 0, "Config"},
		{"index one is Project", 1, "Project"},
		{"later indexes are Project", 4, "Project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, dirLabel(tt.i))
		})
	}
}

func newDirsTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("cwd", "", "")
	return cmd
}

// TestCollectDirs_GlobalAndSystemOnly covers a project directory with no
// angela.json: only the global config dir and the fixed system config dir
// should be listed, and the global dir must not be duplicated even though
// config.ProjectConfigs always includes it too.
func TestCollectDirs_GlobalAndSystemOnly(t *testing.T) {
	t.Setenv("ANGELA_GLOBAL_CONFIG", "")
	xdgConfig := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)

	t.Chdir(t.TempDir())

	dirs := collectDirs(newDirsTestCmd(t))

	require.NotEmpty(t, dirs)
	require.Equal(t, filepath.Dir(config.GlobalConfig()), dirs[0])
	for _, d := range dirs[1:] {
		require.NotEqual(t, dirs[0], d, "the global config dir must not be listed twice")
	}
}

// TestCollectDirs_IncludesProjectConfig covers the case that motivates the
// command: a project-local angela.json must show up in the listing.
func TestCollectDirs_IncludesProjectConfig(t *testing.T) {
	t.Setenv("ANGELA_GLOBAL_CONFIG", "")
	xdgConfig := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)

	t.Chdir(t.TempDir())
	projectDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "angela.json"), []byte("{}"), 0o644))

	dirs := collectDirs(newDirsTestCmd(t))

	require.Contains(t, dirs, projectDir)
}
