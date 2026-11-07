package scratch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func age(t *testing.T, path string, by time.Duration) {
	t.Helper()
	when := time.Now().Add(-by)
	require.NoError(t, os.Chtimes(path, when, when))
}

func write(t *testing.T, path string, size int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
}

func hourAgo() time.Time { return time.Now().Add(-time.Hour) }

func TestSweepRemovesOnlyEntriesOlderThanTheCutoff(t *testing.T) {
	t.Parallel()

	for _, name := range dirs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			stale := filepath.Join(dataDir, name, "stale.bin")
			recent := filepath.Join(dataDir, name, "recent.bin")
			write(t, stale, 10)
			write(t, recent, 10)
			age(t, stale, 2*time.Hour)
			age(t, recent, 30*time.Minute)

			res, err := Sweep(dataDir, hourAgo(), false)
			require.NoError(t, err)

			require.NoFileExists(t, stale)
			require.FileExists(t, recent)
			require.Equal(t, Result{Entries: 1, Bytes: 10}, res)
		})
	}
}

func TestSweepRemovesAWholeSessionDirectoryWhenEverythingInItIsOld(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	session := filepath.Join(dataDir, ToolOutputDir, "s1")
	write(t, filepath.Join(session, "output-1.txt"), 100)
	write(t, filepath.Join(session, "nested", "output-2.txt"), 50)
	age(t, filepath.Join(session, "output-1.txt"), 3*time.Hour)
	age(t, filepath.Join(session, "nested", "output-2.txt"), 3*time.Hour)
	age(t, filepath.Join(session, "nested"), 3*time.Hour)
	age(t, session, 3*time.Hour)

	res, err := Sweep(dataDir, hourAgo(), false)
	require.NoError(t, err)

	require.NoDirExists(t, session)
	require.Equal(t, Result{Entries: 1, Bytes: 150}, res)
}

// A directory's own timestamp only moves when entries are added or removed,
// so a file rewritten in place must still protect the directory around it.
func TestSweepKeepsADirectoryHoldingARecentFile(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	session := filepath.Join(dataDir, WebFetchDir, "s1")
	old := filepath.Join(session, "page-old.md")
	recent := filepath.Join(session, "page-new.md")
	write(t, old, 10)
	write(t, recent, 10)
	age(t, old, 5*time.Hour)
	age(t, recent, 10*time.Minute)
	age(t, session, 5*time.Hour)

	res, err := Sweep(dataDir, hourAgo(), false)
	require.NoError(t, err)

	require.FileExists(t, old)
	require.FileExists(t, recent)
	require.Zero(t, res)
}

func TestSweepDryRunCountsWithoutDeleting(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	stale := filepath.Join(dataDir, MCPMediaDir, "shot.png")
	write(t, stale, 64)
	age(t, stale, 2*time.Hour)

	res, err := Sweep(dataDir, hourAgo(), true)
	require.NoError(t, err)

	require.FileExists(t, stale)
	require.Equal(t, Result{Entries: 1, Bytes: 64}, res)
}

func TestSweepIgnoresMissingDirectories(t *testing.T) {
	t.Parallel()

	res, err := Sweep(filepath.Join(t.TempDir(), "absent"), hourAgo(), false)
	require.NoError(t, err)
	require.Zero(t, res)
}

// The data directory also holds config, user commands and skills, the log
// and the database, none of which a sweep may touch however old they are.
func TestSweepLeavesTheRestOfTheDataDirectoryAlone(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	keep := []string{
		"angela.db",
		"angela.json",
		"init",
		filepath.Join("commands", "deploy.md"),
		filepath.Join("skills", "x", "SKILL.md"),
		filepath.Join("agents", "a.md"),
		filepath.Join("logs", "angela.log"),
		filepath.Join("stats", "index.html"),
	}
	for _, rel := range keep {
		path := filepath.Join(dataDir, rel)
		write(t, path, 5)
		age(t, path, 48*time.Hour)
	}
	loose := filepath.Join(dataDir, ToolOutputDir+"-extra", "x.txt")
	write(t, loose, 5)
	age(t, loose, 48*time.Hour)

	res, err := Sweep(dataDir, hourAgo(), false)
	require.NoError(t, err)

	require.Zero(t, res)
	for _, rel := range keep {
		require.FileExists(t, filepath.Join(dataDir, rel))
	}
	require.FileExists(t, loose)
}

func TestSweepDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	precious := filepath.Join(outside, "precious.txt")
	write(t, precious, 5)
	age(t, precious, 48*time.Hour)

	t.Run("a symlinked scratch directory is skipped", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(dataDir, ToolOutputDir)))

		res, err := Sweep(dataDir, time.Now().Add(time.Hour), false)
		require.NoError(t, err)

		require.FileExists(t, precious)
		require.Zero(t, res)
	})

	t.Run("a symlink inside a scratch directory is removed but not its target", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		link := filepath.Join(dataDir, ViewerDir, "link")
		require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o700))
		require.NoError(t, os.Symlink(outside, link))

		// NOTE: The cutoff lies in the future so the fresh link counts as old.
		res, err := Sweep(dataDir, time.Now().Add(time.Hour), false)
		require.NoError(t, err)

		_, statErr := os.Lstat(link)
		require.ErrorIs(t, statErr, os.ErrNotExist)
		require.FileExists(t, precious)
		require.Equal(t, 1, res.Entries)
	})
}
