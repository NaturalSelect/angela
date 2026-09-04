package cmd

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/workspace"
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

// TestResolveCwd_GetwdErrorPropagates covers the os.Getwd error branch
// taken when no --cwd flag is set and the process's actual working
// directory has been removed out from under it. Not parallel: it
// mutates the process-wide working directory, like
// TestResolveCwd_ChangesDirectoryWhenFlagSet above.
func TestResolveCwd_GetwdErrorPropagates(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		// On macOS, os.Getwd (and the underlying getcwd(3)) can keep
		// resolving "." successfully for a short while after its
		// directory has been rmdir'd out from under the process,
		// unlike Linux. The removed-cwd trick below isn't reliable
		// there.
		t.Skip("os.Getwd does not reliably error after its cwd is removed on macOS")
	case "windows":
		// On Windows, making a directory the process's current
		// directory holds a handle on it without FILE_SHARE_DELETE,
		// so os.Remove below fails outright with a sharing violation
		// instead of succeeding and later making Getwd fail. The
		// removed-cwd trick can't even get started there.
		t.Skip("a process's own current directory cannot be removed on Windows")
	}

	removed := t.TempDir()
	t.Chdir(removed)
	require.NoError(t, os.Remove(removed))

	cmd := &cobra.Command{}
	cmd.Flags().String("cwd", "", "")

	_, err := ResolveCwd(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get current working directory")
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

// fakeSessionListWorkspace stubs workspace.Workspace with GetSession and
// ListSessions overridden, mirroring fakeConfigWorkspace in login_test.go.
type fakeSessionListWorkspace struct {
	workspace.Workspace
	sessions map[string]session.Session
	listErr  error
}

func (f *fakeSessionListWorkspace) GetSession(_ context.Context, id string) (session.Session, error) {
	if s, ok := f.sessions[id]; ok {
		return s, nil
	}
	return session.Session{}, errors.New("not found")
}

func (f *fakeSessionListWorkspace) ListSessions(context.Context) ([]session.Session, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]session.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
	}
	return out, nil
}

func TestResolveWorkspaceSessionID_DirectMatch(t *testing.T) {
	t.Parallel()

	want := session.Session{ID: "session-uuid-1", Title: "Direct"}
	ws := &fakeSessionListWorkspace{sessions: map[string]session.Session{want.ID: want}}

	got, err := resolveWorkspaceSessionID(t.Context(), ws, want.ID)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolveWorkspaceSessionID_HashPrefixMatch(t *testing.T) {
	t.Parallel()

	target := session.Session{ID: "session-uuid-3", Title: "Prefix match"}
	ws := &fakeSessionListWorkspace{sessions: map[string]session.Session{target.ID: target}}

	hash := session.HashID(target.ID)
	got, err := resolveWorkspaceSessionID(t.Context(), ws, hash[:6])
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolveWorkspaceSessionID_AmbiguousMatch(t *testing.T) {
	t.Parallel()

	sessions := map[string]session.Session{
		"session-uuid-4": {ID: "session-uuid-4", Title: "First"},
		"session-uuid-5": {ID: "session-uuid-5", Title: "Second"},
	}
	ws := &fakeSessionListWorkspace{sessions: sessions}

	// Every hash has "" as a prefix, so an empty query matches all sessions
	// and forces the ambiguous branch without needing a real hash collision.
	_, err := resolveWorkspaceSessionID(t.Context(), ws, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is ambiguous")
}

func TestResolveWorkspaceSessionID_NotFound(t *testing.T) {
	t.Parallel()

	ws := &fakeSessionListWorkspace{sessions: map[string]session.Session{}}

	_, err := resolveWorkspaceSessionID(t.Context(), ws, "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found: missing")
}

func TestResolveWorkspaceSessionID_ListSessionsErrorPropagates(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("db exploded")
	ws := &fakeSessionListWorkspace{listErr: wantErr}

	_, err := resolveWorkspaceSessionID(t.Context(), ws, "missing")
	require.ErrorIs(t, err, wantErr)
}

func TestCreateDotAngelaDir_CreatesDirAndGitignore(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), ".angela")
	require.NoError(t, createDotAngelaDir(dir))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	require.Equal(t, defaultGitIgnore, string(content))
}

func TestCreateDotAngelaDir_UpgradesOldGitignore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitIgnorePath := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gitIgnorePath, []byte(oldGitIgnore), 0o644))

	require.NoError(t, createDotAngelaDir(dir))

	content, err := os.ReadFile(gitIgnorePath)
	require.NoError(t, err)
	require.Equal(t, defaultGitIgnore, string(content))
}

func TestCreateDotAngelaDir_PreservesCustomGitignore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitIgnorePath := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gitIgnorePath, []byte("custom\n"), 0o644))

	require.NoError(t, createDotAngelaDir(dir))

	content, err := os.ReadFile(gitIgnorePath)
	require.NoError(t, err)
	require.Equal(t, "custom\n", string(content))
}

