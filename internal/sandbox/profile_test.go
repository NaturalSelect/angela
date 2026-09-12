package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolve_EmptyConfigIsEmpty(t *testing.T) {
	t.Parallel()

	rs := resolve(Config{})
	require.True(t, rs.empty())
	require.Empty(t, rs.readFiles)
	require.Empty(t, rs.writeFiles)
}

func TestResolve_ReadWriteDirsAlsoReadable(t *testing.T) {
	t.Parallel()

	rs := resolve(Config{ReadOnly: []string{"/ro"}, ReadWrite: []string{"/rw"}})
	require.ElementsMatch(t, []string{"/ro", "/rw"}, rs.readDirs)
	require.Equal(t, []string{"/rw"}, rs.writeDirs)
}

func TestResolve_ReadWriteFilesAlsoReadable(t *testing.T) {
	t.Parallel()

	rs := resolve(Config{ReadOnlyFiles: []string{"/ro.txt"}, ReadWriteFiles: []string{"/rw.txt"}})
	require.ElementsMatch(t, append([]string{"/ro.txt", "/rw.txt"}, safeDevReadFiles...), rs.readFiles)
	require.ElementsMatch(t, append([]string{"/rw.txt"}, safeDevWriteFiles...), rs.writeFiles)
}

func TestResolve_DedupesAcrossFields(t *testing.T) {
	t.Parallel()

	rs := resolve(Config{ReadOnly: []string{"/same"}, ReadWrite: []string{"/same"}})
	require.Equal(t, []string{"/same"}, rs.readDirs)
}

// TestResolve_DevFilesOnlyAddedWithAUserPath pins the exception
// carved out for a bare Config{}: EnterSandbox must still be a
// no-op restricting nothing when the caller configured no paths at
// all, on both platforms, rather than silently granting the /dev
// exceptions alone.
func TestResolve_DevFilesOnlyAddedWithAUserPath(t *testing.T) {
	t.Parallel()

	rs := resolve(Config{ReadOnly: []string{"/"}})
	require.ElementsMatch(t, safeDevReadFiles, rs.readFiles)
	require.ElementsMatch(t, safeDevWriteFiles, rs.writeFiles)

	require.True(t, resolve(Config{}).empty())
	require.Empty(t, resolve(Config{}).readFiles)
}

func TestResolve_AllowNetworkMapsToRestrictChildNetwork(t *testing.T) {
	t.Parallel()

	require.False(t, resolve(Config{ReadOnly: []string{"/"}, AllowNetwork: true}).restrictChildNetwork)
	require.True(t, resolve(Config{ReadOnly: []string{"/"}, AllowNetwork: false}).restrictChildNetwork)
}

func TestRuleSet_ExistingDropsMissingPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "present.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	missing := filepath.Join(dir, "missing.txt")

	rs := ruleSet{
		readDirs:  []string{dir, filepath.Join(dir, "no-such-dir")},
		readFiles: []string{file, missing},
	}.existing()

	require.Equal(t, []string{dir}, rs.readDirs)
	require.Equal(t, []string{file}, rs.readFiles)
	require.Empty(t, rs.writeDirs)
	require.Empty(t, rs.writeFiles)
}

func TestRuleSet_ExistingPreservesRestrictChildNetwork(t *testing.T) {
	t.Parallel()

	rs := ruleSet{restrictChildNetwork: true}.existing()
	require.True(t, rs.restrictChildNetwork)
}
