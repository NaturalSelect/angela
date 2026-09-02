package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/lock"
	"github.com/stretchr/testify/require"
)

func TestConnect_SharesConnectionForSameDataDir(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()

	conn1, err := Connect(context.Background(), dataDir)
	require.NoError(t, err)

	conn2, err := Connect(context.Background(), dataDir)
	require.NoError(t, err)

	require.Same(t, conn1, conn2, "should return the same *sql.DB for the same data dir")

	// Releasing once should not close the connection.
	require.NoError(t, Release(dataDir))
	require.NoError(t, conn1.PingContext(context.Background()), "connection should still be usable after partial release")

	// Releasing again should close it.
	require.NoError(t, Release(dataDir))
	require.Error(t, conn1.PingContext(context.Background()), "connection should be closed after final release")
}

func TestConnect_SeparateConnectionsForDifferentDataDirs(t *testing.T) {
	t.Cleanup(ResetPool)

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	conn1, err := Connect(context.Background(), dir1)
	require.NoError(t, err)

	conn2, err := Connect(context.Background(), dir2)
	require.NoError(t, err)

	require.NotSame(t, conn1, conn2, "different data dirs should get different connections")

	require.NoError(t, Release(dir1))
	require.NoError(t, Release(dir2))
}

func TestRelease_NoopForUnknownDataDir(t *testing.T) {
	t.Cleanup(ResetPool)

	require.NoError(t, Release("/nonexistent/path"), "releasing unknown data dir should not error")
}

// TestConnect_FailsWhenDataDirLocked simulates a second angela process by
// taking the data-dir lock directly via the OS primitive on a separate
// file descriptor and then asserting that Connect surfaces a clean
// ErrDataDirLocked instead of opening the database under contention.
func TestConnect_FailsWhenDataDirLocked(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	release, err := lock.TryFile(lockPath)
	require.NoError(t, err, "expected to take the data-dir lock for the first time")
	t.Cleanup(release)

	_, err = Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.Error(t, err, "Connect must refuse to open a locked data dir")
	require.ErrorIs(t, err, ErrDataDirLocked)
}

// TestConnect_SucceedsAfterContenderReleases ensures the lock is purely
// advisory and that a clean release lets the next Connect proceed.
func TestConnect_SucceedsAfterContenderReleases(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	release, err := lock.TryFile(lockPath)
	require.NoError(t, err)

	_, err = Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.ErrorIs(t, err, ErrDataDirLocked)

	release()

	conn, err := Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.NoError(t, err, "Connect should succeed once the contender releases the lock")
	require.NoError(t, conn.PingContext(context.Background()))
	require.NoError(t, Release(dataDir))
}

// TestConnect_LockReleasedOnFinalRelease confirms that closing the last
// reference to a pool entry also drops the OS lock, so subsequent
// processes can take the data dir.
func TestConnect_LockReleasedOnFinalRelease(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	conn, err := Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.NoError(t, err)
	require.NoError(t, conn.PingContext(context.Background()))

	// Holding the in-process entry must keep the OS lock held so a
	// "second process" (simulated by a fresh lock.TryFile call) is
	// rejected.
	_, lockErr := lock.TryFile(lockPath)
	require.Error(t, lockErr)
	require.True(t, errors.Is(lockErr, lock.ErrContended), "expected contended lock while pool entry is live")

	require.NoError(t, Release(dataDir))

	// After the final release the lock is free again.
	release, err := lock.TryFile(lockPath)
	require.NoError(t, err, "expected lock to be released after final Release")
	release()
}

// TestConnect_SharedPoolDoesNotReacquireLock makes sure that subsequent
// in-process Connect calls reuse the existing OS lock through refcount,
// not by re-acquiring it. The simplest observable signal of correctness
// is that the second Connect does not error and the lock is still held
// after a single Release.
func TestConnect_SharedPoolDoesNotReacquireLock(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	_, err := Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.NoError(t, err)

	_, err = Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.NoError(t, err)

	// Drop one reference; lock must still be held.
	require.NoError(t, Release(dataDir))
	_, lockErr := lock.TryFile(lockPath)
	require.ErrorIs(t, lockErr, lock.ErrContended)

	require.NoError(t, Release(dataDir))
}

// TestConnect_SkipLockEnvBypassesAcquisition exercises the escape
// hatch used by users on filesystems where flock is unreliable.
func TestConnect_SkipLockEnvBypassesAcquisition(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	release, err := lock.TryFile(lockPath)
	require.NoError(t, err)
	t.Cleanup(release)

	t.Setenv("ANGELA_SKIP_DATADIR_LOCK", "1")

	conn, err := Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.NoError(t, err, "skip-lock env should bypass contention")
	require.NoError(t, conn.PingContext(context.Background()))
	require.NoError(t, Release(dataDir))
}

