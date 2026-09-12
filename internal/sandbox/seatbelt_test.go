package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeatbeltQuote(t *testing.T) {
	t.Parallel()

	q, err := seatbeltQuote("/plain/path")
	require.NoError(t, err)
	require.Equal(t, `"/plain/path"`, q)

	q, err = seatbeltQuote(`/has "quote" and \backslash`)
	require.NoError(t, err)
	require.Equal(t, `"/has \"quote\" and \\backslash"`, q)

	_, err = seatbeltQuote("/has\nnewline")
	require.Error(t, err)
}

func TestSeatbeltPathForms(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t, []string{"/tmp/x", "/private/tmp/x"}, seatbeltPathForms("/tmp/x"))
	require.ElementsMatch(t, []string{"/private/var/y", "/var/y"}, seatbeltPathForms("/private/var/y"))
	require.ElementsMatch(t, []string{"/etc", "/private/etc"}, seatbeltPathForms("/etc"))
	require.Equal(t, []string{"/home/x"}, seatbeltPathForms("/home/x"))
	require.Equal(t, []string{"/"}, seatbeltPathForms("/"))
	// "/tmpfoo" isn't actually under /tmp, so it must not alias to
	// "/private/tmpfoo".
	require.Equal(t, []string{"/tmpfoo"}, seatbeltPathForms("/tmpfoo"))
}

func TestSeatbeltPathForms_ResolvesSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	require.NoError(t, os.Mkdir(real, 0o755))
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	resolvedReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)

	forms := seatbeltPathForms(link)
	require.Contains(t, forms, filepath.ToSlash(link))
	require.Contains(t, forms, filepath.ToSlash(resolvedReal))
}

func TestSeatbeltProfile_EmptyConfig(t *testing.T) {
	t.Parallel()

	profile, err := seatbeltProfile(Config{}, "/opt/angela/angela")
	require.NoError(t, err)
	require.Equal(t, "(version 1)\n(allow default)\n", profile)
}

func TestSeatbeltProfile_WithRules(t *testing.T) {
	t.Parallel()

	profile, err := seatbeltProfile(Config{ReadOnly: []string{"/"}, ReadWrite: []string{"/work"}}, "/opt/angela/angela")
	require.NoError(t, err)
	require.Contains(t, profile, "(version 1)\n(allow default)\n")
	require.Contains(t, profile, "(deny file-read* file-write*)\n")
	require.Contains(t, profile, "(allow file-read-metadata)\n")
	require.Contains(t, profile, `(allow file-read* (subpath "/") (subpath "/work"))`)
	require.Contains(t, profile, `(allow file-write* (subpath "/work"))`)
	require.Contains(t, profile, `(allow file-read* (literal "/opt/angela/angela"))`)
	require.Contains(t, profile, `(subpath "/System")`)
	require.Contains(t, profile, `(allow file-write* (literal "/dev/null"))`)
	require.Contains(t, profile, `(literal "/dev/urandom")`)
}

// TestSeatbeltProfile_WithFileRules verifies ReadOnlyFiles and
// ReadWriteFiles get "literal" rules, not "subpath" rules: granting a
// single file must not also grant every other file in its parent
// directory, which is what a "subpath" rule for that directory would
// do.
func TestSeatbeltProfile_WithFileRules(t *testing.T) {
	t.Parallel()

	profile, err := seatbeltProfile(Config{
		ReadOnlyFiles:  []string{"/data/ro-file.txt"},
		ReadWriteFiles: []string{"/work/secrets/key.txt"},
	}, "/opt/angela/angela")
	require.NoError(t, err)
	require.Contains(t, profile, `(allow file-read* (literal "/data/ro-file.txt") (literal "/work/secrets/key.txt"))`)
	require.Contains(t, profile, `(allow file-write* (literal "/work/secrets/key.txt"))`)
	require.NotContains(t, profile, `(subpath "/work/secrets")`)
	require.NotContains(t, profile, `(subpath "/data")`)
}

func TestSeatbeltProfile_RejectsControlCharacters(t *testing.T) {
	t.Parallel()

	_, err := seatbeltProfile(Config{ReadWrite: []string{"/tmp/has\nnewline"}}, "/opt/angela/angela")
	require.Error(t, err)
}

func TestSeatbeltRelaunchArgv(t *testing.T) {
	t.Parallel()

	argv := seatbeltRelaunchArgv("(version 1)\n(allow default)\n", "/opt/angela/angela", []string{"--sandbox", "run", "hello"})
	require.Equal(t, []string{"sandbox-exec", "-p", "(version 1)\n(allow default)\n", "--", "/opt/angela/angela", "--sandbox", "run", "hello"}, argv)
}

func TestSeatbeltSandbox_IsInSandbox_FollowsMarker(t *testing.T) {
	t.Setenv(seatbeltMarkerEnv, "")
	require.False(t, (SeatbeltSandbox{}).IsInSandbox())

	t.Setenv(seatbeltMarkerEnv, "1")
	require.True(t, (SeatbeltSandbox{}).IsInSandbox())
}

// TestSeatbeltSandbox_EnterSandbox_AlreadyEntered verifies a second
// EnterSandbox call, once the marker a prior relaunch set is already
// present, is a no-op rather than attempting to relaunch again, which
// would either loop or fail depending on whether the running sandbox
// permits a nested sandbox-exec. Safe on every platform: it returns
// before ever calling relaunchUnderSeatbelt.
func TestSeatbeltSandbox_EnterSandbox_AlreadyEntered(t *testing.T) {
	t.Setenv(seatbeltMarkerEnv, "1")
	require.NoError(t, (SeatbeltSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}}))
}

// TestSeatbeltSandbox_EnterSandbox_AfterStartup verifies EnterSandbox
// refuses to relaunch once MarkStartupComplete has been called: by
// then the process may hold a database connection, background
// goroutines, or TUI state a relaunch would silently discard. Safe on
// every platform: it returns before ever calling
// relaunchUnderSeatbelt. Not parallel: it mutates the shared
// startupComplete and restrictChildNetwork package vars, saving and
// restoring both so they don't leak into other tests.
func TestSeatbeltSandbox_EnterSandbox_AfterStartup(t *testing.T) {
	origStartup := startupComplete.Load()
	t.Cleanup(func() { startupComplete.Store(origStartup) })
	startupComplete.Store(true)

	origNetwork := restrictChildNetwork.Load()
	t.Cleanup(func() { restrictChildNetwork.Store(origNetwork) })
	restrictChildNetwork.Store(false)

	t.Setenv(seatbeltMarkerEnv, "")
	err := (SeatbeltSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}, AllowNetwork: false})
	require.ErrorIs(t, err, ErrNotSupported)
	require.False(t, ShouldRestrictChildNetwork(), "macOS EnterSandbox must never mark children for network restriction")
}
