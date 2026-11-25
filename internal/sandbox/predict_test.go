package sandbox

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfig_Restricts(t *testing.T) {
	t.Parallel()

	require.False(t, Config{}.Restricts())
	require.False(t, Config{AllowNetwork: true}.Restricts())
	require.True(t, Config{ReadWrite: []string{t.TempDir()}}.Restricts())
	require.True(t, Config{ReadWrite: []string{filepath.Join(t.TempDir(), "missing")}}.Restricts(),
		"the /dev grants keep the rules non-empty even when every named path is missing")
}

func TestConfig_AllowsUnixSocket(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	granted := filepath.Join(dir, "granted.sock")
	other := filepath.Join(dir, "other.sock")
	listenUnix(t, granted)
	listenUnix(t, other)

	t.Run("a socket on the list is allowed", func(t *testing.T) {
		t.Parallel()
		cfg := Config{ReadWrite: []string{dir}, UnixSockets: []string{granted}}
		require.True(t, cfg.AllowsUnixSocket(granted))
	})
	t.Run("a socket off the list is refused", func(t *testing.T) {
		t.Parallel()
		cfg := Config{ReadWrite: []string{dir}, UnixSockets: []string{granted}}
		require.False(t, cfg.AllowsUnixSocket(other))
	})
	t.Run("a config without rules allows every socket", func(t *testing.T) {
		t.Parallel()
		require.True(t, Config{}.AllowsUnixSocket(other))
	})
	t.Run("a granted socket that does not exist grants nothing", func(t *testing.T) {
		t.Parallel()
		gone := filepath.Join(dir, "gone.sock")
		cfg := Config{ReadWrite: []string{dir}, UnixSockets: []string{gone}}
		require.False(t, cfg.AllowsUnixSocket(gone))
	})
}

// NOTE: Not parallel, because it swaps the process-wide applied rules.
func TestConfig_AllowsUnixSocket_MatchesTheRulesOnceApplied(t *testing.T) {
	dir := t.TempDir()
	granted := filepath.Join(dir, "granted.sock")
	other := filepath.Join(dir, "other.sock")
	listenUnix(t, granted)
	listenUnix(t, other)

	cfg := Config{ReadWrite: []string{dir}, UnixSockets: []string{granted}}
	predicted := map[string]bool{granted: cfg.AllowsUnixSocket(granted), other: cfg.AllowsUnixSocket(other)}

	rs := resolve(cfg).existing()
	setSandboxStateForTest(t, true, false, &rs)

	for path, want := range predicted {
		require.Equal(t, want, AllowsUnixSocket(path), path)
	}
}
