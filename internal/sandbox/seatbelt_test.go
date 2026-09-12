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

	profile, err := seatbeltProfile(resolve(Config{}))
	require.NoError(t, err)
	require.Equal(t, "(version 1)\n(allow default)\n", profile)
}

func TestSeatbeltProfile_WithRules(t *testing.T) {
	t.Parallel()

	profile, err := seatbeltProfile(resolve(Config{ReadOnly: []string{"/"}, ReadWrite: []string{"/work"}}))
	require.NoError(t, err)
	require.Contains(t, profile, "(version 1)\n(allow default)\n")
	require.Contains(t, profile, "(deny file-read* file-write*)\n")
	require.Contains(t, profile, "(allow file-read-metadata)\n")
	require.Contains(t, profile, `(allow file-read* (subpath "/") (subpath "/work"))`)
	require.Contains(t, profile, `(allow file-write* (subpath "/work"))`)
	require.Contains(t, profile, `(allow file-write* (literal "/dev/null"))`)
	require.Contains(t, profile, `(literal "/dev/urandom")`)
}

// TestSeatbeltProfile_WithFileRules verifies ReadOnlyFiles and
// ReadWriteFiles get "literal" rules, not "subpath" rules: granting a
// single file must not also grant every other file in its parent
// directory, which is what a "subpath" rule for that directory would
// do. The /dev/null write grant lands in the same "literal" clause as
// the user's own file grant, since resolve folds both into
// ruleSet.writeFiles; see profile.go.
func TestSeatbeltProfile_WithFileRules(t *testing.T) {
	t.Parallel()

	profile, err := seatbeltProfile(resolve(Config{
		ReadOnlyFiles:  []string{"/data/ro-file.txt"},
		ReadWriteFiles: []string{"/work/secrets/key.txt"},
	}))
	require.NoError(t, err)
	require.Contains(t, profile, `(literal "/data/ro-file.txt")`)
	require.Contains(t, profile, `(allow file-write* (literal "/work/secrets/key.txt") (literal "/dev/null"))`)
	require.NotContains(t, profile, `(subpath "/work/secrets")`)
	require.NotContains(t, profile, `(subpath "/data")`)
}

func TestSeatbeltProfile_RejectsControlCharacters(t *testing.T) {
	t.Parallel()

	_, err := seatbeltProfile(resolve(Config{ReadWrite: []string{"/tmp/has\nnewline"}}))
	require.Error(t, err)
}

// TestSeatbeltSandbox_EnterSandbox_RefusesNetworkRestriction verifies
// AllowNetwork false fails closed on every platform instead of
// silently leaving children's network access open: an
// already-sandboxed process cannot apply a second, tighter profile to
// its own children the way Landlock's restrictChildNetwork side
// channel does, so pretending to honor the request would be worse
// than refusing it outright. Not parallel: it forces the shared
// entered and restrictChildNetwork package vars to a known baseline
// for the duration of the call (a real EnterSandbox call earlier in
// this binary would otherwise make this a silent no-op, or leave
// restrictChildNetwork already true) and restores both afterward.
func TestSeatbeltSandbox_EnterSandbox_RefusesNetworkRestriction(t *testing.T) {
	origEntered := entered.Swap(false)
	t.Cleanup(func() { entered.Store(origEntered) })
	origNetwork := restrictChildNetwork.Swap(false)
	t.Cleanup(func() { restrictChildNetwork.Store(origNetwork) })

	err := (SeatbeltSandbox{}).EnterSandbox(Config{ReadOnly: []string{"/"}, AllowNetwork: false})
	require.ErrorIs(t, err, ErrNotSupported)
	require.False(t, ShouldRestrictChildNetwork(), "a refused EnterSandbox call must not mark children for network restriction")
}
