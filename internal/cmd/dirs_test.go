package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// swapStdoutPipe redirects os.Stdout to an OS pipe for the duration of the
// test and returns a function that closes the write end and returns
// everything written to it. Restoration of the original os.Stdout is
// registered via t.Cleanup.
//
// lipgloss.Println caches its target writer in lipgloss.Writer at package
// init time, so reassigning os.Stdout alone never reaches it; this also
// repoints lipgloss.Writer at the same pipe so output from either style
// of printing is captured.
func swapStdoutPipe(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	origStdout := os.Stdout
	origLipglossWriter := lipgloss.Writer
	os.Stdout = w
	lipgloss.Writer = colorprofile.NewWriter(w, os.Environ())
	closed := false
	t.Cleanup(func() {
		if !closed {
			w.Close()
		}
		os.Stdout = origStdout
		lipgloss.Writer = origLipglossWriter
	})

	return func() string {
		w.Close()
		closed = true
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return buf.String()
	}
}

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

// TestPrintDirs_LabelsEachEntry pins the output shape: one labeled line per
// directory (Config for the first, Project for the rest) plus the trailing
// hint line, with values wide enough apart that a longer label doesn't
// clip a shorter one.
func TestPrintDirs_LabelsEachEntry(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	printDirs(newDirsTestCmd(t), []string{"/global/config", "/project/one", "/project/two"})

	out := getOutput()
	require.Contains(t, out, "Config:")
	require.Contains(t, out, "/global/config")
	require.Contains(t, out, "Project:")
	require.Contains(t, out, "/project/one")
	require.Contains(t, out, "/project/two")
	require.Contains(t, out, "Configs merge from top to bottom")
}

// TestPrintDirs_EmptyDirs covers the degenerate case: no entries still
// prints the trailing hint without panicking on an empty label width.
func TestPrintDirs_EmptyDirs(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	require.NotPanics(t, func() { printDirs(newDirsTestCmd(t), nil) })

	require.Contains(t, getOutput(), "Configs merge from top to bottom")
}
