package cmd

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestUseClientServer(t *testing.T) {
	tests := []struct {
		name  string
		value string
		unset bool
		want  bool
	}{
		{"unset defaults to false", "", true, false},
		{"empty string is false", "", false, false},
		{"true enables it", "true", false, true},
		{"1 enables it", "1", false, true},
		{"false disables it", "false", false, false},
		{"garbage is false", "not-a-bool", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unset {
				t.Setenv("ANGELA_CLIENT_SERVER", "")
			} else {
				t.Setenv("ANGELA_CLIENT_SERVER", tt.value)
			}
			require.Equal(t, tt.want, useClientServer())
		})
	}
}

func TestShouldEnableMetrics(t *testing.T) {
	tests := []struct {
		name           string
		disableMetrics string
		doNotTrack     string
		cfgDisabled    bool
		want           bool
	}{
		{"enabled by default", "", "", false, true},
		{"ANGELA_DISABLE_METRICS wins", "true", "", false, false},
		{"DO_NOT_TRACK wins", "", "true", false, false},
		{"config option wins", "", "", true, false},
		{"false-ish env vars do not disable", "false", "0", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANGELA_DISABLE_METRICS", tt.disableMetrics)
			t.Setenv("DO_NOT_TRACK", tt.doNotTrack)

			cfg := &config.Config{Options: &config.Options{DisableMetrics: tt.cfgDisabled}}
			require.Equal(t, tt.want, shouldEnableMetrics(cfg))
		})
	}
}

func TestSafeHostName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  *url.URL
		want string
	}{
		{
			name: "unix socket path becomes underscores",
			url:  &url.URL{Scheme: "unix", Host: "", Path: "/tmp/angela/server.sock"},
			want: "unix____tmp_angela_server.sock",
		},
		{
			name: "tcp host and port keep dot-separated address",
			url:  &url.URL{Scheme: "tcp", Host: "127.0.0.1:1234"},
			want: "tcp___127.0.0.1_1234",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, safeHostName(tt.url))
		})
	}
}

func TestServerReadyTimeout(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"unset uses default", "", 10 * time.Second},
		{"valid duration is honored", "2s", 2 * time.Second},
		{"invalid duration falls back to default", "not-a-duration", 10 * time.Second},
		{"negative duration falls back to default", "-5s", 10 * time.Second},
		{"zero falls back to default", "0s", 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANGELA_SERVER_READY_TIMEOUT", tt.value)
			require.Equal(t, tt.want, serverReadyTimeout())
		})
	}
}

func TestResolveCwd_DefaultsToGetwd(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("cwd", "", "")

	wantCwd, err := os.Getwd()
	require.NoError(t, err)

	got, err := ResolveCwd(cmd)
	require.NoError(t, err)
	require.Equal(t, wantCwd, got)
}

func TestResolveCwd_ChangesDirectoryWhenFlagSet(t *testing.T) {
	target := t.TempDir()
	t.Chdir(t.TempDir())

	cmd := &cobra.Command{}
	cmd.Flags().String("cwd", target, "")

	got, err := ResolveCwd(cmd)
	require.NoError(t, err)
	require.Equal(t, target, got)

	afterCwd, err := os.Getwd()
	require.NoError(t, err)

	resolvedTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	resolvedAfter, err := filepath.EvalSymlinks(afterCwd)
	require.NoError(t, err)
	require.Equal(t, resolvedTarget, resolvedAfter, "the process cwd must actually change to the requested dir")
}

// TestRandomExitMessage pins the two invariants callers rely on: every
// message is short enough for a single status line, and the choice is
// actually randomized rather than a hardcoded string.
func TestRandomExitMessage(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for range 200 {
		msg := randomExitMessage()
		require.LessOrEqual(t, len(msg), 40, "exit message must fit a single status line: %q", msg)
		seen[msg] = true
	}
	require.Greater(t, len(seen), 1, "randomExitMessage should not always return the same message")
}

func TestMaybePrependStdin_PipedInputIsPrepended(t *testing.T) {
	swapStdinPipe(t, "piped context")

	got, err := MaybePrependStdin("the prompt")
	require.NoError(t, err)
	require.Equal(t, "piped context\n\nthe prompt", got)
}

func TestMaybePrependStdin_RegularFileIsPrepended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stdin.txt")
	require.NoError(t, os.WriteFile(path, []byte("file context"), 0o644))

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	orig := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = orig }()

	got, err := MaybePrependStdin("the prompt")
	require.NoError(t, err)
	require.Equal(t, "file context\n\nthe prompt", got)
}

// swapStdinPipe replaces os.Stdin with the read end of an OS pipe carrying
// content, restoring the original via t.Cleanup. The pipe's read end
// reports as a named pipe under os.Stat, matching how a shell-piped
// `cmd | angela run` invocation looks to MaybePrependStdin.
func swapStdinPipe(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	_, err = io.WriteString(w, content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })
}
