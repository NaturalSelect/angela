package editorapproval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeLock(t *testing.T, dir, name string, lock lockFile) {
	t.Helper()
	data, err := json.Marshal(lock)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
}

func TestLockFileJSON_MatchesUpstreamShape(t *testing.T) {
	// Cross-checked against VS Code's Copilot Chat extension source
	// (vscode-node/lockFile.ts LockFileInfo) so a tag typo here fails
	// loudly instead of silently dropping a field at runtime.
	raw := `{
		"socketPath": "/tmp/mcp.sock",
		"scheme": "unix",
		"headers": {"Authorization": "Nonce abc-123"},
		"pid": 4242,
		"ideName": "Visual Studio Code",
		"timestamp": 1700000000000,
		"workspaceFolders": ["/home/user/project"],
		"isTrusted": true
	}`
	var lock lockFile
	require.NoError(t, json.Unmarshal([]byte(raw), &lock))
	require.Equal(t, lockFile{
		SocketPath:       "/tmp/mcp.sock",
		Scheme:           "unix",
		Headers:          map[string]string{"Authorization": "Nonce abc-123"},
		PID:              4242,
		IDEName:          "Visual Studio Code",
		Timestamp:        1700000000000,
		WorkspaceFolders: []string{"/home/user/project"},
		IsTrusted:        true,
	}, lock)
}

func TestFindLock_StalePIDSkipped(t *testing.T) {
	dir, work := t.TempDir(), t.TempDir()
	writeLock(t, dir, "a.lock", lockFile{
		SocketPath: "/tmp/sock", Scheme: "unix",
		PID:              999999999,
		WorkspaceFolders: []string{work},
		Timestamp:        1,
	})

	_, ok := findLock(dir, work)
	require.False(t, ok)
}

func TestFindLock_WorkspaceMismatchSkipped(t *testing.T) {
	dir, work, other := t.TempDir(), t.TempDir(), t.TempDir()
	writeLock(t, dir, "a.lock", lockFile{
		SocketPath: "/tmp/sock", Scheme: "unix",
		PID:              os.Getpid(),
		WorkspaceFolders: []string{other},
		Timestamp:        1,
	})

	_, ok := findLock(dir, work)
	require.False(t, ok)
}

func TestFindLock_UnsupportedSchemeSkipped(t *testing.T) {
	dir, work := t.TempDir(), t.TempDir()
	writeLock(t, dir, "a.lock", lockFile{
		SocketPath: "/tmp/sock", Scheme: "tcp",
		PID:              os.Getpid(),
		WorkspaceFolders: []string{work},
		Timestamp:        1,
	})

	_, ok := findLock(dir, work)
	require.False(t, ok)
}

func TestFindLock_NestedWorkingDirMatched(t *testing.T) {
	dir, work := t.TempDir(), t.TempDir()
	nested := filepath.Join(work, "sub", "dir")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	writeLock(t, dir, "a.lock", lockFile{
		SocketPath: "/tmp/sock", Scheme: "unix",
		PID:              os.Getpid(),
		WorkspaceFolders: []string{work},
		Timestamp:        1,
	})

	got, ok := findLock(dir, nested)
	require.True(t, ok)
	require.Equal(t, "/tmp/sock", got.SocketPath)
}

func TestFindLock_NewestWins(t *testing.T) {
	dir, work := t.TempDir(), t.TempDir()
	writeLock(t, dir, "old.lock", lockFile{
		SocketPath: "old", Scheme: "unix",
		PID:              os.Getpid(),
		WorkspaceFolders: []string{work},
		Timestamp:        1,
	})
	writeLock(t, dir, "new.lock", lockFile{
		SocketPath: "new", Scheme: "unix",
		PID:              os.Getpid(),
		WorkspaceFolders: []string{work},
		Timestamp:        2,
	})

	got, ok := findLock(dir, work)
	require.True(t, ok)
	require.Equal(t, "new", got.SocketPath)
}

func TestFindLock_MalformedJSONSkipped(t *testing.T) {
	dir, work := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.lock"), []byte("{not json"), 0o600))
	writeLock(t, dir, "good.lock", lockFile{
		SocketPath: "good", Scheme: "unix",
		PID:              os.Getpid(),
		WorkspaceFolders: []string{work},
		Timestamp:        1,
	})

	got, ok := findLock(dir, work)
	require.True(t, ok)
	require.Equal(t, "good", got.SocketPath)
}

func TestFindLock_MissingDirReturnsFalse(t *testing.T) {
	_, ok := findLock(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	require.False(t, ok)
}

func TestLockDir_DefaultsToHomeDotCopilotIde(t *testing.T) {
	got := lockDir(func(string) string { return "" })
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".copilot", "ide"), got)
}

func TestLockDir_HonorsCopilotHomeOverride(t *testing.T) {
	got := lockDir(func(key string) string {
		if key == "COPILOT_HOME" {
			return "/custom"
		}
		return ""
	})
	require.Equal(t, filepath.Join("/custom", "ide"), got)
}
