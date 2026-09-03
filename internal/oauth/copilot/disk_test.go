package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// setTokenHome points tokenFilePath at a throwaway directory so tests
// never touch the developer's real Copilot credentials.
func setTokenHome(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", dir)
	} else {
		t.Setenv("HOME", dir)
	}
}

func TestRefreshTokenFromDisk(t *testing.T) {
	t.Run("returns token when app entry present", func(t *testing.T) {
		setTokenHome(t, t.TempDir())

		content := map[string]any{
			"github.com:Iv1.b507a08c87ecfe98": map[string]string{
				"user":        "octocat",
				"oauth_token": "gho_test123",
				"githubAppId": "Iv1.b507a08c87ecfe98",
			},
		}
		data, err := json.Marshal(content)
		require.NoError(t, err)

		path := tokenFilePath()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, data, 0o644))

		token, ok := RefreshTokenFromDisk()
		require.True(t, ok)
		require.Equal(t, "gho_test123", token)
	})

	t.Run("returns false when file missing", func(t *testing.T) {
		setTokenHome(t, t.TempDir())

		_, ok := RefreshTokenFromDisk()
		require.False(t, ok)
	})

	t.Run("returns false when app entry absent", func(t *testing.T) {
		setTokenHome(t, t.TempDir())

		path := tokenFilePath()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(`{"github.com:other-app":{"oauth_token":"x"}}`), 0o644))

		_, ok := RefreshTokenFromDisk()
		require.False(t, ok)
	})

	t.Run("returns false on invalid json", func(t *testing.T) {
		setTokenHome(t, t.TempDir())

		path := tokenFilePath()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

		_, ok := RefreshTokenFromDisk()
		require.False(t, ok)
	})
}

func TestHeaders(t *testing.T) {
	h := Headers()
	require.Equal(t, userAgent, h["User-Agent"])
	require.Equal(t, editorVersion, h["Editor-Version"])
	require.Equal(t, editorPluginVersion, h["Editor-Plugin-Version"])
	require.Equal(t, integrationID, h["Copilot-Integration-Id"])
}