func TestCreateDotAngelaDir_MkdirFailurePropagates(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	err := createDotAngelaDir(filepath.Join(blocker, "sub"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create data directory")
}

// TestCreateDotAngelaDir_WriteGitignoreFailurePropagates covers the
// error branch when the directory already exists but is not writable,
// so writing a fresh .gitignore into it fails. POSIX-only: Windows
// doesn't enforce the same directory-mode write check and root bypasses
// file mode permission checks entirely.
func TestCreateDotAngelaDir_WriteGitignoreFailurePropagates(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission model")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file mode permission checks")
	}

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := createDotAngelaDir(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create .gitignore file")
}

// TestSupportsProgressBar_NotATerminalIsFalse pins the short-circuit: when
// stderr isn't a terminal, env vars that would otherwise enable progress
// bars (TERM_PROGRAM, WT_SESSION) must not matter.
func TestSupportsProgressBar_NotATerminalIsFalse(t *testing.T) {
	getOutput := swapStderrPipe(t)
	t.Cleanup(func() { getOutput() })

	t.Setenv("TERM_PROGRAM", "iterm2")
	t.Setenv("WT_SESSION", "some-session")

	require.False(t, supportsProgressBar())
}

// TestResolveCwd_ChdirErrorPropagates covers the failure path: a
// --cwd pointing at a directory that doesn't exist must fail instead
// of silently falling back to the current directory. os.Chdir never
// succeeds here, so the process's real working directory is never
// touched and this is safe to run in parallel with other tests.
func TestResolveCwd_ChdirErrorPropagates(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.Flags().String("cwd", filepath.Join(t.TempDir(), "does-not-exist"), "")

	_, err := ResolveCwd(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to change directory")
}

// TestMaybePrependStdin_NonPipeNonRegularStdinLeavesPromptUnchanged
// covers stdin that is neither a named pipe nor a regular file (e.g. a
// character device like os.DevNull): the prompt must pass through
// untouched rather than trying to read from it.
func TestMaybePrependStdin_NonPipeNonRegularStdinLeavesPromptUnchanged(t *testing.T) {
	f, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer f.Close()

	orig := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = orig }()

	got, err := MaybePrependStdin("the prompt")
	require.NoError(t, err)
	require.Equal(t, "the prompt", got)
}

// TestMaybePrependStdin_ReadErrorPropagates covers the io.ReadAll error
// branch: stdin reports as a regular file (so MaybePrependStdin commits
// to reading it) but the underlying descriptor is write-only, so the
// read itself fails and the error must propagate rather than being
// swallowed.
func TestMaybePrependStdin_ReadErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stdin.txt")
	require.NoError(t, os.WriteFile(path, []byte("unreadable"), 0o644))

	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()

	orig := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = orig }()

	got, err := MaybePrependStdin("the prompt")
	require.Error(t, err)
	require.Equal(t, "the prompt", got, "the original prompt must be returned unchanged alongside the error")
}

// TestLocalSkillsDiscoveryConfig_PropagatesOptionsAndResolver pins the
// field-by-field mapping from the loaded config to skills.DiscoveryConfig:
// each field must come from the actual store rather than a zero value.
func TestLocalSkillsDiscoveryConfig_PropagatesOptionsAndResolver(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "angela.json"), []byte(`{
		"options": {"skills_paths": ["./my-skills"], "disabled_skills": ["foo"]}
	}`), 0o644))

	store, err := config.Init(cwd, t.TempDir(), false)
	require.NoError(t, err)

	dc := localSkillsDiscoveryConfig(store)
	require.Contains(t, dc.SkillsPaths, "./my-skills", "the configured path must survive alongside any default discovery paths")
	require.Equal(t, []string{"foo"}, dc.DisabledSkills)
	require.Equal(t, store.WorkingDir(), dc.WorkingDir)
}

func TestPerHostServerDir_CreatesDirectory(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("ANGELA_CACHE_DIR", cacheDir)

	u := &url.URL{Scheme: "tcp", Host: "127.0.0.1:1234"}
	dir, err := perHostServerDir(u)
	require.NoError(t, err)

	info, statErr := os.Stat(dir)
	require.NoError(t, statErr)
	require.True(t, info.IsDir())
	require.Equal(t, filepath.Join(cacheDir, "server-"+safeHostName(u)), dir)
}

// TestPerHostServerDir_MkdirFailurePropagates covers a cache directory
// blocked by a regular file where the per-host directory needs to go.
func TestPerHostServerDir_MkdirFailurePropagates(t *testing.T) {
	cacheDir := t.TempDir()
	u := &url.URL{Scheme: "tcp", Host: "blocked:1"}
	blocker := filepath.Join(cacheDir, "server-"+safeHostName(u))
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	t.Setenv("ANGELA_CACHE_DIR", cacheDir)

	_, err := perHostServerDir(u)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create server working directory")
}