// TestConnect_DefaultIgnoresContendedLock confirms that without
// WithDataDirLock(true) the lock file is irrelevant: a contender can
// hold lock.TryFile and Connect still succeeds. This pins the
// local-mode default to its pre-lock behavior.
func TestConnect_DefaultIgnoresContendedLock(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	release, err := lock.TryFile(lockPath)
	require.NoError(t, err, "expected to take the data-dir lock for the first time")
	t.Cleanup(release)

	conn, err := Connect(context.Background(), dataDir)
	require.NoError(t, err, "default Connect must not take the lock and must succeed under contention")
	require.NoError(t, conn.PingContext(context.Background()))
	require.NoError(t, Release(dataDir))
}

// TestConnect_ServerPathFailsWhenDataDirLocked is the server's
// workspace-bootstrap analogue of TestConnect_FailsWhenDataDirLocked:
// passing WithDataDirLock(true) must surface ErrDataDirLocked when a
// contender already holds the lock.
func TestConnect_ServerPathFailsWhenDataDirLocked(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	release, err := lock.TryFile(lockPath)
	require.NoError(t, err, "expected to take the data-dir lock for the first time")
	t.Cleanup(release)

	_, err = Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.Error(t, err, "server-path Connect must refuse to open a locked data dir")
	require.ErrorIs(t, err, ErrDataDirLocked)
}

func TestConnect_FailsWhenDataDirEmpty(t *testing.T) {
	t.Cleanup(ResetPool)

	_, err := Connect(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "data.dir is not set")
}

// TestConnect_FailsWhenDataDirCannotBeCreated covers the os.MkdirAll
// failure branch by pointing the data dir at a path whose parent
// segment is a regular file, which MkdirAll refuses to descend into.
func TestConnect_FailsWhenDataDirCannotBeCreated(t *testing.T) {
	t.Cleanup(ResetPool)

	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	dataDir := filepath.Join(blocker, "sub")
	_, err := Connect(context.Background(), dataDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create data directory")
}

func TestConnectReadOnly_EmptyPath(t *testing.T) {
	_, err := ConnectReadOnly(context.Background(), "")
	require.Error(t, err)
}

// TestConnectReadOnly_FailsWhenDatabaseMissing covers the read-only
// Ping failure branch: mode=ro never creates the file, so pinging a
// path that doesn't exist surfaces a clean error.
func TestConnectReadOnly_FailsWhenDatabaseMissing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "does-not-exist.db")

	_, err := ConnectReadOnly(context.Background(), dbPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to connect to database")
}

// TestConnect_FailsWhenPingFails covers the PingContext failure
// branch and confirms the data-dir lock is released on that path too:
// a directory at the database path can be opened lazily by sql.Open
// but fails on the first real connection attempt.
func TestConnect_FailsWhenPingFails(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "angela.db")
	require.NoError(t, os.Mkdir(dbPath, 0o700))

	_, err := Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to connect to database")

	lockPath := filepath.Join(dataDir, dataDirLockFile)
	release, lockErr := lock.TryFile(lockPath)
	require.NoError(t, lockErr, "expected lock to be released after Ping failure")
	release()
}

func TestConnectReadOnly_OpensExistingDatabase(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	_, err := Connect(context.Background(), dataDir)
	require.NoError(t, err)
	require.NoError(t, Release(dataDir))

	dbPath := filepath.Join(dataDir, "angela.db")
	roConn, err := ConnectReadOnly(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { roConn.Close() })

	require.NoError(t, roConn.PingContext(context.Background()))
}

// TestResetPool_ClosesConnectionsAndReleasesLocks targets the loop
// body of ResetPool directly: most other tests already release their
// connections before cleanup runs, leaving the pool empty by the time
// ResetPool executes.
func TestResetPool_ClosesConnectionsAndReleasesLocks(t *testing.T) {
	t.Cleanup(ResetPool)

	dataDir := t.TempDir()
	lockPath := filepath.Join(dataDir, dataDirLockFile)

	conn, err := Connect(context.Background(), dataDir, WithDataDirLock(true))
	require.NoError(t, err)
	require.NoError(t, conn.PingContext(context.Background()))

	ResetPool()

	require.Error(t, conn.PingContext(context.Background()), "connection should be closed after ResetPool")

	release, err := lock.TryFile(lockPath)
	require.NoError(t, err, "expected data-dir lock to be released after ResetPool")
	release()
}
